package pending

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type State string

const (
	StateQueued    State = "queued"
	StateInFlight  State = "in_flight"
	StateConfirmed State = "confirmed"
	StateFailed    State = "failed"
)

const staleRequestAge = 10 * time.Minute

type Request struct {
	ID          string `json:"id"`
	Surface     string `json:"surface"`
	Command     string `json:"command"`
	Resume      string `json:"resume,omitempty"`
	State       State  `json:"state"`
	Reason      string `json:"reason,omitempty"`
	RequestedAt int64  `json:"requested_at"`
}

// invariant: a surface has at most one file per state suffix; Claim relies on rename failing for every caller but one.
type Store struct {
	Dir string
	Now func() time.Time
}

func DefaultStore() *Store {
	dir := os.Getenv("COMPACT_MILESTONE_DIR")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".claude", "compact-pending")
	}
	return &Store{Dir: dir, Now: time.Now}
}

func (s *Store) path(surface string, state State) string {
	return filepath.Join(s.Dir, surface+"."+string(state)+".json")
}

func (s *Store) write(req Request) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.Dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path(req.Surface, req.State))
}

func (s *Store) Read(surface string, state State) (Request, bool) {
	data, err := os.ReadFile(s.path(surface, state))
	if err != nil {
		return Request{}, false
	}
	var req Request
	if json.Unmarshal(data, &req) != nil {
		return Request{}, false
	}
	return req, true
}

func (s *Store) Get(surface string) (Request, bool) {
	for _, state := range []State{StateInFlight, StateQueued, StateFailed, StateConfirmed} {
		if req, ok := s.Read(surface, state); ok {
			return req, true
		}
	}
	return Request{}, false
}

func (s *Store) Enqueue(surface, command string) (Request, error) {
	return s.EnqueueWithResume(surface, command, "")
}

func (s *Store) EnqueueWithResume(surface, command, resume string) (Request, error) {
	if surface == "" {
		return Request{}, errors.New("CMUX_SURFACE_ID is not set; run Claude Code inside a cmux pane")
	}
	for _, active := range []State{StateQueued, StateInFlight} {
		if req, ok := s.Read(surface, active); ok {
			if s.Now().Sub(time.Unix(req.RequestedAt, 0)) > staleRequestAge {
				os.Remove(s.path(surface, active))
				continue
			}
			return Request{}, fmt.Errorf("a compaction request is already %s for this pane", active)
		}
	}
	id := make([]byte, 8)
	if _, err := rand.Read(id); err != nil {
		return Request{}, err
	}
	req := Request{ID: hex.EncodeToString(id), Surface: surface, Command: command, Resume: resume, State: StateQueued, RequestedAt: s.Now().Unix()}
	os.Remove(s.path(surface, StateFailed))
	os.Remove(s.path(surface, StateConfirmed))
	return req, s.write(req)
}

func (s *Store) Claim(surface string) (Request, bool) {
	if os.Rename(s.path(surface, StateQueued), s.path(surface, StateInFlight)) != nil {
		return Request{}, false
	}
	req, ok := s.Read(surface, StateInFlight)
	if !ok {
		return Request{}, false
	}
	req.State = StateInFlight
	return req, s.write(req) == nil
}

func (s *Store) Release(req Request) error {
	return s.move(req, StateInFlight, StateQueued, "")
}

func (s *Store) Confirm(surface string) bool {
	req, ok := s.Read(surface, StateInFlight)
	return ok && s.move(req, StateInFlight, StateConfirmed, "") == nil
}

func (s *Store) Fail(surface, reason string) error {
	for _, from := range []State{StateInFlight, StateQueued} {
		if req, ok := s.Read(surface, from); ok {
			return s.move(req, from, StateFailed, reason)
		}
	}
	return errors.New("no active request to fail")
}

func (s *Store) move(req Request, from, to State, reason string) error {
	req.State, req.Reason = to, reason
	if err := s.write(req); err != nil {
		return err
	}
	return os.Remove(s.path(req.Surface, from))
}

func (s *Store) workerLock(surface string) string { return filepath.Join(s.Dir, surface+".worker") }

// invariant: one worker per surface; a lock older than staleWorkerLock belongs to a dead worker.
const staleWorkerLock = 45 * time.Minute

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

func (s *Store) stoppedPath(surface string) string {
	return filepath.Join(s.Dir, surface+".stopped")
}

func (s *Store) MarkStopped(surface string) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	return os.WriteFile(s.stoppedPath(surface), []byte(now.Format(time.RFC3339Nano)), 0o600)
}

func (s *Store) StoppedAt(surface string) (time.Time, bool) {
	data, err := os.ReadFile(s.stoppedPath(surface))
	if err != nil {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(data)))
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}
