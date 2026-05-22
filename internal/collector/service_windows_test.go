//go:build windows

package collector

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/dusthoff/hashpoint/internal/ipc"
	"github.com/dusthoff/hashpoint/internal/ipc/collectorpb"
)

// TestInvokeOverPipe exercises the full Invoke path — named pipe, gRPC and the
// reflection dispatcher — against a fake domain object, without touching the
// real database. It covers both a successful call and the domain-error path
// (error carried in the response, not as a gRPC status).
func TestInvokeOverPipe(t *testing.T) {
	name := ipc.PipeName(fmt.Sprintf("collector-test-%d", os.Getpid()))
	svc := NewService(VersionInfo{Version: "test"}, NewEventHub(0), NewInvoker(fakeAPI{}))

	srv, err := ipc.NewServer(name, svc)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve() }()
	defer func() {
		srv.Stop()
		<-serveErr
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cli, err := ipc.Dial(ctx, name)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = cli.Close() }()

	resp, err := cli.Invoke(ctx, &collectorpb.InvokeRequest{Method: "Echo", Args: []byte(`["hi"]`)})
	if err != nil {
		t.Fatalf("invoke Echo: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected domain error: %s", resp.Error)
	}
	if string(resp.Result) != `"echo:hi"` {
		t.Errorf("result = %s, want %q", resp.Result, `"echo:hi"`)
	}

	resp, err = cli.Invoke(ctx, &collectorpb.InvokeRequest{Method: "Fail", Args: []byte(`[]`)})
	if err != nil {
		t.Fatalf("invoke Fail: %v", err)
	}
	if resp.Error == "" {
		t.Error("expected a domain error in the response")
	}
}
