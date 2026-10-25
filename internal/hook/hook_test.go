package hook

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/nadimtuhin/claude-compact-milestone/internal/pending"
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

func TestStopHookActiveIsIgnored(t *testing.T) {
	s := newTestStore(t)
	s.Enqueue("a", "/compact")
	if n, _ := hookRun(t, s, "a", `{"hook_event_name":"Stop","stop_hook_active":true}`); n != 0 {
		t.Fatal("must ignore stop_hook_active")
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
