package tracker

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/dusthoff/hashpoint/internal/storage"
	"github.com/dusthoff/hashpoint/internal/winapi"
)

func TestTitleExcluded(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		title    string
		excludes []string
		want     bool
	}{
		{"empty excludes", "Anything", nil, false},
		{"empty title with excludes", "", []string{"x"}, false},
		{"case-insensitive substring match", "Microsoft Teams - Benachrichtigung", []string{"benachrichtigung"}, true},
		{"upper-case match", "INCOMING CALL", []string{"call"}, true},
		{"non-match", "Project Foo - Teams", []string{"benachrichtigung", "reminder"}, false},
		{"first phrase wins early", "Teams Reminder", []string{"reminder", "benachrichtigung"}, true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := titleExcluded(tc.title, tc.excludes)
			if got != tc.want {
				t.Errorf("titleExcluded(%q, %v) = %v, want %v", tc.title, tc.excludes, got, tc.want)
			}
		})
	}
}

func TestLowerCopy(t *testing.T) {
	t.Parallel()
	got := lowerCopy([]string{"  Benachrichtigung ", "", "Reminder", "  "})
	want := []string{"benachrichtigung", "reminder"}
	if len(got) != len(want) {
		t.Fatalf("lowerCopy length = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("lowerCopy[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if lowerCopy(nil) != nil {
		t.Errorf("lowerCopy(nil) should return nil")
	}
}

// fakeRepo is a minimal in-memory ProcessTrackRepository sufficient for the
// tickComm tests below. Only the methods tickComm exercises are implemented;
// the rest panics so an accidental call is loud.
type fakeRepo struct {
	mu       sync.Mutex
	nextID   int64
	tracks   map[int64]*storage.ProcessTrack
	openLog  []int64
	closeLog []int64
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{tracks: map[int64]*storage.ProcessTrack{}}
}

func (r *fakeRepo) Open(_ context.Context, p *storage.ProcessTrack) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	p.ID = r.nextID
	cp := *p
	r.tracks[p.ID] = &cp
	r.openLog = append(r.openLog, p.ID)
	return nil
}

func (r *fakeRepo) Close(_ context.Context, id int64, end time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t, ok := r.tracks[id]; ok {
		t.EndTime = &end
		r.closeLog = append(r.closeLog, id)
	}
	return nil
}

func (r *fakeRepo) MarkIdle(context.Context, int64, time.Time) error { panic("not used") }

func (r *fakeRepo) Touch(_ context.Context, id int64, ts time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t, ok := r.tracks[id]; ok && t.EndTime == nil {
		if t.LastSeen == nil || t.LastSeen.Before(ts) {
			cp := ts
			t.LastSeen = &cp
		}
	}
	return nil
}
func (r *fakeRepo) LastOpen(context.Context) (*storage.ProcessTrack, error) {
	panic("not used")
}
func (r *fakeRepo) ListOpen(context.Context) ([]storage.ProcessTrack, error) { return nil, nil }
func (r *fakeRepo) ListOpenCommunication(context.Context) ([]storage.ProcessTrack, error) {
	return nil, nil
}
func (r *fakeRepo) ListByDay(context.Context, time.Time) ([]storage.ProcessTrack, error) {
	panic("not used")
}
func (r *fakeRepo) ListBetween(context.Context, time.Time, time.Time) ([]storage.ProcessTrack, error) {
	panic("not used")
}
func (r *fakeRepo) LastEnd(context.Context) (time.Time, error)                { panic("not used") }
func (r *fakeRepo) Get(context.Context, int64) (*storage.ProcessTrack, error) { panic("not used") }

// stubCommSource feeds a scripted set of windows to each call.
type stubCommSource struct {
	mu       sync.Mutex
	scripted [][]winapi.WindowInfo
	calls    int
}

func (s *stubCommSource) push(w []winapi.WindowInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scripted = append(s.scripted, w)
}

func (s *stubCommSource) EnumVisibleWindows(_ []string) ([]winapi.WindowInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.calls >= len(s.scripted) {
		return nil, nil
	}
	out := s.scripted[s.calls]
	s.calls++
	return out, nil
}

// recordingObserver counts OnCommunicationChanged invocations and remembers
// the last session list so the test can assert on the comm-snapshot.
type recordingObserver struct {
	mu        sync.Mutex
	commCalls int
	lastSess  []CommSession
}

func (o *recordingObserver) OnFocusChanged(context.Context, string, string, time.Time) {}
func (o *recordingObserver) OnFocusCleared(context.Context, time.Time)                 {}
func (o *recordingObserver) OnCommunicationChanged(_ context.Context, sessions []CommSession, _ time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.commCalls++
	o.lastSess = append([]CommSession(nil), sessions...)
}

// TestTickComm_TitleExcludeFilters covers the central exclusion contract from
// spec §2.1a: a comm-process window whose title contains an exclude phrase
// must be filtered out (no comm-track), and a previously-open comm-track
// whose title transitions into the exclude bucket must be closed on the next
// tick — exactly the same code path that closes a window that disappeared.
func TestTickComm_TitleExcludeFilters(t *testing.T) {
	t.Parallel()
	repo := newFakeRepo()
	stub := &stubCommSource{}
	obs := &recordingObserver{}
	trk := New(Config{
		PollInterval:               time.Second,
		IdleThreshold:              5 * time.Minute,
		CommunicationNames:         []string{"teams.exe"},
		CommunicationTitleExcludes: []string{"Benachrichtigung"},
	}, repo, nil,
		WithCommunicationSource(stub),
		WithObserver(obs),
	)

	// Tick 1: a regular meeting window — should open a comm-track.
	stub.push([]winapi.WindowInfo{{HWND: 1, PID: 100, ProcessName: "teams.exe", Title: "Project Sync | Microsoft Teams"}})
	// Tick 2: the SAME window's title flips into the exclude bucket —
	// tickComm filters it out, the open track must close.
	stub.push([]winapi.WindowInfo{{HWND: 1, PID: 100, ProcessName: "teams.exe", Title: "Microsoft Teams Benachrichtigung"}})
	// Tick 3: the excluded title clears — a NEW comm-track should open.
	stub.push([]winapi.WindowInfo{{HWND: 1, PID: 100, ProcessName: "teams.exe", Title: "Project Sync | Microsoft Teams"}})

	ctx := context.Background()
	trk.tickComm(ctx)
	if len(repo.openLog) != 1 {
		t.Fatalf("after tick 1: opened=%d, want 1", len(repo.openLog))
	}
	if obs.commCalls != 1 || len(obs.lastSess) != 1 {
		t.Fatalf("after tick 1: observer calls=%d sessions=%v, want 1 call with 1 session",
			obs.commCalls, obs.lastSess)
	}

	trk.tickComm(ctx)
	if len(repo.closeLog) != 1 {
		t.Fatalf("after tick 2: closed=%d, want 1 (excluded title must close the open track)",
			len(repo.closeLog))
	}
	if obs.commCalls != 2 || len(obs.lastSess) != 0 {
		t.Fatalf("after tick 2: observer calls=%d sessions=%v, want 2 calls with empty session list",
			obs.commCalls, obs.lastSess)
	}

	trk.tickComm(ctx)
	if len(repo.openLog) != 2 {
		t.Fatalf("after tick 3: opened=%d, want 2 (title cleared → fresh comm-track)",
			len(repo.openLog))
	}
	if obs.commCalls != 3 || len(obs.lastSess) != 1 {
		t.Fatalf("after tick 3: observer calls=%d sessions=%v, want 3 calls with 1 session",
			obs.commCalls, obs.lastSess)
	}
}

// TestTickComm_HotReloadExcludes covers SetCommunicationTitleExcludes: the
// runtime can mutate the exclude list and the very next tick honours the
// new set. Used by App.SaveConfig to apply settings changes without a
// tracker restart.
func TestTickComm_HotReloadExcludes(t *testing.T) {
	t.Parallel()
	repo := newFakeRepo()
	stub := &stubCommSource{}
	obs := &recordingObserver{}
	trk := New(Config{
		PollInterval:       time.Second,
		IdleThreshold:      5 * time.Minute,
		CommunicationNames: []string{"teams.exe"},
	}, repo, nil,
		WithCommunicationSource(stub),
		WithObserver(obs),
	)

	stub.push([]winapi.WindowInfo{{HWND: 1, PID: 100, ProcessName: "teams.exe", Title: "Reminder Popup"}})
	stub.push([]winapi.WindowInfo{{HWND: 1, PID: 100, ProcessName: "teams.exe", Title: "Reminder Popup"}})

	ctx := context.Background()
	trk.tickComm(ctx)
	if len(repo.openLog) != 1 {
		t.Fatalf("tick 1 (no excludes): opened=%d, want 1", len(repo.openLog))
	}

	// User adds "Reminder" to the exclude list via the settings UI.
	trk.SetCommunicationTitleExcludes([]string{"Reminder"})

	trk.tickComm(ctx)
	if len(repo.closeLog) != 1 {
		t.Fatalf("tick 2 (after exclude added): closed=%d, want 1", len(repo.closeLog))
	}
}

// fixedClock returns the same instant on every call.
type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

// manualClock is a test clock the caller advances explicitly.
type manualClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *manualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *manualClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// stubFocusSource feeds a fixed foreground window and idle duration to tick().
type stubFocusSource struct {
	info winapi.FocusInfo
	idle time.Duration
}

func (s stubFocusSource) Foreground() (winapi.FocusInfo, error) { return s.info, nil }
func (s stubFocusSource) IdleDuration() (time.Duration, error)  { return s.idle, nil }

// TestRecoveredEnd covers the heartbeat-vs-fallback clamping in isolation:
// last_seen wins when present, otherwise start+idle_threshold; either result
// is clamped to now (the next-open chaining clamp is applied by recover, not
// here). This is the core of the issue-#21 data-loss fix (ADR 0001 §9).
func TestRecoveredEnd(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	idle := 5 * time.Minute
	at := func(d time.Duration) *time.Time { tt := base.Add(d); return &tt }

	cases := []struct {
		name     string
		start    time.Duration // offset from base
		lastSeen *time.Time
		want     time.Duration // expected end offset from base
	}{
		{"heartbeat wins over idle fallback", -60 * time.Minute, at(-10 * time.Minute), -10 * time.Minute},
		{"nil heartbeat falls back to start+idle", -60 * time.Minute, nil, -55 * time.Minute},
		{"idle fallback clamped to now", -2 * time.Minute, nil, 0},
		{"future heartbeat clamped to now", -10 * time.Minute, at(1 * time.Minute), 0},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			open := storage.ProcessTrack{StartTime: base.Add(tc.start), LastSeen: tc.lastSeen}
			got := recoveredEnd(open, idle, base)
			want := base.Add(tc.want)
			if got.Unix() != want.Unix() {
				t.Errorf("recoveredEnd = %s, want %s",
					got.Format(time.RFC3339), want.Format(time.RFC3339))
			}
		})
	}
}

