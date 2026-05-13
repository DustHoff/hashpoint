package tagging

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dusthoff/hashpoint/internal/tracker"
)

// fakeResolver is a scripted PluginAutoTagResolver for orchestrator tests.
type fakeResolver struct {
	answer func(processName, windowTitle string, isComm bool) *PluginAutoTagMatch
	calls  atomic.Int32
}

func (f *fakeResolver) Resolve(_ context.Context, processName, windowTitle string, isComm bool) *PluginAutoTagMatch {
	f.calls.Add(1)
	if f.answer == nil {
		return nil
	}
	return f.answer(processName, windowTitle, isComm)
}

// TestPluginAutoTag_FallbackWhenNoRule: a process not covered by any
// user rule still opens an auto-tag block when the plugin resolver
// claims it.
func TestPluginAutoTag_FallbackWhenNoRule(t *testing.T) {
	e := newOrchEnv(t, 5*time.Minute)
	resolver := &fakeResolver{
		answer: func(p, _ string, _ bool) *PluginAutoTagMatch {
			if p != "code.exe" {
				return nil
			}
			return &PluginAutoTagMatch{
				PluginName: "coder",
				TagID:      e.tagCode,
			}
		},
	}
	e.orch.SetPluginResolver(resolver)

	at1 := time.Date(2026, 4, 29, 10, 3, 0, 0, time.UTC) // floors to 10:00
	e.orch.OnFocusChanged(e.ctx, "code.exe", "main.go", at1)
	at2 := time.Date(2026, 4, 29, 10, 8, 0, 0, time.UTC) // floors to 10:05
	e.orch.OnFocusChanged(e.ctx, "shell.exe", "bash", at2)

	bs := e.listTagBlocks()
	if len(bs) != 1 {
		t.Fatalf("expected 1 plugin-driven block, got %d", len(bs))
	}
	if bs[0].TagID != e.tagCode {
		t.Errorf("tag = %d, want %d", bs[0].TagID, e.tagCode)
	}
	if bs[0].IsManual {
		t.Errorf("plugin-driven block must be auto, not manual")
	}
	if resolver.calls.Load() < 1 {
		t.Errorf("resolver should have been consulted at least once")
	}
}

// TestPluginAutoTag_UserRuleWinsOverPlugin: a focus event that matches
// an enabled rule must NOT consult the resolver — user rules take
// priority. The resolver may still be consulted for OTHER focus events
// in the same scenario; we only assert it was never asked about the
// rule-covered process.
func TestPluginAutoTag_UserRuleWinsOverPlugin(t *testing.T) {
	e := newOrchEnv(t, 5*time.Minute)
	var queried []string
	var queriedMu sync.Mutex
	resolver := &fakeResolver{
		answer: func(p, _ string, _ bool) *PluginAutoTagMatch {
			queriedMu.Lock()
			queried = append(queried, p)
			queriedMu.Unlock()
			// Plugin would be happy to claim browser.exe — but the rule
			// should win and the resolver must never be asked about it.
			return &PluginAutoTagMatch{PluginName: "interloper", TagID: e.tagCode}
		},
	}
	e.orch.SetPluginResolver(resolver)

	at1 := time.Date(2026, 4, 29, 10, 3, 0, 0, time.UTC) // floors to 10:00
	e.orch.OnFocusChanged(e.ctx, "browser.exe", "Wiki", at1)
	at2 := time.Date(2026, 4, 29, 10, 8, 0, 0, time.UTC)
	e.orch.OnFocusChanged(e.ctx, "shell.exe", "", at2)

	queriedMu.Lock()
	defer queriedMu.Unlock()
	for _, p := range queried {
		if p == "browser.exe" {
			t.Errorf("resolver must not be consulted for rule-covered process; was called with %q", p)
		}
	}

	bs := e.listTagBlocks()
	// Two blocks expected: 10:00-10:05 web (from the rule),
	// 10:05-10:10 code (from the plugin claiming shell.exe).
	if len(bs) == 0 {
		t.Fatalf("expected at least one block")
	}
	if bs[0].TagID != e.tagWeb {
		t.Errorf("first block tag = %d, want %d (rule's web tag)", bs[0].TagID, e.tagWeb)
	}
}

// TestPluginAutoTag_DescriptionRoundTrip: a plugin-supplied description
// reaches the persisted block.
func TestPluginAutoTag_DescriptionRoundTrip(t *testing.T) {
	e := newOrchEnv(t, 5*time.Minute)
	resolver := &fakeResolver{
		answer: func(_, _ string, _ bool) *PluginAutoTagMatch {
			return &PluginAutoTagMatch{
				PluginName:  "coder",
				TagID:       e.tagCode,
				Description: "Focus mode",
			}
		},
	}
	e.orch.SetPluginResolver(resolver)

	e.orch.OnFocusChanged(e.ctx, "code.exe", "main.go",
		time.Date(2026, 4, 29, 10, 3, 0, 0, time.UTC))
	e.orch.OnFocusChanged(e.ctx, "shell.exe", "",
		time.Date(2026, 4, 29, 10, 8, 0, 0, time.UTC))

	bs := e.listTagBlocks()
	if len(bs) != 1 {
		t.Fatalf("expected 1 block, got %d", len(bs))
	}
	if bs[0].Description == nil || *bs[0].Description != "Focus mode" {
		t.Errorf("description = %v, want %q", bs[0].Description, "Focus mode")
	}
}

