package worker

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/nadimtuhin/claude-compact-milestone/internal/pending"
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

func IsIdle(screen string) bool {
	if strings.Contains(screen, "esc to interrupt") || spinnerLine.MatchString(screen) {
		return false
	}
	for _, line := range strings.Split(screen, "\n") {
		if strings.HasPrefix(line, "❯") {
			return true
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

func NewWorker() *Worker {
	return &Worker{Store: pending.DefaultStore(), Pane: CmuxPane{}, Sleep: time.Sleep, Now: time.Now,
		SendDeadline: 120 * time.Second, ConfirmTimeout: 180 * time.Second, Poll: 500 * time.Millisecond}
}

func (w *Worker) Run(surface string) {
	if !w.Store.AcquireWorker(surface) {
		return
	}
	defer w.Store.ReleaseWorker(surface)
	lastErr := "prompt never became idle"
	deadline := w.Now().Add(w.SendDeadline)
	for w.Now().Before(deadline) {
		if _, ok := w.Store.Read(surface, pending.StateQueued); !ok {
			return
		}
		if !w.stableIdle(surface) {
			w.Sleep(w.Poll)
			continue
		}
		req, ok := w.Store.Claim(surface)
		if !ok {
			return
		}
		err := w.Pane.Send(surface, req.Command)
		if err == nil {
			w.awaitConfirm(surface)
			return
		}
		lastErr = err.Error()
		w.Store.Release(req)
		w.Sleep(w.Poll)
	}
	w.Store.Fail(surface, "not sent before deadline: "+lastErr)
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
