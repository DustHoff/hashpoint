package collector

import (
	"context"

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
	version VersionInfo
	hub     *EventHub
}

// NewService builds the gRPC service backed by the given build metadata and
// event hub.
func NewService(version VersionInfo, hub *EventHub) *Service {
	return &Service{version: version, hub: hub}
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
