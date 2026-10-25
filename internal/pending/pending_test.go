package pending

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	return &Store{Dir: t.TempDir(), Now: func() time.Time { return time.Unix(1000, 0) }}
}

func TestEnqueueWritesRequestForSurface(t *testing.T) {
	s := newTestStore(t)
	req, err := s.Enqueue("surf-1", "/compact x")
	if err != nil {
		t.Fatal(err)
	}
	got, ok := s.Get("surf-1")
	if !ok || got.Surface != "surf-1" || got.Command != "/compact x" || got.State != StateQueued || got.ID != req.ID {
		t.Fatalf("unexpected request %+v ok=%v", got, ok)
	}
}

func TestEnqueueRejectsWhileActive(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Enqueue("a", "/compact 1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enqueue("a", "/compact 2"); err == nil {
		t.Fatal("second enqueue while queued must fail")
	}
	if _, ok := s.Claim("a"); !ok {
		t.Fatal("claim failed")
	}
	if _, err := s.Enqueue("a", "/compact 3"); err == nil {
		t.Fatal("enqueue while in_flight must fail")
	}
	if _, err := s.Enqueue("b", "/compact 4"); err != nil {
		t.Fatalf("other surface must be independent: %v", err)
	}
}

func TestConcurrentClaimOnlyOneWins(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Enqueue("a", "/compact"); err != nil {
		t.Fatal(err)
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok := s.Claim("a"); ok {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("want exactly 1 claim, got %d", wins.Load())
	}
}

func TestReleaseReturnsToQueued(t *testing.T) {
	s := newTestStore(t)
	s.Enqueue("a", "/compact")
	req, _ := s.Claim("a")
	if err := s.Release(req); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get("a"); got.State != StateQueued {
		t.Fatalf("state %s", got.State)
	}
	if _, ok := s.Claim("a"); !ok {
		t.Fatal("re-claim after release failed")
	}
}

func TestConfirmOnlyFromInFlight(t *testing.T) {
	s := newTestStore(t)
	s.Enqueue("a", "/compact")
	if s.Confirm("a") {
		t.Fatal("confirm of a merely queued request must not count")
	}
	s.Claim("a")
	if !s.Confirm("a") {
		t.Fatal("confirm of in_flight failed")
	}
	if got, _ := s.Get("a"); got.State != StateConfirmed {
		t.Fatalf("state %s", got.State)
	}
	if _, err := s.Enqueue("a", "/compact again"); err != nil {
		t.Fatalf("enqueue after confirmed must work: %v", err)
	}
}

func TestFailRecordsReason(t *testing.T) {
	s := newTestStore(t)
	s.Enqueue("a", "/compact")
	if err := s.Fail("a", "draft present"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get("a")
	if got.State != StateFailed || got.Reason != "draft present" {
		t.Fatalf("%+v", got)
	}
}

func TestEnqueueOverwritesStaleQueuedRequest(t *testing.T) {
	s := newTestStore(t)
	oldReq, err := s.Enqueue("a", "/compact 1")
	if err != nil {
		t.Fatal(err)
	}
	s.Now = func() time.Time { return time.Unix(1000, 0).Add(11 * time.Minute) }
	newReq, err := s.Enqueue("a", "/compact 2")
	if err != nil {
		t.Fatalf("enqueue over stale queued request should succeed: %v", err)
	}
	if newReq.ID == oldReq.ID {
		t.Fatal("expected new request ID")
	}
	got, ok := s.Get("a")
	if !ok || got.ID != newReq.ID || got.Command != "/compact 2" {
		t.Fatalf("unexpected request in store: %+v", got)
	}
}

func TestEnqueueOverwritesStaleInFlightRequest(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Enqueue("a", "/compact 1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Claim("a"); !ok {
		t.Fatal("claim failed")
	}
	s.Now = func() time.Time { return time.Unix(1000, 0).Add(11 * time.Minute) }
	newReq, err := s.Enqueue("a", "/compact 2")
	if err != nil {
		t.Fatalf("enqueue over stale in_flight request should succeed: %v", err)
	}
	if _, ok := s.Read("a", StateInFlight); ok {
		t.Fatal("stale in_flight file still exists")
	}
	got, ok := s.Get("a")
	if !ok || got.ID != newReq.ID || got.Command != "/compact 2" || got.State != StateQueued {
		t.Fatalf("unexpected request in store: %+v", got)
	}
}

func TestEnqueueRejectsFreshQueuedRequest(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Enqueue("a", "/compact 1"); err != nil {
		t.Fatal(err)
	}
	s.Now = func() time.Time { return time.Unix(1000, 0).Add(9 * time.Minute) }
	if _, err := s.Enqueue("a", "/compact 2"); err == nil {
		t.Fatal("enqueue over fresh queued request must fail")
	}
}

func TestEnqueueRejectsFreshInFlightRequest(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Enqueue("a", "/compact 1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Claim("a"); !ok {
		t.Fatal("claim failed")
	}
	s.Now = func() time.Time { return time.Unix(1000, 0).Add(9 * time.Minute) }
	if _, err := s.Enqueue("a", "/compact 2"); err == nil {
		t.Fatal("enqueue over fresh in_flight request must fail")
	}
}

func TestMarkStoppedAndStoppedAt(t *testing.T) {
	s := newTestStore(t)
	if _, ok := s.StoppedAt("a"); ok {
		t.Fatal("expected missing stopped marker to return false")
	}
	if err := s.MarkStopped("a"); err != nil {
		t.Fatal(err)
	}
	got, ok := s.StoppedAt("a")
	if !ok || !got.Equal(time.Unix(1000, 0)) {
		t.Fatalf("unexpected stopped at: got=%v ok=%v", got, ok)
	}
}

func TestStoppedAtCorruptReturnsFalse(t *testing.T) {
	s := newTestStore(t)
	if err := os.WriteFile(filepath.Join(s.Dir, "a.stopped"), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.StoppedAt("a"); ok {
		t.Fatal("expected corrupted marker to return false")
	}
}
