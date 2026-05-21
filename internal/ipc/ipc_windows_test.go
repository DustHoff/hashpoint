//go:build windows

package ipc_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/dusthoff/hashpoint/internal/ipc"
	"github.com/dusthoff/hashpoint/internal/ipc/collectorpb"
)

// stubServer is a minimal CollectorService used to prove the named-pipe + gRPC
// round-trip end to end. The real implementation, wrapping the domain, lands
// in the collector in a later phase.
type stubServer struct {
	collectorpb.UnimplementedCollectorServiceServer
}

func (stubServer) GetVersion(context.Context, *collectorpb.GetVersionRequest) (*collectorpb.GetVersionResponse, error) {
	return &collectorpb.GetVersionResponse{
		Version:         "test",
		Commit:          "deadbeef",
		ProtocolVersion: ipc.ProtocolVersion,
	}, nil
}

// TestPipeRoundTrip_GetVersion starts the server on a user-restricted pipe and
// calls GetVersion through a real gRPC client, exercising the listener, the
// ACL, the dialer and the generated stubs together.
func TestPipeRoundTrip_GetVersion(t *testing.T) {
	name := ipc.PipeName(fmt.Sprintf("test-%d", os.Getpid()))

	srv, err := ipc.NewServer(name, stubServer{})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve() }()
	defer func() {
		srv.Stop()
		if err := <-serveErr; err != nil {
			t.Errorf("serve returned: %v", err)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cli, err := ipc.Dial(ctx, name)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = cli.Close() }()

	resp, err := cli.GetVersion(ctx, &collectorpb.GetVersionRequest{})
	if err != nil {
		t.Fatalf("GetVersion: %v", err)
	}
	if resp.Version != "test" {
		t.Errorf("version = %q, want %q", resp.Version, "test")
	}
	if resp.ProtocolVersion != ipc.ProtocolVersion {
		t.Errorf("protocol version = %d, want %d", resp.ProtocolVersion, ipc.ProtocolVersion)
	}
}
