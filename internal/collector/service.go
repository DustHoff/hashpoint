package collector

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dusthoff/hashpoint/internal/ipc"
	"github.com/dusthoff/hashpoint/internal/ipc/collectorpb"
)

// EventShowUI is the control event the collector publishes to ask the UI to
// foreground its window. The UI's event pump acts on it locally rather than
// forwarding it to the frontend.
const EventShowUI = "ui:show"

// EventQuickTagEnter and EventQuickTagLeave are control events the collector
// publishes to drive the UI's quick-tag popup window: enter shrinks the UI
// window into the popup (saving its placement), leave restores it. Like
// EventShowUI the UI's event pump acts on them locally against its own Wails
// context rather than forwarding them to the frontend (issue #28).
const (
	EventQuickTagEnter = "ui:quicktag-enter"
	EventQuickTagLeave = "ui:quicktag-leave"
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

// ShowUI publishes the show-window control event so the connected UI brings
// itself to the foreground. Called by a second app launch that found the
// collector already running.
func (s *Service) ShowUI(context.Context, *collectorpb.ShowUIRequest) (*collectorpb.ShowUIResponse, error) {
	s.hub.Publish(EventShowUI, nil)
	return &collectorpb.ShowUIResponse{}, nil
}
