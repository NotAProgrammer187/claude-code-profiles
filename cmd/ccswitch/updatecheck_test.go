package main

import (
	"strings"
	"testing"
)

func TestNewerVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"0.1.8", "0.1.7", true},
		{"0.2.0", "0.1.9", true},
		{"1.0.0", "0.9.9", true},
		{"0.1.7", "0.1.7", false},
		{"0.1.6", "0.1.7", false},
		{"0.1.7.1", "0.1.7", true}, // longer wins when the prefix ties
		{"0.1.7", "0.1.7.1", false},
		{"0.1.10", "0.1.9", true}, // numeric, not lexicographic
		{"", "0.1.7", false},
		{"0.1.8", "", false},
		{"abc", "0.1.7", false}, // garbage never nudges
		{"0.1.8", "abc", false},
	}
	for _, c := range cases {
		if got := newerVersion(c.a, c.b); got != c.want {
			t.Errorf("newerVersion(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestParseClaudeVersion(t *testing.T) {
	cases := []struct {
		out, want string
	}{
		{"2.1.3 (Claude Code)", "2.1.3"},
		{"claude, version 1.0.61", "1.0.61"},
		{"v2.0.0", "2.0.0"},
		{"Claude Code", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := parseClaudeVersion(c.out); got != c.want {
			t.Errorf("parseClaudeVersion(%q) = %q, want %q", c.out, got, c.want)
		}
	}
}

func TestNudgesFor(t *testing.T) {
	selfUpgrade := func() string { return "ccswitch upgrade" }

	// Everything current: silence.
	if n := nudgesFor(updateCache{CcswitchLatest: version, ClaudeLatest: "2.0.0", ClaudeHave: "2.0.0"}, selfUpgrade); len(n) != 0 {
		t.Errorf("up-to-date cache must not nudge, got %v", n)
	}

	// Claude Code behind: exactly one nudge, naming both versions.
	n := nudgesFor(updateCache{ClaudeLatest: "2.1.0", ClaudeHave: "2.0.0"}, selfUpgrade)
	if len(n) != 1 || !strings.Contains(n[0], "2.1.0") || !strings.Contains(n[0], "2.0.0") {
		t.Errorf("stale Claude Code must nudge with both versions, got %v", n)
	}

	// A latest with no installed version read stays quiet — nothing to compare.
	if n := nudgesFor(updateCache{ClaudeLatest: "9.9.9"}, selfUpgrade); len(n) != 0 {
		t.Errorf("unknown installed version must not nudge, got %v", n)
	}

	// Both tools behind: two nudges, ccswitch's naming the upgrade command.
	n = nudgesFor(updateCache{CcswitchLatest: "99.0.0", ClaudeLatest: "2.1.0", ClaudeHave: "2.0.0"}, selfUpgrade)
	if len(n) != 2 || !strings.Contains(n[0], "ccswitch upgrade") {
		t.Errorf("both stale must yield two nudges, got %v", n)
	}

	// A package-manager install nudges with that manager's command, not ours.
	n = nudgesFor(updateCache{CcswitchLatest: "99.0.0"}, func() string { return "scoop update ccswitch" })
	if len(n) != 1 || !strings.Contains(n[0], "scoop update ccswitch") {
		t.Errorf("managed install must nudge with the manager's command, got %v", n)
	}

	// The hint is only computed when a ccswitch nudge actually fires — the
	// common up-to-date path must stay free of the symlink walk behind it.
	called := false
	nudgesFor(updateCache{CcswitchLatest: version}, func() string { called = true; return "x" })
	if called {
		t.Error("upgradeCmd must not be evaluated when no ccswitch nudge fires")
	}
}
