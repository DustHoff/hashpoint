package sdk

import (
	"context"
	"testing"
)

// stubCore implements Plugin without any capability. PluginMap should
// give it just the core service.
type stubCore struct{}

func (stubCore) Init(_ context.Context, _ HostAPI) error           { return nil }
func (stubCore) Metadata(_ context.Context) (Metadata, error)      { return Metadata{}, nil }
func (stubCore) Configure(_ context.Context, _ PluginConfig) error { return nil }

// stubOnCall adds the on-call capability.
type stubOnCall struct{ stubCore }

func (stubOnCall) Submit(_ context.Context, _ OnCallDocument) (SubmissionResult, error) {
	return SubmissionResult{}, nil
}

// stubMgmt adds the plugin-management capability.
type stubMgmt struct{ stubCore }

func (stubMgmt) ListAvailable(_ context.Context) ([]AvailablePlugin, error) { return nil, nil }
func (stubMgmt) Install(_ context.Context, _ string) error                  { return nil }
func (stubMgmt) Update(_ context.Context, _ string) error                   { return nil }
func (stubMgmt) Uninstall(_ context.Context, _ string) error                { return nil }

// stubBoth advertises both capabilities at once.
type stubBoth struct {
	stubCore
	stubOnCall
	stubMgmt
}

func TestPluginMap_CoreOnly(t *testing.T) {
	set := PluginMap(stubCore{})
	if _, ok := set[CoreKey]; !ok {
		t.Errorf("CoreKey missing")
	}
	if _, ok := set[OnCallKey]; ok {
		t.Errorf("OnCallKey unexpectedly present for plugin without capability")
	}
	if _, ok := set[MgmtKey]; ok {
		t.Errorf("MgmtKey unexpectedly present for plugin without capability")
	}
}

func TestPluginMap_OnCall(t *testing.T) {
	set := PluginMap(stubOnCall{})
	if _, ok := set[OnCallKey]; !ok {
		t.Errorf("OnCallKey missing for OnCallDocumentationHandler")
	}
	if _, ok := set[MgmtKey]; ok {
		t.Errorf("MgmtKey unexpectedly present")
	}
}

func TestPluginMap_Mgmt(t *testing.T) {
	set := PluginMap(stubMgmt{})
	if _, ok := set[MgmtKey]; !ok {
		t.Errorf("MgmtKey missing for PluginManagementHandler")
	}
	if _, ok := set[OnCallKey]; ok {
		t.Errorf("OnCallKey unexpectedly present")
	}
}

func TestPluginMap_BothCapabilities(t *testing.T) {
	set := PluginMap(stubBoth{})
	for _, key := range []string{CoreKey, OnCallKey, MgmtKey} {
		if _, ok := set[key]; !ok {
			t.Errorf("expected %q in plugin set, missing", key)
		}
	}
}

func TestHostSidePluginMap_IncludesAllKeys(t *testing.T) {
	set := HostSidePluginMap()
	for _, key := range []string{CoreKey, OnCallKey, MgmtKey} {
		if _, ok := set[key]; !ok {
			t.Errorf("HostSidePluginMap missing %q", key)
		}
	}
}
