package tracker

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/onesi/hashpoint/internal/storage"
	"github.com/onesi/hashpoint/internal/winapi"
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
func (r *fakeRepo) LastEnd(context.Context) (time.Time, error)                  { panic("not used") }
func (r *fakeRepo) Get(context.Context, int64) (*storage.ProcessTrack, error)   { panic("not used") }

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
func (o *recordingObserver) OnFocusCleared(context.Context, time.Time)                  {}
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