// TestPluginAutoTag_PluginToPluginTransition: switching between two
// plugins claiming different tags closes the first block and opens a
// new one (different sourceKey).
func TestPluginAutoTag_PluginToPluginTransition(t *testing.T) {
	e := newOrchEnv(t, 5*time.Minute)
	resolver := &fakeResolver{
		answer: func(p, _ string, _ bool) *PluginAutoTagMatch {
			switch p {
			case "code.exe":
				return &PluginAutoTagMatch{PluginName: "coder", TagID: e.tagCode}
			case "design.exe":
				return &PluginAutoTagMatch{PluginName: "designer", TagID: e.tagWeb}
			}
			return nil
		},
	}
	e.orch.SetPluginResolver(resolver)

	// 10:00 floor
	e.orch.OnFocusChanged(e.ctx, "code.exe", "main.go",
		time.Date(2026, 4, 29, 10, 3, 0, 0, time.UTC))
	// 10:05 floor — plugin changes
	e.orch.OnFocusChanged(e.ctx, "design.exe", "logo.fig",
		time.Date(2026, 4, 29, 10, 8, 0, 0, time.UTC))
	// 10:10 floor — close
	e.orch.OnFocusChanged(e.ctx, "shell.exe", "",
		time.Date(2026, 4, 29, 10, 13, 0, 0, time.UTC))

	bs := e.listTagBlocks()
	if len(bs) != 2 {
		t.Fatalf("expected 2 blocks (transition), got %d", len(bs))
	}
	if bs[0].TagID != e.tagCode {
		t.Errorf("first block tag = %d, want %d", bs[0].TagID, e.tagCode)
	}
	if bs[1].TagID != e.tagWeb {
		t.Errorf("second block tag = %d, want %d", bs[1].TagID, e.tagWeb)
	}
}

// TestPluginAutoTag_SamePluginSameTagNoTransition: repeated focus
// changes within the same (plugin, tag) source treat the second event
// as a no-op — no block split.
func TestPluginAutoTag_SamePluginSameTagNoTransition(t *testing.T) {
	e := newOrchEnv(t, 5*time.Minute)
	resolver := &fakeResolver{
		answer: func(_, _ string, _ bool) *PluginAutoTagMatch {
			return &PluginAutoTagMatch{PluginName: "coder", TagID: e.tagCode}
		},
	}
	e.orch.SetPluginResolver(resolver)

	e.orch.OnFocusChanged(e.ctx, "code.exe", "main.go",
		time.Date(2026, 4, 29, 10, 3, 0, 0, time.UTC))
	// Title change but same (plugin, tag) — must not split the block.
	e.orch.OnFocusChanged(e.ctx, "code.exe", "other.go",
		time.Date(2026, 4, 29, 10, 8, 0, 0, time.UTC))
	e.orch.OnFocusChanged(e.ctx, "shell.exe", "",
		time.Date(2026, 4, 29, 10, 13, 0, 0, time.UTC))

	bs := e.listTagBlocks()
	if len(bs) != 1 {
		t.Fatalf("expected 1 block (no transition), got %d", len(bs))
	}
}

// TestPluginAutoTag_CommRail: plugin claims a comm session when no rule
// matches → comm-auto block opens; closing the comm window closes the
// comm-auto.
func TestPluginAutoTag_CommRail(t *testing.T) {
	e := newOrchEnv(t, 5*time.Minute)
	resolver := &fakeResolver{
		answer: func(p, _ string, isComm bool) *PluginAutoTagMatch {
			if !isComm || p != "teams.exe" {
				return nil
			}
			return &PluginAutoTagMatch{PluginName: "meeter", TagID: e.tagWeb}
		},
	}
	e.orch.SetPluginResolver(resolver)

	at1 := time.Date(2026, 4, 29, 10, 3, 0, 0, time.UTC) // floors to 10:00
	e.orch.OnCommunicationChanged(e.ctx, []tracker.CommSession{
		{ProcessName: "teams.exe", WindowTitle: "Standup"},
	}, at1)

	at2 := time.Date(2026, 4, 29, 10, 8, 0, 0, time.UTC) // floors to 10:05
	e.orch.OnCommunicationChanged(e.ctx, nil, at2)

	bs := e.listTagBlocks()
	if len(bs) != 1 {
		t.Fatalf("expected 1 comm-auto block, got %d", len(bs))
	}
	if bs[0].TagID != e.tagWeb {
		t.Errorf("tag = %d, want %d", bs[0].TagID, e.tagWeb)
	}
}

// TestPluginAutoTag_NoResolverInstalled: with no resolver attached the
// orchestrator behaves exactly as before — no panic on unknown
// processes, no block opens.
func TestPluginAutoTag_NoResolverInstalled(t *testing.T) {
	e := newOrchEnv(t, 5*time.Minute)
	// Deliberately do NOT call SetPluginResolver.
	e.orch.OnFocusChanged(e.ctx, "unmatched.exe", "",
		time.Date(2026, 4, 29, 10, 3, 0, 0, time.UTC))
	e.orch.OnFocusChanged(e.ctx, "shell.exe", "",
		time.Date(2026, 4, 29, 10, 8, 0, 0, time.UTC))
	if bs := e.listTagBlocks(); len(bs) != 0 {
		t.Errorf("expected no blocks without resolver, got %d", len(bs))
	}
}