// TestRecover_ClosesOpenTracksViaSQLite drives the full recover() path through
// a real in-memory database so the last_seen column round-trip, the focused
// next-start chaining clamp, the nil fallback, and the independent comm-track
// recovery are all exercised together.
func TestRecover_ClosesOpenTracksViaSQLite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := storage.OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	defer func() { _ = db.Close() }()
	repo := storage.NewProcessTrackRepo(db)

	base := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	idle := 5 * time.Minute

	// Focused A: started 60m ago, heartbeat 5m ago. Its heartbeat is later
	// than B's start, so the chaining clamp must pull A's end back to B.start.
	a := &storage.ProcessTrack{ProcessName: "firefox.exe", StartTime: base.Add(-60 * time.Minute)}
	mustOpen(t, ctx, repo, a)
	mustTouch(t, ctx, repo, a.ID, base.Add(-5*time.Minute))
	// Focused B: started 30m ago, no heartbeat → start+idle fallback.
	b := &storage.ProcessTrack{ProcessName: "code.exe", StartTime: base.Add(-30 * time.Minute)}
	mustOpen(t, ctx, repo, b)
	// Comm C: started 60m ago, heartbeat 15m ago. Comm tracks do not chain.
	c := &storage.ProcessTrack{ProcessName: "teams.exe", StartTime: base.Add(-60 * time.Minute), IsCommunication: true}
	mustOpen(t, ctx, repo, c)
	mustTouch(t, ctx, repo, c.ID, base.Add(-15*time.Minute))

	trk := New(Config{PollInterval: time.Second, IdleThreshold: idle}, repo, nil,
		WithClock(fixedClock{now: base}))
	if err := trk.recover(ctx); err != nil {
		t.Fatalf("recover: %v", err)
	}

	assertEnd(t, ctx, repo, "A (heartbeat clamped to B.start)", a.ID, base.Add(-30*time.Minute))
	assertEnd(t, ctx, repo, "B (idle fallback)", b.ID, base.Add(-25*time.Minute))
	assertEnd(t, ctx, repo, "C (comm heartbeat)", c.ID, base.Add(-15*time.Minute))
}

