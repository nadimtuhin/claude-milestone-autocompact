package worker

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nadimtuhin/claude-milestone-autocompact/internal/pending"
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

const spinnerScreen = "⏺ Calling claude-milestone-autocompact…\n✻ Caramelizing… (16s · ↓ 306 tokens)\n────\n❯ \n────\n"

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

func TestIsIdleFalseWhileSpinnerPastOneMinute(t *testing.T) {
	for _, elapsed := range []string{"1m 23s", "12m 5s", "1h 2m 3s"} {
		screen := "✻ Cooking… (" + elapsed + " · ↓ 9k tokens)\n────\n❯ \n────\n"
		if IsIdle(screen) {
			t.Errorf("spinner at %s must count as busy", elapsed)
		}
	}
}

func TestFailureReasonShowsWhatThePaneLooked(t *testing.T) {
	pane := &fakePane{screens: []string{"old\n✻ Thinking… (esc to interrupt)\n────\n❯ half typed\n────\n  status"}}
	w, clock := testWorker(t, pane)
	w.Store.Enqueue("s", "/compact x")
	*clock = clock.Add(time.Second)
	w.Store.MarkStopped("s")
	w.Run("s")
	got, _ := w.Store.Get("s")
	if !strings.Contains(got.Reason, "half typed") {
		t.Fatalf("reason should carry the last screen tail, got %q", got.Reason)
	}
}

func TestIsIdleAcceptsNonBreakingSpaceAfterPrompt(t *testing.T) {
	if !IsIdle("────\n❯ \n────\n  status") {
		t.Fatal("Claude Code draws the empty prompt as ❯ plus a non-breaking space")
	}
}

func TestIsIdleIgnoresBusyTextInScrollback(t *testing.T) {
	scrollback := "⏺ Bash(grep -c 'esc to interrupt')\n  Caramelizing… (16s · ↓ 3 tokens)\n" +
		"line\nline\nline\nline\nline\n✻ Worked for 34s\n\n────\n❯ \n────\n  status\n  ⏵⏵ bypass · ← 1 agent\n"
	if !IsIdle(scrollback) {
		t.Fatal("busy phrases quoted in scrollback far above the prompt must not block idle")
	}
}

func TestIsIdleFalseWhenLastPromptIsDialog(t *testing.T) {
	screen := "Do you trust this folder?\n❯ 1. Yes, I trust this folder\n  2. No\n"
	if IsIdle(screen) {
		t.Fatal("last prompt is dialog option, must not be idle")
	}
}

func TestIsIdleFalseWhenLastPromptIsEchoedCommand(t *testing.T) {
	screen := "❯ /compact Milestone: x.\nrunning...\n"
	if IsIdle(screen) {
		t.Fatal("last prompt is echoed command, must not be idle")
	}
}

func TestIsIdleFalseWhenLastPromptIsDraft(t *testing.T) {
	screen := "────\n❯ half typed draft\n────\n"
	if IsIdle(screen) {
		t.Fatal("last prompt is draft, must not be idle")
	}
}

func TestIsIdleTrueWhenEchoedPromptFollowedByEmptyPrompt(t *testing.T) {
	screen := "❯ do the thing\noutput\n❯ \n"
	if !IsIdle(screen) {
		t.Fatal("echoed prompt followed by empty prompt must be idle")
	}
}

func TestIsIdleFalseWhenNoPromptLine(t *testing.T) {
	screen := "output line 1\noutput line 2\n"
	if IsIdle(screen) {
		t.Fatal("no prompt line must not be idle")
	}
}

