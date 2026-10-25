package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
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
var spinnerLine = regexp.MustCompile(`…\s*\(\d+s`)

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
	Store          *Store
	Pane           Pane
	Sleep          func(time.Duration)
	Now            func() time.Time
	SendDeadline   time.Duration
	ConfirmTimeout time.Duration
	Poll           time.Duration
}

func NewWorker() *Worker {
	return &Worker{Store: DefaultStore(), Pane: CmuxPane{}, Sleep: time.Sleep, Now: time.Now,
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
		if _, ok := w.Store.read(surface, StateQueued); !ok {
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
		if _, ok := w.Store.read(surface, StateInFlight); !ok {
			return
		}
		w.Sleep(w.Poll)
	}
	w.Store.Fail(surface, "sent, but no post-compaction signal arrived")
}

func (s *Store) workerLock(surface string) string { return filepath.Join(s.Dir, surface+".worker") }

// invariant: one worker per surface; a lock older than staleWorkerLock belongs to a dead worker.
const staleWorkerLock = 6 * time.Minute

func (s *Store) AcquireWorker(surface string) bool {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return false
	}
	path := s.workerLock(surface)
	if info, err := os.Stat(path); err == nil && s.Now().Sub(info.ModTime()) > staleWorkerLock {
		os.Remove(path)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return false
	}
	return f.Close() == nil
}

func (s *Store) ReleaseWorker(surface string) { os.Remove(s.workerLock(surface)) }

func spawnWorker(surface string) error {
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
