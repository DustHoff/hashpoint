package ipc

import (
	"context"
	"fmt"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/dusthoff/hashpoint/internal/ipc/collectorpb"
)

// Client is a CollectorService client connected to the collector over its
// named pipe. It embeds the generated client so callers invoke RPCs directly.
type Client struct {
	conn *grpc.ClientConn
	collectorpb.CollectorServiceClient
}

// Dial connects to the collector pipe at name, blocking until the connection
// is established or ctx expires. The pipe's user-only ACL already authorises
// the channel, so the gRPC layer uses insecure credentials — there is no TLS
// over a local kernel object. A dummy passthrough target keeps the pipe path
// (with its backslashes) out of gRPC's URI resolver; the custom dialer uses
// the captured name.
func Dial(ctx context.Context, name string) (*Client, error) {
	conn, err := grpc.DialContext(ctx, "passthrough:///collector",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(dialCtx context.Context, _ string) (net.Conn, error) {
			return dialPipe(dialCtx, name)
		}),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, fmt.Errorf("dial collector %s: %w", name, err)
	}
	return &Client{conn: conn, CollectorServiceClient: collectorpb.NewCollectorServiceClient(conn)}, nil
}

// Close tears down the connection to the collector.
func (c *Client) Close() error { return c.conn.Close() }