// TestTick_WritesHeartbeat verifies the write side: an active poll tick on an
// unchanged foreground window advances last_seen on the open track, so a crash
// mid-session recovers to the last tick rather than start+idle_threshold.
func TestTick_WritesHeartbeat(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := storage.OpenInMemory(ctx)
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	defer func() { _ = db.Close() }()
	repo := storage.NewProcessTrackRepo(db)

	base := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	clk := &manualClock{now: base}
	src := stubFocusSource{info: winapi.FocusInfo{HWND: 1, PID: 10, ProcessName: "firefox.exe", Title: "Hashpoint"}}

	trk := New(Config{PollInterval: time.Second, IdleThreshold: 5 * time.Minute}, repo, nil,
		WithFocusSource(src), WithClock(clk))

	// Tick 1 opens the focused track at base (no heartbeat yet on open).
	trk.tick(ctx)
	// Tick 2, 30s later: same focus → heartbeat advances to base+30s.
	clk.advance(30 * time.Second)
	trk.tick(ctx)

	open, err := repo.LastOpen(ctx)
	if err != nil {
		t.Fatalf("last open: %v", err)
	}
	if open == nil {
		t.Fatal("expected an open focused track after two active ticks")
	}
	if open.LastSeen == nil {
		t.Fatal("last_seen not written by an active tick")
	}
	if open.LastSeen.Unix() != base.Add(30*time.Second).Unix() {
		t.Errorf("last_seen = %s, want %s",
			open.LastSeen.Format(time.RFC3339), base.Add(30*time.Second).Format(time.RFC3339))
	}
}

func mustOpen(t *testing.T, ctx context.Context, repo storage.ProcessTrackRepository, p *storage.ProcessTrack) {
	t.Helper()
	if err := repo.Open(ctx, p); err != nil {
		t.Fatalf("open %s: %v", p.ProcessName, err)
	}
}

func mustTouch(t *testing.T, ctx context.Context, repo storage.ProcessTrackRepository, id int64, ts time.Time) {
	t.Helper()
	if err := repo.Touch(ctx, id, ts); err != nil {
		t.Fatalf("touch %d: %v", id, err)
	}
}

func assertEnd(t *testing.T, ctx context.Context, repo storage.ProcessTrackRepository, name string, id int64, want time.Time) {
	t.Helper()
	got, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("%s: get: %v", name, err)
	}
	if got == nil || got.EndTime == nil {
		t.Fatalf("%s: track still open after recover", name)
	}
	if got.EndTime.Unix() != want.Unix() {
		t.Errorf("%s: end = %s, want %s",
			name, got.EndTime.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}