func TestWorkerWaitsForIdleThenSendsOnce(t *testing.T) {
	pane := &fakePane{screens: []string{busyScreen, busyScreen, idleScreen}}
	w, clock := testWorker(t, pane)
	w.Store.Enqueue("s", "/compact x")
	*clock = clock.Add(time.Second)
	w.Store.MarkStopped("s")
	w.Sleep = func(d time.Duration) {
		*clock = clock.Add(d)
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
	w, clock := testWorker(t, pane)
	w.Store.Enqueue("s", "/compact x")
	*clock = clock.Add(time.Second)
	w.Store.MarkStopped("s")
	w.Run("s")
	got, _ := w.Store.Get("s")
	if len(pane.sent) != 0 || got.State != pending.StateFailed {
		t.Fatalf("sent=%v state=%s", pane.sent, got.State)
	}
}

func TestWorkerDraftRefusalRetriesThenFails(t *testing.T) {
	refuse := errors.New("draft present")
	pane := &fakePane{screens: []string{idleScreen}, sendErrs: []error{refuse, refuse, refuse, refuse, refuse, refuse, refuse, refuse, refuse, refuse, refuse, refuse}}
	w, clock := testWorker(t, pane)
	w.Store.Enqueue("s", "/compact x")
	*clock = clock.Add(time.Second)
	w.Store.MarkStopped("s")
	w.Run("s")
	got, _ := w.Store.Get("s")
	if got.State != pending.StateFailed || got.Reason == "" || len(pane.sent) < 2 {
		t.Fatalf("state=%s reason=%q sends=%d", got.State, got.Reason, len(pane.sent))
	}
}

func TestWorkerSentButNeverConfirmedFails(t *testing.T) {
	pane := &fakePane{screens: []string{idleScreen}}
	w, clock := testWorker(t, pane)
	w.Store.Enqueue("s", "/compact x")
	*clock = clock.Add(time.Second)
	w.Store.MarkStopped("s")
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

func TestNewWorkerConfirmTimeout(t *testing.T) {
	w := NewWorker()
	if w.ConfirmTimeout != 10*time.Minute {
		t.Fatalf("ConfirmTimeout = %v, want %v", w.ConfirmTimeout, 10*time.Minute)
	}
}

func TestWorkerScenario1QueuedWithoutStopMarkerWaitsPastFiveMinutes(t *testing.T) {
	pane := &fakePane{screens: []string{busyScreen}}
	w, clock := testWorker(t, pane)
	w.Store.Enqueue("s", "/compact x")
	start := *clock
	type stopSignal struct{}
	w.Sleep = func(d time.Duration) {
		*clock = clock.Add(d)
		if clock.Sub(start) >= 5*time.Minute {
			panic(stopSignal{})
		}
	}
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected 5 minutes of fake clock to elapse")
		}
		if _, ok := r.(stopSignal); !ok {
			panic(r)
		}
		got, ok := w.Store.Get("s")
		if !ok || got.State != pending.StateQueued {
			t.Fatalf("want queued, got state=%s ok=%v", got.State, ok)
		}
		if len(pane.sent) != 0 {
			t.Fatalf("want nothing sent, got %v", pane.sent)
		}
	}()
	w.Run("s")
}

func TestWorkerScenario2SendsWhenMarkerNewerAndIdle(t *testing.T) {
	pane := &fakePane{screens: []string{idleScreen}}
	w, clock := testWorker(t, pane)
	w.Store.Enqueue("s", "/compact x")
	*clock = clock.Add(time.Second)
	w.Store.MarkStopped("s")
	w.Sleep = func(d time.Duration) {
		*clock = clock.Add(d)
		if len(pane.sent) == 1 {
			w.Store.Confirm("s")
		}
	}
	w.Run("s")
	if len(pane.sent) != 1 || pane.sent[0] != "/compact x" {
		t.Fatalf("sent %v", pane.sent)
	}
	got, _ := w.Store.Get("s")
	if got.State != pending.StateConfirmed {
		t.Fatalf("state %s", got.State)
	}
}

