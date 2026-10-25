package worker

import (
	"errors"
	"testing"
	"time"

	"github.com/nadimtuhin/claude-compact-milestone/internal/pending"
)

const idleScreen = "────\n❯ \n────\n  status"
const busyScreen = "✻ Thinking… (esc to interrupt)\n❯ \n"

type fakePane struct {
	screens  []string
	sendErrs []error
	sent     []string
	reads    int
}

func (p *fakePane) ReadScreen(string) (string, error) {
	s := p.screens[min(p.reads, len(p.screens)-1)]
	p.reads++
	return s, nil
}

func (p *fakePane) Send(_, text string) error {
	p.sent = append(p.sent, text)
	if len(p.sent) <= len(p.sendErrs) {
		return p.sendErrs[len(p.sent)-1]
	}
	return nil
}

func testWorker(t *testing.T, pane *fakePane) (*Worker, *time.Time) {
	clock := time.Unix(1000, 0)
	store := &pending.Store{Dir: t.TempDir(), Now: func() time.Time { return clock }}
	w := &Worker{Store: store, Pane: pane, Now: func() time.Time { return clock },
		SendDeadline: 10 * time.Second, ConfirmTimeout: 10 * time.Second, Poll: time.Second}
	w.Sleep = func(d time.Duration) { clock = clock.Add(d) }
	return w, &clock
}

const spinnerScreen = "⏺ Calling claude-compact-milestone…\n✻ Caramelizing… (16s · ↓ 306 tokens)\n────\n❯ \n────\n"

func TestIsIdle(t *testing.T) {
	if !IsIdle(idleScreen) || IsIdle(busyScreen) || IsIdle("no prompt here") {
		t.Fatal("idle detection wrong")
	}
}

func TestIsIdleFalseWhileSpinnerRuns(t *testing.T) {
	if IsIdle(spinnerScreen) {
		t.Fatal("spinner line with elapsed seconds means the turn is still running")
	}
}

func TestWorkerWaitsForIdleThenSendsOnce(t *testing.T) {
	pane := &fakePane{screens: []string{busyScreen, busyScreen, idleScreen}}
	w, _ := testWorker(t, pane)
	w.Store.Enqueue("s", "/compact x")
	w.Sleep = func(d time.Duration) {
		if len(pane.sent) == 1 {
			w.Store.Confirm("s")
		}
	}
	w.Run("s")
	if len(pane.sent) != 1 || pane.sent[0] != "/compact x" {
		t.Fatalf("sent %v", pane.sent)
	}
	if got, _ := w.Store.Get("s"); got.State != pending.StateConfirmed {
		t.Fatalf("state %s", got.State)
	}
}

func TestWorkerNeverSendsWhileBusyThenFails(t *testing.T) {
	pane := &fakePane{screens: []string{busyScreen}}
	w, _ := testWorker(t, pane)
	w.Store.Enqueue("s", "/compact x")
	w.Run("s")
	got, _ := w.Store.Get("s")
	if len(pane.sent) != 0 || got.State != pending.StateFailed {
		t.Fatalf("sent=%v state=%s", pane.sent, got.State)
	}
}

func TestWorkerDraftRefusalRetriesThenFails(t *testing.T) {
	refuse := errors.New("draft present")
	pane := &fakePane{screens: []string{idleScreen}, sendErrs: []error{refuse, refuse, refuse, refuse, refuse, refuse, refuse, refuse, refuse, refuse, refuse, refuse}}
	w, _ := testWorker(t, pane)
	w.Store.Enqueue("s", "/compact x")
	w.Run("s")
	got, _ := w.Store.Get("s")
	if got.State != pending.StateFailed || got.Reason == "" || len(pane.sent) < 2 {
		t.Fatalf("state=%s reason=%q sends=%d", got.State, got.Reason, len(pane.sent))
	}
}

func TestWorkerSentButNeverConfirmedFails(t *testing.T) {
	pane := &fakePane{screens: []string{idleScreen}}
	w, _ := testWorker(t, pane)
	w.Store.Enqueue("s", "/compact x")
	w.Run("s")
	got, _ := w.Store.Get("s")
	if len(pane.sent) != 1 || got.State != pending.StateFailed {
		t.Fatalf("sent=%d state=%s", len(pane.sent), got.State)
	}
}

func TestSecondWorkerExitsWhileLockHeld(t *testing.T) {
	pane := &fakePane{screens: []string{idleScreen}}
	w, _ := testWorker(t, pane)
	w.Store.Enqueue("s", "/compact x")
	w.Store.AcquireWorker("s")
	w.Run("s")
	if len(pane.sent) != 0 {
		t.Fatal("second worker must not send")
	}
}
