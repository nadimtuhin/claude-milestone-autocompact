package command

import (
	"strings"
	"testing"
)

func TestBuildCommand(t *testing.T) {
	got, err := BuildCommand("auth done", []string{"decisions", "next step"}, []string{"raw output"})
	want := "/compact Milestone: auth done. KEEP: decisions; next step. DROP: raw output."
	if err != nil || got != want {
		t.Fatalf("got %q, %v; want %q", got, err, want)
	}
}

func TestBuildCommandRejects(t *testing.T) {
	cases := map[string]struct {
		milestone  string
		keep, drop []string
	}{
		"empty keep":      {"m", nil, []string{"d"}},
		"empty drop":      {"m", []string{"k"}, nil},
		"blank keep item": {"m", []string{"  "}, []string{"d"}},
		"empty milestone": {" ", []string{"k"}, []string{"d"}},
		"oversized":       {"m", []string{strings.Repeat("x", 801)}, []string{"d"}},
	}
	for name, c := range cases {
		if got, err := BuildCommand(c.milestone, c.keep, c.drop); err == nil {
			t.Errorf("%s: want error, got %q", name, got)
		}
	}
}

func TestBuildCommandSingleLine(t *testing.T) {
	got, err := BuildCommand("a\nb", []string{"k\r\n1", "tab\there"}, []string{`back\nslash`})
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"\n", "\r", "\t", `\`} {
		if strings.Contains(got, bad) {
			t.Errorf("command %q contains %q", got, bad)
		}
	}
}

func TestBuildResumeIsOneLine(t *testing.T) {
	got, err := BuildResume("continue with\nthe next task\\n now")
	if err != nil {
		t.Fatal(err)
	}
	if got != "continue with the next task n now" {
		t.Fatalf("got %q", got)
	}
}

func TestBuildResumeEmptyMeansOptOut(t *testing.T) {
	got, err := BuildResume("  \n ")
	if err != nil || got != "" {
		t.Fatalf("got %q err %v", got, err)
	}
}

func TestBuildResumeRejectsOverLimit(t *testing.T) {
	if _, err := BuildResume(strings.Repeat("a", maxResumeLen+1)); err == nil {
		t.Fatal("over-limit resume must fail")
	}
}