func TestWorkerScenario3FailsWhenPaneNeverIdleAfterMarker(t *testing.T) {
	pane := &fakePane{screens: []string{busyScreen}}
	w, clock := testWorker(t, pane)
	w.Store.Enqueue("s", "/compact x")
	*clock = clock.Add(time.Second)
	w.Store.MarkStopped("s")
	markedAt := *clock
	w.Run("s")
	got, ok := w.Store.Get("s")
	if !ok || got.State != pending.StateFailed || got.Reason == "" {
		t.Fatalf("state=%s reason=%q ok=%v", got.State, got.Reason, ok)
	}
	if len(pane.sent) != 0 {
		t.Fatalf("sent=%v", pane.sent)
	}
	if failedAfter := clock.Sub(markedAt); failedAfter >= outerCap {
		t.Fatalf("failed after %v, send deadline must trip well before the %v outer cap", failedAfter, outerCap)
	}
}

func TestWorkerScenario4MarkerOlderThanRequestedAtDoesNotSend(t *testing.T) {
	pane := &fakePane{screens: []string{idleScreen}}
	w, clock := testWorker(t, pane)
	w.Store.MarkStopped("s")
	*clock = clock.Add(time.Second)
	w.Store.Enqueue("s", "/compact x")
	w.Run("s")
	if len(pane.sent) != 0 {
		t.Fatalf("expected nothing sent, got %v", pane.sent)
	}
}

func TestWorkerOuterCapExpires(t *testing.T) {
	pane := &fakePane{screens: []string{busyScreen}}
	w, _ := testWorker(t, pane)
	w.Store.Enqueue("s", "/compact x")
	w.Run("s")
	got, ok := w.Store.Get("s")
	if !ok || got.State != pending.StateFailed || got.Reason != "not sent before deadline: turn outlasted 30m outer cap" {
		t.Fatalf("expected failed with outer cap, got %+v", got)
	}
}

func resumeWorker(t *testing.T, pane *fakePane, resume string, confirm bool) *Worker {
	w, clock := testWorker(t, pane)
	w.Store.EnqueueWithResume("s", "/compact x", resume)
	*clock = clock.Add(time.Second)
	w.Store.MarkStopped("s")
	w.Sleep = func(d time.Duration) {
		*clock = clock.Add(d)
		if len(pane.sent) == 1 {
			if confirm {
				w.Store.Confirm("s")
			} else {
				w.Store.Fail("s", "compaction did not run")
			}
		}
	}
	return w
}

func TestWorkerTypesResumeAfterConfirmation(t *testing.T) {
	pane := &fakePane{screens: []string{idleScreen}}
	resumeWorker(t, pane, "carry on with the plan", true).Run("s")
	if len(pane.sent) != 2 || pane.sent[1] != "carry on with the plan" {
		t.Fatalf("sent %v", pane.sent)
	}
}

func TestWorkerWithoutResumeSendsOnlyCompact(t *testing.T) {
	pane := &fakePane{screens: []string{idleScreen}}
	resumeWorker(t, pane, "", true).Run("s")
	if len(pane.sent) != 1 {
		t.Fatalf("sent %v", pane.sent)
	}
}

func TestWorkerSkipsResumeWhenCompactionNeverConfirmed(t *testing.T) {
	pane := &fakePane{screens: []string{idleScreen}}
	resumeWorker(t, pane, "carry on", false).Run("s")
	if len(pane.sent) != 1 {
		t.Fatalf("resume must not be sent without confirmation, sent %v", pane.sent)
	}
}

func TestWorkerResumeWaitsForIdle(t *testing.T) {
	pane := &fakePane{screens: []string{idleScreen, idleScreen, busyScreen, busyScreen, idleScreen}}
	resumeWorker(t, pane, "carry on", true).Run("s")
	if len(pane.sent) != 2 {
		t.Fatalf("sent %v", pane.sent)
	}
}

