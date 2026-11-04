// Package corepolicy applies and reads back local ElXray runtime policies.
package corepolicy

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"time"

	wire "github.com/mhsanaei/3x-ui/v3/internal/elxcontrol/corepolicy/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
)

type Client struct {
	conn *grpc.ClientConn
	rpc  wire.SubscriptionServiceClient
}

// Open connects to a local socket owned by the node supervisor. The socket
// and its parent directory must be protected by operating-system permissions.
func Open(path string) (*Client, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("absolute ElXray socket path required")
	}
	conn, err := grpc.NewClient("unix://"+path, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	return &Client{conn: conn, rpc: wire.NewSubscriptionServiceClient(conn)}, nil
}

func (c *Client) Close() error { return c.conn.Close() }

type Policy struct {
	ID          string
	Revision    uint64
	UplinkBps   uint64
	DownlinkBps uint64
	Disabled    bool
}

// Apply checks the live policy after the RPC. Its result only proves live
// state and must never be used as a durable application receipt.
func (c *Client) Apply(ctx context.Context, desired Policy) error {
	if desired.ID == "" || desired.Revision < 2 || desired.Revision > math.MaxInt64 ||
		desired.UplinkBps == 0 || desired.DownlinkBps == 0 {
		return errors.New("invalid runtime policy")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := c.rpc.SetSubscription(ctx, &wire.SetSubscriptionRequest{
		Id: desired.ID, ExpectedRevision: desired.Revision - 1, Revision: desired.Revision,
		UplinkBps: proto.Uint64(desired.UplinkBps), DownlinkBps: proto.Uint64(desired.DownlinkBps),
		Disabled: proto.Bool(desired.Disabled),
	})
	if err != nil {
		return fmt.Errorf("set ElXray runtime policy: %w", err)
	}
	policy, err := c.rpc.GetSubscription(ctx, &wire.GetSubscriptionRequest{Id: desired.ID})
	if err != nil {
		return fmt.Errorf("read ElXray runtime policy: %w", err)
	}
	if policy.GetId() != desired.ID || policy.GetRevision() != desired.Revision ||
		policy.GetUplinkBps() != desired.UplinkBps || policy.GetDownlinkBps() != desired.DownlinkBps ||
		policy.GetDisabled() != desired.Disabled {
		return errors.New("ElXray runtime policy readback mismatch")
	}
	return nil
}
