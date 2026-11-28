package cli

import (
	"fmt"
	"io"
	"os"

	"charm.land/lipgloss/v2"
)

// Styling is a presentation decision, so it lives with the other I/O decisions
// in this package. Human output on stdout is styled; --json and stderr are not.
//
// The styles add weight and colour only — never padding, borders or width. The
// column widths come from the format strings, and a cell is padded before it is
// styled, so the escape sequences never widen a column and a plain transcript
// lines up exactly like a coloured one.
var (
	boldStyle  = lipgloss.NewStyle().Bold(true)
	faintStyle = lipgloss.NewStyle().Faint(true)
	goodStyle  = lipgloss.NewStyle().Foreground(lipgloss.Green)
	badStyle   = lipgloss.NewStyle().Foreground(lipgloss.Red)
)

// styles renders the human output. With colour off (the default in tests, pipes
// and redirected logs) every method returns its input unchanged, so the same
// code path produces the same text everywhere and only a terminal carries the
// escapes.
//
// The colours are the eight basic ANSI ones, which every terminal understands:
// nothing has to be downsampled and no terminal capability is queried.
type styles struct{ enabled bool }

// styles decides once per command whether stdout may carry escape sequences.
func (a *App) styles() styles {
	return styles{enabled: allowColor(isTerminal(a.Out), a.JSON, a.NoColor)}
}

// allowColor reports whether the human output may be styled. Only a terminal
// gets escapes, --json never does, and the usual opt-outs (--no-color, and the
// NO_COLOR / TERM=dumb environment) always win.
func allowColor(terminal, jsonOut, noColor bool) bool {
	return terminal && !jsonOut && !noColor &&
		os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
}

func (s styles) bold(text string) string { return s.render(boldStyle, text) }

func (s styles) faint(text string) string { return s.render(faintStyle, text) }

// render applies one style, or nothing. Styling an empty string is skipped: it
// would add reset sequences around no text at all.
func (s styles) render(style lipgloss.Style, text string) string {
	if !s.enabled || text == "" {
		return text
	}
	return style.Render(text)
}

// header renders a table's first row. The format string is the one the rows use,
// so the header and the rows cannot drift apart.
func (s styles) header(format string, args ...any) string {
	return s.bold(fmt.Sprintf(format, args...))
}

// planLine prints one line of the run plan: a label padded to ten columns, then
// one space, then the value. The padding is part of the label cell, so the
// values line up and a plain transcript keeps the same columns.
func (s styles) planLine(out io.Writer, label, format string, args ...any) {
	fmt.Fprintf(out, "%s %s\n", s.bold(fmt.Sprintf("%-10s", label)), fmt.Sprintf(format, args...))
}

// delta renders one change column: green when the rate rose, red when it fell,
// dim when a side was not measured (there is no direction to show). The cell is
// padded to width before it is styled, so the escapes do not count as width.
func (s styles) delta(value *float64, text string, width int) string {
	cell := fmt.Sprintf("%-*s", width, text)
	switch {
	case value == nil:
		return s.faint(cell)
	case *value > 0:
		return s.render(goodStyle, cell)
	case *value < 0:
		return s.render(badStyle, cell)
	default:
		return cell
	}
}