func TestWorkerScenario1SecondStopExtendsDeadlineAndSends(t *testing.T) {
	screens := make([]string, 126)
	for i := 0; i < 125; i++ {
		screens[i] = busyScreen
	}
	screens[125] = idleScreen
	pane := &fakePane{screens: screens}

	t0 := time.Unix(1000, 0)
	clock := t0.Add(-time.Second)
	store := &pending.Store{Dir: t.TempDir(), Now: func() time.Time { return clock }}
	store.Enqueue("s", "/compact x")

	clock = t0
	store.MarkStopped("s")

	w := &Worker{
		Store:          store,
		Pane:           pane,
		Now:            func() time.Time { return clock },
		SendDeadline:   120 * time.Second,
		ConfirmTimeout: 10 * time.Second,
		Poll:           time.Second,
	}
	w.Sleep = func(d time.Duration) {
		clock = clock.Add(d)
		if clock.Equal(t0.Add(100 * time.Second)) {
			store.MarkStopped("s")
		}
		if len(pane.sent) == 1 && clock.Sub(t0) >= 130*time.Second {
			store.Confirm("s")
		}
	}

	w.Run("s")

	if len(pane.sent) != 1 || pane.sent[0] != "/compact x" {
		t.Fatalf("sent=%v, want [/compact x]", pane.sent)
	}
	got, ok := store.Get("s")
	if !ok || got.State == pending.StateFailed {
		t.Fatalf("state=%s, want not failed", got.State)
	}
	if clock.Before(t0.Add(130 * time.Second)) {
		t.Fatalf("clock=%v, want passed %v", clock, t0.Add(130*time.Second))
	}
}

func TestWorkerScenario2SingleStopPastDeadlineFails(t *testing.T) {
	pane := &fakePane{screens: []string{busyScreen}}
	t0 := time.Unix(1000, 0)
	clock := t0.Add(-time.Second)
	store := &pending.Store{Dir: t.TempDir(), Now: func() time.Time { return clock }}
	store.Enqueue("s", "/compact x")

	clock = t0
	store.MarkStopped("s")

	w := &Worker{
		Store:          store,
		Pane:           pane,
		Now:            func() time.Time { return clock },
		SendDeadline:   120 * time.Second,
		ConfirmTimeout: 10 * time.Second,
		Poll:           time.Second,
		Sleep:          func(d time.Duration) { clock = clock.Add(d) },
	}

	w.Run("s")

	if len(pane.sent) != 0 {
		t.Fatalf("sent=%v, want nothing sent", pane.sent)
	}
	got, ok := store.Get("s")
	if !ok || got.State != pending.StateFailed || !strings.HasPrefix(got.Reason, "not sent before deadline: prompt never became idle") {
		t.Fatalf("state=%s reason=%q ok=%v", got.State, got.Reason, ok)
	}
	if clock.Sub(t0) != 120*time.Second {
		t.Fatalf("failed at %v, want %v", clock.Sub(t0), 120*time.Second)
	}
}

func TestWorkerScenario4OuterCapElapsedWithRepeatedStopsFails(t *testing.T) {
	pane := &fakePane{screens: []string{busyScreen}}
	t0 := time.Unix(1000, 0)
	clock := t0.Add(-time.Second)
	store := &pending.Store{Dir: t.TempDir(), Now: func() time.Time { return clock }}
	store.Enqueue("s", "/compact x")

	clock = t0
	store.MarkStopped("s")

	w := &Worker{
		Store:          store,
		Pane:           pane,
		Now:            func() time.Time { return clock },
		SendDeadline:   120 * time.Second,
		ConfirmTimeout: 10 * time.Second,
		Poll:           time.Second,
	}
	w.Sleep = func(d time.Duration) {
		clock = clock.Add(d)
		if clock.Sub(t0)%time.Minute == 0 {
			store.MarkStopped("s")
		}
	}

	w.Run("s")

	if len(pane.sent) != 0 {
		t.Fatalf("sent=%v, want nothing sent", pane.sent)
	}
	got, ok := store.Get("s")
	if !ok || got.State != pending.StateFailed || got.Reason != "not sent before deadline: turn outlasted 30m outer cap" {
		t.Fatalf("state=%s reason=%q ok=%v", got.State, got.Reason, ok)
	}
	if clock.Sub(t0) != 30*time.Minute {
		t.Fatalf("elapsed=%v, want %v", clock.Sub(t0), 30*time.Minute)
	}
}
