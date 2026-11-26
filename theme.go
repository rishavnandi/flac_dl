//go:build !vercel

package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// AMOLED palette: the terminal owns the canvas; only selected controls get a fill.
var (
	colCanvas lipgloss.TerminalColor = lipgloss.NoColor{}
	colRaised                        = lipgloss.Color("#151515")
	colBorder                        = lipgloss.Color("#303030")
	colText                          = lipgloss.Color("#f5f5f5")
	colBody                          = lipgloss.Color("#c8c8c8")
	colMuted                         = lipgloss.Color("#8a8a8a")
	colDim                           = lipgloss.Color("#626262")
	colAccent                        = lipgloss.Color("#ff4d55")
)

var frameStyle = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(colBorder).
	BorderBackground(colCanvas).
	Background(colCanvas).
	Padding(0, 1)

func fg(c lipgloss.Color) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(c).Background(colCanvas)
}

func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	if w == 1 {
		return "…"
	}
	return ansi.Truncate(s, w, "…")
}

func padRight(s string, w int) string {
	s = truncate(s, w)
	if gap := w - lipgloss.Width(s); gap > 0 {
		s += strings.Repeat(" ", gap)
	}
	return s
}

func padLeft(s string, w int) string {
	s = truncate(s, w)
	if gap := w - lipgloss.Width(s); gap > 0 {
		s = strings.Repeat(" ", gap) + s
	}
	return s
}

func bgSpaces(n int, bg lipgloss.TerminalColor) string {
	if n <= 0 {
		return ""
	}
	return lipgloss.NewStyle().Background(bg).Render(strings.Repeat(" ", n))
}

// fillTo pads a styled line with the container background so borders never
// show a terminal-coloured gap.
func fillTo(s string, w int, bg lipgloss.TerminalColor) string {
	s = truncate(s, w)
	if gap := w - lipgloss.Width(s); gap > 0 {
		s += bgSpaces(gap, bg)
	}
	return s
}

// centerLines centres a block inside w x h cells and fills the rest.
func centerLines(lines []string, w, h int) []string {
	out := make([]string, 0, h)
	top := (h - len(lines)) / 2
	for i := 0; i < h; i++ {
		if i < top || i >= top+len(lines) {
			out = append(out, bgSpaces(w, colCanvas))
			continue
		}
		s := truncate(lines[i-top], w)
		gap := w - lipgloss.Width(s)
		left := gap / 2
		out = append(out, bgSpaces(left, colCanvas)+s+bgSpaces(gap-left, colCanvas))
	}
	return out
}

func progressBar(done, total int64, width int) string {
	if width < 4 {
		width = 4
	}
	filled := 0
	if total > 0 {
		filled = int(float64(done) / float64(total) * float64(width))
		if filled > width {
			filled = width
		}
		if filled < 1 && done > 0 {
			filled = 1
		}
	}
	return lipgloss.NewStyle().Foreground(colAccent).Render(strings.Repeat("█", filled)) +
		lipgloss.NewStyle().Foreground(colBorder).Render(strings.Repeat("░", width-filled))
}

func megabytes(n int64) string {
	return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
}
