package main

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

const maxCommandLen = 800

func BuildCommand(milestone string, keep, drop []string) (string, error) {
	milestone = oneLine(milestone)
	keep, drop = cleanItems(keep), cleanItems(drop)
	switch {
	case milestone == "":
		return "", errors.New("milestone must not be empty")
	case len(keep) == 0:
		return "", errors.New("keep must list at least one thing to preserve")
	case len(drop) == 0:
		return "", errors.New("drop must list at least one thing to discard")
	}
	cmd := fmt.Sprintf("/compact Milestone: %s. KEEP: %s. DROP: %s.", milestone, strings.Join(keep, "; "), strings.Join(drop, "; "))
	if len(cmd) > maxCommandLen {
		return "", fmt.Errorf("command is %d characters, limit is %d; shorten keep/drop", len(cmd), maxCommandLen)
	}
	return cmd, nil
}

// why: cmux send turns a literal \n into Enter, so backslashes are removed along with control characters.
func oneLine(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '\\' || unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

func cleanItems(items []string) []string {
	var out []string
	for _, item := range items {
		if item = oneLine(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}
