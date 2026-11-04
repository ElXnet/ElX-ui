// Package policytransport exchanges durable policy commands with a provisioned node.
package policytransport

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/google/uuid"
	"github.com/mhsanaei/3x-ui/v3/internal/elxcontrol/corepolicy"
	"github.com/mhsanaei/3x-ui/v3/internal/elxcontrol/journal"
	"github.com/mhsanaei/3x-ui/v3/internal/elxcontrol/nodeidentity"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const serviceName = "elx.node.v1.PolicyDelivery"

type Binding struct {
	Target       journal.Target
	Subscription string
	Epoch        int64
	PublicKey    ed25519.PublicKey
}

func (b Binding) Valid() bool {
	return validID(b.Target.NodeID) && validID(b.Target.ResourceID) &&
		len(b.Subscription) > 0 && len(b.Subscription) <= 1024 && b.Epoch > 0 &&
		len(b.PublicKey) == ed25519.PublicKeySize
}

func validID(s string) bool {
	id, err := uuid.Parse(s)
	return err == nil && id != uuid.Nil && id.String() == s
}

type Rates struct {
	Up       uint64
	Down     uint64
	Disabled bool
}

func (r Rates) Valid() bool {
	return r.Up > 0 && r.Down > 0 && r.Up <= 1<<40 && r.Down <= 1<<40
}

type Job struct {
	Operation string
	Policy    corepolicy.Policy
	Challenge string
}

type receipt struct {
	Operation string
	Proof     []byte
}

type server struct {
	db      *sql.DB
	binding Binding
}

func (s *server) authorize(ctx context.Context) error {
	id, err := nodeidentity.FromGRPC(ctx)
	if err != nil || id != s.binding.Target.NodeID {
		return status.Error(codes.PermissionDenied, "node is not assigned")
	}
	return nil
}

func (s *server) job(cmd journal.Command) (Job, error) {
	var rates Rates
	if !validID(cmd.ID) || cmd.ExpectedRevision < 0 || cmd.ExpectedRevision > math.MaxInt64-2 ||
		json.Unmarshal(cmd.Payload, &rates) != nil || !rates.Valid() {
		return Job{}, status.Error(codes.FailedPrecondition, "invalid stored policy operation")
	}
	policy := corepolicy.Policy{ID: s.binding.Subscription, Revision: uint64(cmd.ExpectedRevision + 2),
		UplinkBps: rates.Up, DownlinkBps: rates.Down, Disabled: rates.Disabled}
	challenge := sha256.Sum256(fmt.Appendf(nil, "elx-command-v1\n%s\n%s\n%s\n%d\n%x",
		s.binding.Target.NodeID, s.binding.Target.ResourceID, cmd.ID, s.binding.Epoch, sha256.Sum256(cmd.Payload)))
	return Job{Operation: cmd.ID, Policy: policy, Challenge: fmt.Sprintf("%x", challenge)}, nil
}

func (s *server) next(ctx context.Context, _ *emptypb.Empty) (*wrapperspb.BytesValue, error) {
	if err := s.authorize(ctx); err != nil {
		return nil, err
	}
	cmd, err := journal.Pending(ctx, s.db, s.binding.Target)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, status.Error(codes.NotFound, "no pending command")
	}
	if err != nil {
		return nil, status.Error(codes.Unavailable, "journal unavailable")
	}
	job, err := s.job(cmd)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(job)
	return wrapperspb.Bytes(data), err
}

func (s *server) confirm(ctx context.Context, in *wrapperspb.BytesValue) (*emptypb.Empty, error) {
	if err := s.authorize(ctx); err != nil {
		return nil, err
	}
	var ack receipt
	if len(in.GetValue()) > 1024 || json.Unmarshal(in.GetValue(), &ack) != nil || !validID(ack.Operation) ||
		len(ack.Proof) != ed25519.SignatureSize {
		return nil, status.Error(codes.InvalidArgument, "invalid policy receipt")
	}
	cmd, err := journal.Lookup(ctx, s.db, s.binding.Target, ack.Operation)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, status.Error(codes.NotFound, "unknown command")
	}
	if err != nil {
		return nil, status.Error(codes.Unavailable, "journal unavailable")
	}
	job, err := s.job(cmd)
	if err != nil {
		return nil, err
	}
	p := job.Policy
	message := fmt.Appendf(nil, "elx-policy-v1\n%x\n%x\n%d\n%d\n%d\n%t",
		job.Challenge, p.ID, p.Revision, p.UplinkBps, p.DownlinkBps, p.Disabled)
	if !ed25519.Verify(s.binding.PublicKey, message, ack.Proof) {
		return nil, status.Error(codes.PermissionDenied, "invalid durable policy signature")
	}
	err = journal.ConfirmApplied(ctx, s.db, s.binding.Target, cmd.ID, cmd.ExpectedRevision+1, sha256.Sum256(cmd.Payload))
	switch {
	case errors.Is(err, journal.ErrRevision), errors.Is(err, journal.ErrReceipt):
		return nil, status.Error(codes.FailedPrecondition, "receipt conflicts with journal")
	case err != nil:
		return nil, status.Error(codes.Unavailable, "journal unavailable")
	}
	return &emptypb.Empty{}, nil
}

type service interface {
	next(context.Context, *emptypb.Empty) (*wrapperspb.BytesValue, error)
	confirm(context.Context, *wrapperspb.BytesValue) (*emptypb.Empty, error)
}

// Register installs the single-binding policy RPC. Callers must configure
// dedicated client CA verification on the gRPC server.
func Register(g *grpc.Server, db *sql.DB, b Binding) error {
	if db == nil || !b.Valid() {
		return errors.New("invalid policy delivery binding")
	}
	g.RegisterService(&grpc.ServiceDesc{
		ServiceName: serviceName, HandlerType: (*service)(nil),
		Methods: []grpc.MethodDesc{
			{MethodName: "Next", Handler: handler("Next", (*server).next)},
			{MethodName: "Confirm", Handler: handler("Confirm", (*server).confirm)},
		},
	}, &server{db: db, binding: b})
	return nil
}

func handler[Request, Response any](method string, call func(*server, context.Context, *Request) (*Response, error)) grpc.MethodHandler {
	return func(s any, ctx context.Context, decode func(any) error, intercept grpc.UnaryServerInterceptor) (any, error) {
		in := new(Request)
		if err := decode(in); err != nil {
			return nil, err
		}
		if intercept == nil {
			return call(s.(*server), ctx, in)
		}
		return intercept(ctx, in, &grpc.UnaryServerInfo{Server: s, FullMethod: "/" + serviceName + "/" + method},
			func(ctx context.Context, req any) (any, error) { return call(s.(*server), ctx, req.(*Request)) })
	}
}

type Applier interface {
	ApplyWithProof(context.Context, corepolicy.Policy, string) ([]byte, error)
}

// ApplyNext pulls one command, applies it locally and sends its core proof.
func ApplyNext(ctx context.Context, conn grpc.ClientConnInterface, core Applier) error {
	var data wrapperspb.BytesValue
	if err := conn.Invoke(ctx, "/"+serviceName+"/Next", &emptypb.Empty{}, &data); err != nil {
		return err
	}
	var job Job
	if err := json.Unmarshal(data.Value, &job); err != nil {
		return err
	}
	proof, err := core.ApplyWithProof(ctx, job.Policy, job.Challenge)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(receipt{Operation: job.Operation, Proof: proof})
	if err != nil {
		return err
	}
	return conn.Invoke(ctx, "/"+serviceName+"/Confirm", wrapperspb.Bytes(payload), &emptypb.Empty{})
}
