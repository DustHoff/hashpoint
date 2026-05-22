package ipc

import (
	"fmt"
	"net"

	"google.golang.org/grpc"

	"github.com/dusthoff/hashpoint/internal/ipc/collectorpb"
)

// Server hosts CollectorService over a per-user named pipe. It is created by
// the collector process; the UI connects with Dial.
type Server struct {
	grpc *grpc.Server
	lis  net.Listener
}

// NewServer opens the user-restricted pipe at name, builds a gRPC server, and
// registers svc on it. The server does not accept connections until Serve is
// called. On non-Windows builds this fails with an unsupported-platform error.
func NewServer(name string, svc collectorpb.CollectorServiceServer, opts ...grpc.ServerOption) (*Server, error) {
	lis, err := listenPipe(name)
	if err != nil {
		return nil, err
	}
	gs := grpc.NewServer(opts...)
	collectorpb.RegisterCollectorServiceServer(gs, svc)
	return &Server{grpc: gs, lis: lis}, nil
}

// Serve blocks, handling requests until Stop is called or the listener fails.
func (s *Server) Serve() error {
	if err := s.grpc.Serve(s.lis); err != nil {
		return fmt.Errorf("ipc serve: %w", err)
	}
	return nil
}

// Stop gracefully drains in-flight RPCs and closes the pipe listener.
func (s *Server) Stop() { s.grpc.GracefulStop() }
