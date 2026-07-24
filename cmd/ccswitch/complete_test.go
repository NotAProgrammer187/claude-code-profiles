package main

import (
	"reflect"
	"strings"
	"testing"
)

var testProfiles = []string{"Work", "personal", "side"}

func TestCompletions(t *testing.T) {
	cases := []struct {
		name  string
		words []string
		want  []string
	}{
		{"nothing typed yet", nil, completeCommands},
		{"partial command", []string{"ru"}, []string{"run"}},
		{"command prefix matching several", []string{"u"}, []string{"use", "usage", "upgrade"}},
		{"profile after run", []string{"run", ""}, testProfiles},
		// Profile names are matched case-insensitively elsewhere, so a
		// lowercase prefix has to find an upper-case profile.
		{"profile is case-insensitive", []string{"run", "w"}, []string{"Work"}},
		{"profile after use", []string{"use", "p"}, []string{"personal"}},
		{"profile after where", []string{"where", "s"}, []string{"side"}},
		{"first name of rename", []string{"rename", "s"}, []string{"side"}},
		{"flags for use", []string{"use", "--"}, []string{"--unset", "--shell"}},
		{"shell values", []string{"use", "--shell", "f"}, []string{"fish"}},
		{"sync source", []string{"sync", "--from", "p"}, []string{"personal"}},
		{"sync items", []string{"sync", "--only", "s"}, []string{"settings", "skills"}},
		{"init shells", []string{"init", "z"}, []string{"zsh"}},
		{"nothing to offer", []string{"list", ""}, nil},
		{"unknown command", []string{"frobnicate", ""}, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := completions(tc.words, testProfiles)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("completions(%q) = %v, want %v", tc.words, got, tc.want)
			}
		})
	}
}

// The --line protocol is what the shells actually use, and the trailing space
// is the whole point of it: it separates "complete this word" from "complete
// the next one".
func TestLineWords(t *testing.T) {
	cases := []struct {
		line string
		want []string
	}{
		{"ccswitch", []string{}},
		{"ccswitch ", []string{""}},
		{"ccswitch run", []string{"run"}},
		{"ccswitch run ", []string{"run", ""}},
		{"ccswitch run wo", []string{"run", "wo"}},
		{"ccswitch sync --from work --to ", []string{"sync", "--from", "work", "--to", ""}},
		{`ccswitch run "two words"`, []string{"run", "two words"}},
		{"ccswitch  run   ", []string{"run", ""}}, // runs of spaces collapse
		{"", nil},
	}
	for _, tc := range cases {
		got := lineWords(tc.line)
		if len(got) == 0 && len(tc.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("lineWords(%q) = %#v, want %#v", tc.line, got, tc.want)
		}
	}
}

// The end-to-end shape: a line with a trailing space completes profiles, the
// same line without one completes the command.
func TestCompletionsFromLine(t *testing.T) {
	if got := completions(lineWords("ccswitch run "), testProfiles); !reflect.DeepEqual(got, testProfiles) {
		t.Errorf("trailing space should complete profiles, got %v", got)
	}
	if got := completions(lineWords("ccswitch run"), testProfiles); !reflect.DeepEqual(got, []string{"run"}) {
		t.Errorf("no trailing space should complete the command, got %v", got)
	}
	if got := completions(lineWords("ccswitch use --"), testProfiles); !reflect.DeepEqual(got, []string{"--unset", "--shell"}) {
		t.Errorf(`"--" should complete flags, got %v`, got)
	}
}

// A comma-separated value should extend its last element, not replace the whole
// list with one item.
func TestCompletionsInsideCommaList(t *testing.T) {
	got := completions([]string{"sync", "--only", "settings,co"}, testProfiles)
	want := []string{"settings,commands"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	got = completions([]string{"sync", "--to", "personal,s"}, testProfiles)
	want = []string{"personal,side"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// Everything the completer offers should be a documented command — the two
// lists drifting apart is how you end up completing to something that errors.
// (Checked against the help text rather than by calling run(), which would
// really try to upgrade the binary.)
func TestCompleteCommandsAreDocumented(t *testing.T) {
	for _, name := range completeCommands {
		if !strings.Contains(usage, "ccswitch "+name) {
			t.Errorf("completion offers %q but the help text doesn't mention it", name)
		}
	}
}

// The --only tokens must line up with the real sync items.
func TestSyncItemTokensMatchItems(t *testing.T) {
	tokens := syncItemTokens()
	if len(tokens) != len(syncItems) {
		t.Fatalf("got %d tokens for %d items", len(tokens), len(syncItems))
	}
	for i, it := range syncItems {
		if tokens[i] != it.Name {
			t.Errorf("token %d = %q, want %q", i, tokens[i], it.Name)
		}
	}
}

func TestNormalizeInitShell(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"pwsh", "powershell"},
		{"PowerShell", "powershell"},
		{"bash", "bash"},
		{"sh", "bash"},
		{"zsh", "zsh"},
		{"fish", "fish"},
	} {
		got, err := normalizeInitShell(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("normalizeInitShell(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
	// cmd.exe can't hold the integration, and saying so beats emitting a
	// snippet that silently does nothing.
	if _, err := normalizeInitShell("cmd"); err == nil {
		t.Error("expected cmd.exe to be rejected with an explanation")
	}
	if _, err := normalizeInitShell("elvish"); err == nil {
		t.Error("expected an unknown shell to be rejected")
	}
}

// The generated snippets must call the binary in a way that can't re-enter the
// wrapper function they define.
func TestInitSnippetsAvoidRecursion(t *testing.T) {
	for name, script := range map[string]string{"bash": bashInit, "zsh": zshInit, "fish": fishInit} {
		for _, line := range strings.Split(script, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") {
				continue
			}
			// Any invocation has to go through `command`, which bypasses
			// functions; a bare `ccswitch …` would call the wrapper again.
			if i := strings.Index(trimmed, "ccswitch "); i > 0 {
				before := trimmed[:i]
				if !strings.Contains(before, "command ") && !strings.Contains(before, "_ccswitch") &&
					!strings.Contains(before, "-c ") && !strings.Contains(before, "compdef") {
					t.Errorf("%s: possible recursive call: %s", name, trimmed)
				}
			}
		}
	}
	if strings.Contains(pwshInit, "& ccswitch") {
		t.Error("pwsh snippet must call the binary by path, not by name")
	}
}

func TestPsQuote(t *testing.T) {
	if got := psQuote(`C:\bin\ccswitch.exe`); got != `'C:\bin\ccswitch.exe'` {
		t.Errorf("got %s", got)
	}
	// A single quote in the path would otherwise end the string early.
	if got := psQuote(`C:\it's\ccswitch.exe`); got != `'C:\it''s\ccswitch.exe'` {
		t.Errorf("got %s", got)
	}
}
