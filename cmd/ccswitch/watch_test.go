package main

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
)

func TestMeterBar(t *testing.T) {
	plain := func(s string) string {
		return strings.ReplaceAll(lipgloss.NewStyle().Render(s), "\x1b", "\x1b")
	}
	_ = plain

	cases := []struct {
		util   float64
		filled int
	}{
		{0, 0},
		{100, 10},
		{50, 5},
		// Anything above zero has to show, or "1%" looks identical to unused.
		{0.4, 1},
		{1, 1},
		// Out-of-range values must clamp rather than draw past the track.
		{140, 10},
		{-5, 0},
	}
	for _, c := range cases {
		got := meterBar(10, c.util)
		if n := strings.Count(got, "█"); n != c.filled {
			t.Errorf("meterBar(10, %v) filled %d cells, want %d", c.util, n, c.filled)
		}
		if w := lipgloss.Width(got); w != 10 {
			t.Errorf("meterBar(10, %v) is %d columns wide, want 10", c.util, w)
		}
	}
}

func TestMeterStyleThresholds(t *testing.T) {
	// The picker warns at the same points; the two views must never disagree.
	if meterStyle(69).GetForeground() != sOK.GetForeground() {
		t.Error("69% should still read as fine")
	}
	if meterStyle(70).GetForeground() != sWarn.GetForeground() {
		t.Error("70% should warn")
	}
	if meterStyle(90).GetForeground() != sErr.GetForeground() {
		t.Error("90% should be an error colour")
	}
}

func TestTrunc(t *testing.T) {
	cases := []struct {
		in, want string
		max      int
	}{
		{"short", "short", 10},
		{"exactly-10", "exactly-10", 10},
		{"much too long here", "much too …", 10},
		{"x", "x", 1},  // already fits: nothing to cut
		{"xy", "…", 1}, // doesn't fit, and there's no room for a prefix
		{"x", "", 0},
	}
	for _, c := range cases {
		if got := trunc(c.in, c.max); got != c.want {
			t.Errorf("trunc(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
		}
	}
}

// A panel whose right edge wobbles looks broken, and styling is the usual
// cause — so measure every rendered line in display columns.
func TestWatchPanelIsRectangular(t *testing.T) {
	m := testWatchModel()
	for _, width := range []int{34, 40, 52, 80, 120} {
		m.width = width
		out := m.View()
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		if len(lines) < 5 {
			t.Fatalf("width %d: panel is only %d lines", width, len(lines))
		}
		want := lipgloss.Width(lines[0])
		for i, l := range lines {
			if got := lipgloss.Width(l); got != want {
				t.Errorf("width %d: line %d is %d columns, want %d\n%q", width, i, got, want, l)
			}
		}
		if !strings.HasPrefix(lines[0], "╭") || !strings.HasPrefix(lines[len(lines)-1], "╰") {
			t.Errorf("width %d: panel is not boxed", width)
		}
	}
}

// The art is optional and user-pasted, so it must not be able to break the box.
func TestWatchArtTooWideIsDropped(t *testing.T) {
	saved := watchArt
	defer func() { watchArt = saved }()
	watchArt = []string{"fits", strings.Repeat("W", 200)}

	m := testWatchModel()
	m.width = 40
	lines := strings.Split(strings.TrimRight(m.View(), "\n"), "\n")
	want := lipgloss.Width(lines[0])
	for i, l := range lines {
		if got := lipgloss.Width(l); got != want {
			t.Fatalf("over-wide art broke line %d: %d columns, want %d", i, got, want)
		}
	}
	if !strings.Contains(m.View(), "fits") {
		t.Error("art that does fit should still be drawn")
	}
}

func testWatchModel() watchModel {
	reset := time.Now().Add(2 * time.Hour)
	return watchModel{
		profiles: []Profile{
			{Name: "work", Email: "you@example.com", Plan: "Max", SignedIn: true},
			{Name: "personal", Email: "other@example.com", Plan: "Max", SignedIn: true},
		},
		usage: map[string]*Usage{
			"work": {
				Session: &UsageWindow{Utilization: 42, ResetsAt: reset},
				Week:    &UsageWindow{Utilization: 12, ResetsAt: reset.Add(72 * time.Hour)},
				Opus:    &UsageWindow{Utilization: 8},
			},
			"personal": {
				Session: &UsageWindow{Utilization: 94, ResetsAt: reset},
				Week:    &UsageWindow{Utilization: 73, ResetsAt: reset.Add(72 * time.Hour)},
				Opus:    &UsageWindow{},
			},
		},
		errs:    map[string]string{},
		active:  "work",
		every:   watchDefaultEvery,
		updated: time.Now(),
		width:   40,
		height:  40,
	}
}
