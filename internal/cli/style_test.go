package cli

import (
	"regexp"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

var ansiPattern = regexp.MustCompile("\x1b\\[[0-9;]*m")

func stripANSI(text string) string { return ansiPattern.ReplaceAllString(text, "") }

// Styling is allowed to add weight and colour, never to change the text: the
// plain output, a saved transcript and the styled terminal have to read the same.
func TestStylesOnlyAddEscapes(t *testing.T) {
	plain, colored := styles{}, styles{enabled: true}
	if got := plain.bold("provider"); got != "provider" {
		t.Fatalf("plain bold = %q, want the text unchanged", got)
	}
	for name, render := range map[string]func(string) string{
		"bold": colored.bold, "faint": colored.faint,
	} {
		got := render("provider")
		if stripANSI(got) != "provider" {
			t.Errorf("%s changed the text: %q", name, got)
		}
		if got == "provider" {
			t.Errorf("%s added no escape sequence on a terminal", name)
		}
	}
	if got := colored.bold(""); got != "" {
		t.Errorf("bold of an empty string = %q, want it left empty", got)
	}
}

// A cell is padded before it is styled, so the escapes never count as column
// width and every row of a table lines up in both modes.
func TestHeaderAndDeltaKeepTheirColumnWidth(t *testing.T) {
	const format = "%-8s %-28s %-9s"
	plain, colored := styles{}, styles{enabled: true}

	styled := colored.header(format, "provider", "model", "stage")
	if want := plain.header(format, "provider", "model", "stage"); stripANSI(styled) != want {
		t.Errorf("styled header = %q, want %q plus escapes", stripANSI(styled), want)
	}

	rise, fall := 1.5, -1.5
	cases := []struct {
		name  string
		value *float64
		text  string
		color string
	}{
		{"rise", &rise, "+1.5pt", "\x1b[32m"},
		{"fall", &fall, "-1.5pt", "\x1b[31m"},
		{"unmeasured", nil, "—", "\x1b[2m"},
	}
	for _, one := range cases {
		got := colored.delta(one.value, one.text, 10)
		if width := lipgloss.Width(got); width != 10 {
			t.Errorf("%s: column width = %d, want 10 (%q)", one.name, width, got)
		}
		if !strings.HasPrefix(got, one.color) {
			t.Errorf("%s: colour = %q, want %q", one.name, got, one.color)
		}
		if want := plain.delta(one.value, one.text, 10); stripANSI(got) != want {
			t.Errorf("%s: styled cell = %q, want %q", one.name, stripANSI(got), want)
		}
	}

	// No change is neither a rise nor a fall.
	zero := 0.0
	if got := colored.delta(&zero, "±0.0pt", 10); strings.Contains(got, "\x1b[") {
		t.Errorf("an unchanged rate was coloured: %q", got)
	}
}

// Colour is a terminal-only feature, and --json is never styled: a consumer must
// be able to parse stdout whatever NO_COLOR or the terminal says.
func TestAllowColorOnlyOnATerminal(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	cases := []struct {
		name                       string
		terminal, jsonOut, noColor bool
		want                       bool
	}{
		{"terminal", true, false, false, true},
		{"pipe", false, false, false, false},
		{"json", true, true, false, false},
		{"flag", true, false, true, false},
	}
	for _, one := range cases {
		if got := allowColor(one.terminal, one.jsonOut, one.noColor); got != one.want {
			t.Errorf("%s: allowColor = %v, want %v", one.name, got, one.want)
		}
	}

	t.Setenv("NO_COLOR", "1")
	if allowColor(true, false, false) {
		t.Error("NO_COLOR was set but the output would still be styled")
	}
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "dumb")
	if allowColor(true, false, false) {
		t.Error("TERM=dumb was set but the output would still be styled")
	}
}

// The end-to-end guard: a command run with redirected stdout carries no escapes,
// so a log, a pipe or a test buffer holds exactly the text a reader sees.
func TestRedirectedOutputHasNoEscapes(t *testing.T) {
	h := newHarness(t)
	h.importQuestions()

	code, stdout, stderr := h.run("questions", "list")
	if code != ExitOK {
		t.Fatalf("questions list = %d: %s", code, stderr)
	}
	if strings.Contains(stdout, "\x1b") {
		t.Errorf("redirected stdout carries escape sequences:\n%q", stdout)
	}
	if !strings.Contains(stdout, "id") || !strings.Contains(stdout, "version") {
		t.Errorf("the table header is missing from the plain output:\n%s", stdout)
	}
}
