package main

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/mhsanaei/3x-ui/v3/internal/elxcontrol/corepolicy"
	"github.com/mhsanaei/3x-ui/v3/internal/elxcontrol/journal"
	"github.com/mhsanaei/3x-ui/v3/internal/elxcontrol/policytransport"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func transportTLS() (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(os.Getenv("ELX_TLS_CERT"), os.Getenv("ELX_TLS_KEY"))
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(os.Getenv("ELX_TLS_CA"))
	if err != nil {
		return nil, err
	}
	ca := x509.NewCertPool()
	if !ca.AppendCertsFromPEM(data) {
		return nil, errors.New("invalid TLS CA")
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}, RootCAs: ca, ClientCAs: ca}, nil
}

func loadBinding() (policytransport.Binding, error) {
	var binding policytransport.Binding
	data, err := os.ReadFile(os.Getenv("ELX_POLICY_BINDING"))
	if err != nil {
		return binding, err
	}
	if err := json.Unmarshal(data, &binding); err != nil {
		return binding, err
	}
	if !binding.Valid() {
		return binding, errors.New("invalid policy binding")
	}
	return binding, nil
}

func servePolicy(ctx context.Context, db *sql.DB) error {
	binding, err := loadBinding()
	if err != nil {
		return err
	}
	cfg, err := transportTLS()
	if err != nil {
		return err
	}
	cfg.ClientAuth = tls.RequireAndVerifyClientCert
	addr := os.Getenv("ELX_POLICY_LISTEN")
	if addr == "" {
		return errors.New("ELX_POLICY_LISTEN is required")
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer listener.Close()
	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(cfg)), grpc.MaxRecvMsgSize(4096))
	if err := policytransport.Register(server, db, binding); err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, server.Stop)
	defer stop()
	err = server.Serve(listener)
	if ctx.Err() != nil {
		return nil
	}
	return err
}

func enqueuePolicy(ctx context.Context, db *sql.DB, args []string) error {
	binding, err := loadBinding()
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("enqueue-policy", flag.ContinueOnError)
	op := fs.String("operation", "", "stable UUID for retries")
	expected := fs.Int64("expected", -1, "expected journal revision")
	up := fs.Uint64("up", 0, "upload bytes per second")
	down := fs.Uint64("down", 0, "download bytes per second")
	disabled := fs.Bool("disabled", false, "disable subscription")
	if err := fs.Parse(args); err != nil {
		return err
	}
	id, err := uuid.Parse(*op)
	rates := policytransport.Rates{Up: *up, Down: *down, Disabled: *disabled}
	if fs.NArg() != 0 || err != nil || id == uuid.Nil || id.String() != *op || !rates.Valid() || *expected < 0 || *expected > 1<<63-3 {
		return errors.New("valid operation UUID, expected revision and positive rates required")
	}
	payload, err := json.Marshal(rates)
	if err != nil {
		return err
	}
	rev, err := journal.Enqueue(ctx, db, binding.Target, journal.Command{ID: *op, ExpectedRevision: *expected, Payload: payload})
	if err == nil {
		fmt.Println(rev)
	}
	return err
}

func applyPolicy(ctx context.Context) error {
	cfg, err := transportTLS()
	if err != nil {
		return err
	}
	endpoint := os.Getenv("ELX_POLICY_CONTROL")
	if endpoint == "" {
		return errors.New("ELX_POLICY_CONTROL is required")
	}
	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(credentials.NewTLS(cfg)), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(4096)))
	if err != nil {
		return err
	}
	defer conn.Close()
	var core *corepolicy.Client
	if addr := os.Getenv("ELXRAY_POLICY_LOOPBACK"); addr != "" {
		core, err = corepolicy.OpenLoopback(addr)
	} else {
		core, err = corepolicy.Open(os.Getenv("ELXRAY_POLICY_SOCKET"))
	}
	if err != nil {
		return err
	}
	defer core.Close()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return policytransport.ApplyNext(ctx, conn, core)
}

func printPolicyKey(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: elx-control policy-public-key SEED_FILE")
	}
	data, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	seed, err := hex.DecodeString(string(data))
	if err != nil || len(seed) != 32 {
		return errors.New("invalid signing seed")
	}
	fmt.Println(base64.StdEncoding.EncodeToString(ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)))
	return nil
}
