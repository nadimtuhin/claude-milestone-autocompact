package hook

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/nadimtuhin/claude-milestone-autocompact/internal/pending"
)

type hookInput struct {
	Event  string `json:"hook_event_name"`
	Source string `json:"source"`
}

const resumeNotice = "Context was just compacted at a milestone. Resume the task from the preserved summary and your plan or notepad."

func HandleHook(in io.Reader, out io.Writer, store *pending.Store, surface string, wake func(surface string) error) error {
	var input hookInput
	if err := json.NewDecoder(in).Decode(&input); err != nil || !pending.ValidSurface(surface) {
		return nil
	}
	switch {
	// why: stop_hook_active only means another Stop hook continued this turn. This hook never
	// blocks, so it cannot loop, and the Stop that ends a continued turn is the real turn end.
	case input.Event == "Stop":
		if _, ok := store.Read(surface, pending.StateQueued); ok {
			if err := store.MarkStopped(surface); err != nil {
				return err
			}
			return wake(surface)
		}
	case input.Event == "PostCompact":
		// why: Claude Code does not add PostCompact stdout to context, so only confirm here.
		store.Confirm(surface)
	case input.Event == "SessionStart" && input.Source == "compact":
		store.Confirm(surface)
		if store.Announce(surface) {
			fmt.Fprintln(out, resumeNotice)
		}
	}
	return nil
}
