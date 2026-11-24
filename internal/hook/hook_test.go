package hook

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/nadimtuhin/claude-milestone-autocompact/internal/pending"
)

func newTestStore(t *testing.T) *pending.Store {
	return &pending.Store{Dir: t.TempDir(), Now: func() time.Time { return time.Unix(1000, 0) }}
}

func hookRun(t *testing.T, s *pending.Store, surface, payload string) (woken int, out string) {
	var buf bytes.Buffer
	err := HandleHook(strings.NewReader(payload), &buf, s, surface, func(string) error { woken++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	return woken, buf.String()
}

func TestStopWakesWorkerOnlyWhenQueued(t *testing.T) {
	s := newTestStore(t)
	if n, _ := hookRun(t, s, "a", `{"hook_event_name":"Stop"}`); n != 0 {
		t.Fatal("woke with nothing queued")
	}
	s.Enqueue("a", "/compact")
	if n, _ := hookRun(t, s, "a", `{"hook_event_name":"Stop"}`); n != 1 {
		t.Fatal("did not wake for queued request")
	}
}

func TestSessionStartCompactConfirmsInFlight(t *testing.T) {
	s := newTestStore(t)
	s.Enqueue("a", "/compact")
	s.Claim("a")
	_, out := hookRun(t, s, "a", `{"hook_event_name":"SessionStart","source":"compact"}`)
	if got, _ := s.Get("a"); got.State != pending.StateConfirmed || out == "" {
		t.Fatalf("state=%s out=%q", got.State, out)
	}
}

func TestSessionStartOtherSourceDoesNotConfirm(t *testing.T) {
	s := newTestStore(t)
	s.Enqueue("a", "/compact")
	s.Claim("a")
	_, out := hookRun(t, s, "a", `{"hook_event_name":"SessionStart","source":"startup"}`)
	if got, _ := s.Get("a"); got.State != pending.StateInFlight || out != "" {
		t.Fatalf("state=%s out=%q", got.State, out)
	}
}

func TestStopEventQueuedWritesMarker(t *testing.T) {
	s := newTestStore(t)
	s.Enqueue("a", "/compact")
	if _, ok := s.StoppedAt("a"); ok {
		t.Fatal("stopped marker should not exist before Stop event")
	}
	hookRun(t, s, "a", `{"hook_event_name":"Stop"}`)
	got, ok := s.StoppedAt("a")
	if !ok || !got.Equal(time.Unix(1000, 0)) {
		t.Fatalf("unexpected marker: got=%v ok=%v", got, ok)
	}
}

func TestHookScenario3StopWithNoQueuedRequestWritesNoMarkerAndWakesNoWorker(t *testing.T) {
	s := newTestStore(t)
	woken, _ := hookRun(t, s, "a", `{"hook_event_name":"Stop"}`)
	if woken != 0 {
		t.Fatalf("woken=%d, want 0", woken)
	}
	if _, ok := s.StoppedAt("a"); ok {
		t.Fatal("stopped marker should not be written when no request is queued")
	}
}

func TestSecondStopEventWhenQueuedRefreshesMarker(t *testing.T) {
	clock := time.Unix(1000, 0)
	s := &pending.Store{Dir: t.TempDir(), Now: func() time.Time { return clock }}
	s.Enqueue("a", "/compact")
	hookRun(t, s, "a", `{"hook_event_name":"Stop"}`)
	got, ok := s.StoppedAt("a")
	if !ok || !got.Equal(time.Unix(1000, 0)) {
		t.Fatalf("first marker=%v, want %v", got, clock)
	}
	clock = time.Unix(1100, 0)
	hookRun(t, s, "a", `{"hook_event_name":"Stop"}`)
	got, ok = s.StoppedAt("a")
	if !ok || !got.Equal(time.Unix(1100, 0)) {
		t.Fatalf("refreshed marker=%v, want %v", got, clock)
	}
}

// PostCompact stdout never reaches Claude (the hooks spec routes it nowhere), so the resume
// notice must come out of the SessionStart(compact) hook even when PostCompact confirmed first.
func TestResumeNoticeReachesSessionStartAfterPostCompactConfirms(t *testing.T) {
	s := newTestStore(t)
	s.Enqueue("a", "/compact")
	s.Claim("a")
	hookRun(t, s, "a", `{"hook_event_name":"PostCompact","trigger":"manual"}`)
	if got, _ := s.Get("a"); got.State != pending.StateConfirmed {
		t.Fatalf("PostCompact should confirm, state=%s", got.State)
	}
	_, out := hookRun(t, s, "a", `{"hook_event_name":"SessionStart","source":"compact"}`)
	if !strings.Contains(out, "compacted at a milestone") {
		t.Fatalf("SessionStart(compact) printed %q, want the resume notice", out)
	}
}

func TestResumeNoticeIsPrintedOncePerRequest(t *testing.T) {
	s := newTestStore(t)
	s.Enqueue("a", "/compact")
	s.Claim("a")
	hookRun(t, s, "a", `{"hook_event_name":"SessionStart","source":"compact"}`)
	_, out := hookRun(t, s, "a", `{"hook_event_name":"SessionStart","source":"compact"}`)
	if out != "" {
		t.Fatalf("a later, unrelated compaction printed %q", out)
	}
}

func TestPostCompactPrintsNothing(t *testing.T) {
	s := newTestStore(t)
	s.Enqueue("a", "/compact")
	s.Claim("a")
	if _, out := hookRun(t, s, "a", `{"hook_event_name":"PostCompact","trigger":"manual"}`); out != "" {
		t.Fatalf("PostCompact stdout is not shown to Claude, printed %q", out)
	}
}

// A turn continued by another Stop hook (e.g. /goal) ends with stop_hook_active=true.
// That final Stop is the real end of the turn and must refresh the marker.
func TestStopHookActiveStillMarksTurnEnd(t *testing.T) {
	clock := time.Unix(1000, 0)
	s := &pending.Store{Dir: t.TempDir(), Now: func() time.Time { return clock }}
	s.Enqueue("a", "/compact")
	hookRun(t, s, "a", `{"hook_event_name":"Stop"}`)
	clock = time.Unix(1300, 0)
	n, _ := hookRun(t, s, "a", `{"hook_event_name":"Stop","stop_hook_active":true}`)
	got, ok := s.StoppedAt("a")
	if n != 1 || !ok || !got.Equal(clock) {
		t.Fatalf("woken=%d marker=%v ok=%v, want woken=1 marker=%v", n, got, ok, clock)
	}
}

func TestInvalidSurfaceIsIgnored(t *testing.T) {
	s := newTestStore(t)
	if n, _ := hookRun(t, s, "../x", `{"hook_event_name":"Stop"}`); n != 0 {
		t.Fatal("woke a worker for a path-like surface")
	}
}
