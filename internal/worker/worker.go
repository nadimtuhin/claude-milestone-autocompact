package worker

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/nadimtuhin/claude-milestone-autocompact/internal/pending"
)

type Pane interface {
	ReadScreen(surface string) (string, error)
	Send(surface, text string) error
}

type CmuxPane struct{}

func (CmuxPane) ReadScreen(surface string) (string, error) {
	out, err := exec.Command("cmux", "read-screen", "--surface", surface, "--lines", "40").Output()
	return string(out), err
}

// why: a literal \n makes cmux press Enter; the command never contains backslashes (see oneLine).
func (CmuxPane) Send(surface, text string) error {
	out, err := exec.Command("cmux", "send", "--surface", surface, "--", text+`\n`).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// why: newer Claude Code drops "esc to interrupt"; the spinner line "✻ Verb… (16s · …" is the busy signal.
var spinnerLine = regexp.MustCompile(`…\s*\((\d+h )?(\d+m )?\d+s`)
var emptyPrompt = regexp.MustCompile(`^❯\s*$`)

func IsIdle(screen string) bool {
	if strings.Contains(screen, "esc to interrupt") || spinnerLine.MatchString(screen) {
		return false
	}
	lines := strings.Split(screen, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(lines[i], "❯") {
			return emptyPrompt.MatchString(lines[i])
		}
	}
	return false
}

type Worker struct {
	Store          *pending.Store
	Pane           Pane
	Sleep          func(time.Duration)
	Now            func() time.Time
	SendDeadline   time.Duration
	ConfirmTimeout time.Duration
	Poll           time.Duration
}

const outerCap = 30 * time.Minute

func NewWorker() *Worker {
	return &Worker{Store: pending.DefaultStore(), Pane: CmuxPane{}, Sleep: time.Sleep, Now: time.Now,
		SendDeadline: 120 * time.Second, ConfirmTimeout: 10 * time.Minute, Poll: 500 * time.Millisecond}
}

func (w *Worker) Run(surface string) {
	if !w.Store.AcquireWorker(surface) {
		return
	}
	defer w.Store.ReleaseWorker(surface)
	lastErr := "prompt never became idle"
	outerDeadline := w.Now().Add(outerCap)
	for w.Now().Before(outerDeadline) {
		req, ok := w.Store.Read(surface, pending.StateQueued)
		if !ok {
			return
		}
		stoppedAt, ok := w.Store.StoppedAt(surface)
		if !ok || !stoppedAt.After(time.Unix(req.RequestedAt, 0)) {
			w.Sleep(w.Poll)
			continue
		}
		sendDeadline := stoppedAt.Add(w.SendDeadline)
		if !w.Now().Before(sendDeadline) {
			w.Store.Fail(surface, "not sent before deadline: "+lastErr)
			return
		}
		if !w.stableIdle(surface) {
			w.Sleep(w.Poll)
			continue
		}
		claimed, ok := w.Store.Claim(surface)
		if !ok {
			return
		}
		err := w.Pane.Send(surface, claimed.Command)
		if err == nil {
			w.awaitConfirm(surface)
			return
		}
		lastErr = err.Error()
		w.Store.Release(claimed)
		w.Sleep(w.Poll)
	}
	w.Store.Fail(surface, "not sent before deadline: turn outlasted 30m outer cap")
}

func (w *Worker) stableIdle(surface string) bool {
	first, err := w.Pane.ReadScreen(surface)
	if err != nil || !IsIdle(first) {
		return false
	}
	w.Sleep(w.Poll)
	second, err := w.Pane.ReadScreen(surface)
	return err == nil && IsIdle(second)
}

func (w *Worker) awaitConfirm(surface string) {
	deadline := w.Now().Add(w.ConfirmTimeout)
	for w.Now().Before(deadline) {
		if _, ok := w.Store.Read(surface, pending.StateInFlight); !ok {
			return
		}
		w.Sleep(w.Poll)
	}
	w.Store.Fail(surface, "sent, but no post-compaction signal arrived")
}

func Spawn(surface string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "worker", surface)
	cmd.SysProcAttr = detachedAttr()
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
