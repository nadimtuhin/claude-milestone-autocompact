package hook

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/nadimtuhin/claude-milestone-autocompact/internal/pending"
)

type hookInput struct {
	Event      string `json:"hook_event_name"`
	Source     string `json:"source"`
	StopActive bool   `json:"stop_hook_active"`
}

const resumeNotice = "Context was just compacted at a milestone. Resume the task from the preserved summary and your plan or notepad."

func HandleHook(in io.Reader, out io.Writer, store *pending.Store, surface string, wake func(surface string) error) error {
	var input hookInput
	if err := json.NewDecoder(in).Decode(&input); err != nil || surface == "" {
		return nil
	}
	switch {
	case input.Event == "Stop" && !input.StopActive:
		if _, ok := store.Read(surface, pending.StateQueued); ok {
			if err := store.MarkStopped(surface); err != nil {
				return err
			}
			return wake(surface)
		}
	case input.Event == "PostCompact", input.Event == "SessionStart" && input.Source == "compact":
		if store.Confirm(surface) {
			fmt.Fprintln(out, resumeNotice)
		}
	}
	return nil
}
