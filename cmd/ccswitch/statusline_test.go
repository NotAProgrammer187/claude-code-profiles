package main

import (
	"strings"
	"testing"
	"time"
)

func TestStatuslineText(t *testing.T) {
	// No usage yet: just the label (and model), no stray separators.
	if got := statuslineText("work", "", nil); got != "work" {
		t.Errorf("bare label rendered %q", got)
	}
	got := statuslineText("work", "Opus 4.5", nil)
	if !strings.Contains(got, "work") || !strings.Contains(got, "Opus 4.5") {
		t.Errorf("label+model rendered %q", got)
	}

	u := &Usage{
		Session: &UsageWindow{Utilization: 42},
		Week:    &UsageWindow{Utilization: 12},
	}
	got = statuslineText("work", "Opus 4.5", u)
	for _, want := range []string{"work", "Opus 4.5", "5h 42%", "wk 12%"} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered %q, missing %q", got, want)
		}
	}

	// Opus only earns a slot once it's been used, like every other view.
	u.Opus = &UsageWindow{Utilization: 0}
	if got := statuslineText("w", "", u); strings.Contains(got, "opus") {
		t.Errorf("unused opus window must not render, got %q", got)
	}
	u.Opus.Utilization = 31
	if got := statuslineText("w", "", u); !strings.Contains(got, "opus 31%") {
		t.Errorf("used opus window must render, got %q", got)
	}
}

func TestStatuslineWindowThresholds(t *testing.T) {
	reset := time.Now().Add(2 * time.Hour)

	if got := statuslineWindow("5h", &UsageWindow{Utilization: 42}); strings.Contains(got, "\x1b[3") {
		t.Errorf("42%% must be uncoloured, got %q", got)
	}
	if got := statuslineWindow("5h", &UsageWindow{Utilization: 71}); !strings.Contains(got, ansiYellow) {
		t.Errorf("71%% must be amber, got %q", got)
	}
	got := statuslineWindow("5h", &UsageWindow{Utilization: 93, ResetsAt: reset})
	if !strings.Contains(got, ansiRed) {
		t.Errorf("93%% must be red, got %q", got)
	}
	// The red case is when you'd switch accounts, so it carries the reset time.
	if !strings.Contains(got, "resets") {
		t.Errorf("red window must show its reset time, got %q", got)
	}
}

func TestReadStatuslineModel(t *testing.T) {
	if got := readStatuslineModel(strings.NewReader(`{"model":{"display_name":"Opus 4.5"}}`)); got != "Opus 4.5" {
		t.Errorf("got %q", got)
	}
	// Anything unparseable degrades to no model, never an error.
	for _, in := range []string{"", "not json", `{"model":{}}`} {
		if got := readStatuslineModel(strings.NewReader(in)); got != "" {
			t.Errorf("readStatuslineModel(%q) = %q, want empty", in, got)
		}
	}
}

func TestUsageCacheKeyFoldsCase(t *testing.T) {
	a := usageCacheKey(`C:\Users\R\.ccswitch\profiles\Work`)
	b := usageCacheKey(`c:\users\r\.ccswitch\profiles\work`)
	if a != b {
		t.Errorf("two spellings of one directory got different keys: %q vs %q", a, b)
	}
}
