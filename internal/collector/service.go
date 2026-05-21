package collector

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dusthoff/hashpoint/internal/ipc"
	"github.com/dusthoff/hashpoint/internal/ipc/collectorpb"
)

// VersionInfo is the collector build metadata reported to the UI via
// GetVersion for the compatibility handshake.
type VersionInfo struct {
	Version   string
	Commit    string
	BuildDate string
}

// Service implements collectorpb.CollectorServiceServer. Phase 1 covers the
// handshake (GetVersion) and the event push stream (Events); the ~69 domain
// methods (ADR Anhang A) are added in later phases by delegating to the
// existing repositories, tracker and orchestrator.
type Service struct {
	collectorpb.UnimplementedCollectorServiceServer
	version    VersionInfo
	hub        *EventHub
	dispatcher *Invoker
}

// NewService builds the gRPC service backed by the given build metadata, event
// hub and method dispatcher. dispatcher may be nil while the domain has not
// yet moved into the collector — Invoke then reports Unimplemented.
func NewService(version VersionInfo, hub *EventHub, dispatcher *Invoker) *Service {
	return &Service{version: version, hub: hub, dispatcher: dispatcher}
}

// GetVersion returns build metadata plus the compiled-in protocol version so
// the UI can refuse to attach to an incompatible collector.
func (s *Service) GetVersion(context.Context, *collectorpb.GetVersionRequest) (*collectorpb.GetVersionResponse, error) {
	return &collectorpb.GetVersionResponse{
		Version:         s.version.Version,
		Commit:          s.version.Commit,
		BuildDate:       s.version.BuildDate,
		ProtocolVersion: ipc.ProtocolVersion,
	}, nil
}

// Events streams collector events to one UI until the stream's context is
// cancelled (UI closed/disconnected) or the subscription is torn down. A UI
// disconnecting is normal, not an error, so it returns nil.
func (s *Service) Events(_ *collectorpb.EventsRequest, stream collectorpb.CollectorService_EventsServer) error {
	ch, cancel := s.hub.Subscribe()
	defer cancel()
	ctx := stream.Context()
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-ch:
			if !ok {
				return nil
			}
			if err := stream.Send(&collectorpb.Event{Name: ev.Name, JsonPayload: ev.JSON}); err != nil {
				return err
			}
		}
	}
}

// Invoke dispatches a domain method by name through the reflection dispatcher.
// Domain errors are returned in the response's Error field (not as a gRPC
// status) so the UI proxy can hand the frontend the same error the in-process
// method would have. A gRPC status is reserved for transport/setup failures.
func (s *Service) Invoke(ctx context.Context, req *collectorpb.InvokeRequest) (*collectorpb.InvokeResponse, error) {
	if s.dispatcher == nil {
		return nil, status.Error(codes.Unimplemented, "collector has no method dispatcher configured")
	}
	result, err := s.dispatcher.Invoke(ctx, req.Method, req.Args)
	resp := &collectorpb.InvokeResponse{Result: result}
	if err != nil {
		resp.Error = err.Error()
	}
	return resp, nil
}
