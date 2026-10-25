package main

import (
	"bytes"
	"strings"
	"testing"
)

func hookRun(t *testing.T, s *Store, surface, payload string) (woken int, out string) {
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
	if got, _ := s.Get("a"); got.State != StateConfirmed || out == "" {
		t.Fatalf("state=%s out=%q", got.State, out)
	}
}

func TestSessionStartOtherSourceDoesNotConfirm(t *testing.T) {
	s := newTestStore(t)
	s.Enqueue("a", "/compact")
	s.Claim("a")
	_, out := hookRun(t, s, "a", `{"hook_event_name":"SessionStart","source":"startup"}`)
	if got, _ := s.Get("a"); got.State != StateInFlight || out != "" {
		t.Fatalf("state=%s out=%q", got.State, out)
	}
}
