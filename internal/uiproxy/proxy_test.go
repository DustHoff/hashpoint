package uiproxy

import (
	"context"
	"testing"

	"google.golang.org/grpc"

	"github.com/dusthoff/hashpoint/internal/ipc/collectorpb"
)

// fakeClient is a CollectorServiceClient that records the last Invoke and
// returns a scripted response, so the proxy's marshalling can be tested
// without a collector.
type fakeClient struct {
	method string
	args   []byte
	result []byte
	errMsg string
}

func (f *fakeClient) GetVersion(context.Context, *collectorpb.GetVersionRequest, ...grpc.CallOption) (*collectorpb.GetVersionResponse, error) {
	panic("unused")
}

func (f *fakeClient) Events(context.Context, *collectorpb.EventsRequest, ...grpc.CallOption) (collectorpb.CollectorService_EventsClient, error) {
	panic("unused")
}

func (f *fakeClient) ShowUI(context.Context, *collectorpb.ShowUIRequest, ...grpc.CallOption) (*collectorpb.ShowUIResponse, error) {
	panic("unused")
}

func (f *fakeClient) Invoke(_ context.Context, req *collectorpb.InvokeRequest, _ ...grpc.CallOption) (*collectorpb.InvokeResponse, error) {
	f.method, f.args = req.Method, req.Args
	return &collectorpb.InvokeResponse{Result: f.result, Error: f.errMsg}, nil
}

func TestProxy_SingleStructReturn(t *testing.T) {
	fc := &fakeClient{result: []byte(`{"version":"1.2.3","commit":"abc","build_date":"d"}`)}
	v := New(fc).Version()
	if v.Version != "1.2.3" || v.Commit != "abc" {
		t.Errorf("Version() = %+v", v)
	}
	if fc.method != "Version" {
		t.Errorf("method = %q, want Version", fc.method)
	}
}

func TestProxy_SingleScalarReturn(t *testing.T) {
	fc := &fakeClient{result: []byte(`true`)}
	if !New(fc).IsTrackingPaused() {
		t.Error("IsTrackingPaused() = false, want true")
	}
}

func TestProxy_MultiReturn(t *testing.T) {
	fc := &fakeClient{result: []byte(`[7, true]`)}
	id, active := New(fc).IsManualTagActive()
	if id != 7 || !active {
		t.Errorf("IsManualTagActive() = (%d, %v), want (7, true)", id, active)
	}
}

func TestProxy_ArgsAndDomainError(t *testing.T) {
	fc := &fakeClient{errMsg: "boom"}
	err := New(fc).DeleteTag(42)
	if err == nil || err.Error() != "boom" {
		t.Errorf("DeleteTag err = %v, want boom", err)
	}
	if string(fc.args) != `[42]` {
		t.Errorf("args = %s, want [42]", fc.args)
	}
}
