package main

import (
	"fmt"
	"strings"
)

// `ccswitch complete` is the engine behind tab completion in every shell: the
// snippets from `ccswitch init` hand it what you've typed so far and print what
// comes back, one candidate per line. Keeping the logic here rather than in
// four shell dialects means completion behaves the same everywhere and can be
// tested.
//
// The shells pass the command line as a single `--line <text>` argument rather
// than as separate words, because a trailing space has to survive the trip —
// it's the difference between completing `run` and completing the profile after
// it — and Windows PowerShell drops empty arguments to native commands.
func cmdComplete(args []string) error {
	words := args
	if len(args) > 0 {
		switch {
		case args[0] == "--line":
			line := ""
			if len(args) > 1 {
				line = args[1]
			}
			words = lineWords(line)
		case strings.HasPrefix(args[0], "--line="):
			words = lineWords(strings.TrimPrefix(args[0], "--line="))
		}
	}

	var names []string
	if ps, err := List(); err == nil {
		for _, p := range ps {
			names = append(names, p.Name)
		}
	}
	// Completion must never be noisy: a broken profile directory should cost
	// you candidates, not print an error into the middle of your prompt.
	for _, c := range completions(words, names) {
		fmt.Println(c)
	}
	return nil
}

// lineWords turns a command line into the argument words after the command
// name, with a trailing empty word when the line ends in whitespace — that
// empty word is what tells completions() you've finished a word and want the
// next one.
func lineWords(line string) []string {
	words := splitLine(line)
	if len(words) == 0 {
		return nil
	}
	return words[1:] // drop the command name itself
}

// splitLine splits a command line roughly the way a shell would, honouring
// single and double quotes so a profile name with a space in it survives.
func splitLine(line string) []string {
	var (
		words  []string
		cur    strings.Builder
		quote  rune
		inWord bool
	)
	for _, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			inWord = true
		case r == ' ' || r == '\t':
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if inWord {
		return append(words, cur.String())
	}
	// Ends in whitespace (or is empty): the cursor sits on a fresh word.
	return append(words, "")
}

// completeCommands is what we offer for the first word — the primary names
// only, so the list reads like the help text rather than every alias.
var completeCommands = []string{
	"run", "use", "usage", "sync", "defaults", "link", "unlink", "links", "new",
	"import", "rename", "rm", "list", "current", "where", "init", "upgrade",
	"version", "help",
}

var completeFlags = map[string][]string{
	"use":  {"--unset", "--shell"},
	"sync": {"--from", "--to", "--only", "--dry-run", "--yes"},
	"rm":   {"-y"},
}

var completeDefaultsSubs = []string{"set", "unset", "apply"}

// Settings you'd plausibly want identical everywhere. Completing a key is a
// convenience, not a restriction — any top-level settings.json key is accepted.
var completeDefaultsKeys = []string{
	"agentPushNotifEnabled", "autoCompactEnabled", "autoUpdates",
	"autoUploadSessions", "editorMode", "inputNeededNotifEnabled", "model",
	"remoteControlAtStartup", "theme", "todoFeatureEnabled", "tui", "verbose",
}

var completeShells = []string{"pwsh", "cmd", "bash", "zsh", "fish"}

// The shells `init` supports are narrower than the ones `use` accepts, because
// cmd.exe has no function to hang the integration on: see normalizeInitShell.
var completeInitShells = []string{"pwsh", "bash", "zsh", "fish"}

// completions returns the candidates for the last word in words, which is the
// partial word under the cursor (possibly empty). Everything before it is
// context. profiles is passed in rather than read from disk so this stays a
// pure function.
func completions(words, profiles []string) []string {
	if len(words) == 0 {
		return matching(completeCommands, "")
	}
	prefix := words[len(words)-1]
	prior := words[:len(words)-1]
	if len(prior) == 0 {
		return matching(completeCommands, prefix)
	}

	cmd := strings.ToLower(prior[0])

	// A flag that takes a value wants the value completed, not another flag.
	switch strings.ToLower(prior[len(prior)-1]) {
	case "--from":
		return matching(profiles, prefix)
	case "--to":
		return matchingList(profiles, prefix)
	case "--only":
		return matchingList(syncItemTokens(), prefix)
	case "--shell":
		return matching(completeShells, prefix)
	}

	if strings.HasPrefix(prefix, "-") {
		return matching(completeFlags[cmd], prefix)
	}

	switch cmd {
	case "defaults":
		// `defaults <sub>`, then a key for set/unset or profiles for apply.
		// `set`'s value is yours to type — we don't know what it should be.
		if len(prior) == 1 {
			return matching(completeDefaultsSubs, prefix)
		}
		switch strings.ToLower(prior[1]) {
		case "set", "unset", "rm", "remove":
			if len(prior) == 2 {
				return matching(completeDefaultsKeys, prefix)
			}
		case "apply":
			return matching(profiles, prefix)
		}
	case "link":
		// `link <profile> [dir]`: after the profile the argument is a
		// directory, which the shell's own path completion does better.
		if len(prior) == 1 {
			return matching(profiles, prefix)
		}
	case "run", "use", "where", "rm", "remove", "sync":
		// One profile argument, then we have nothing useful to add — anything
		// after `run <profile> --` belongs to claude, not to us.
		if len(prior) == 1 {
			return matching(profiles, prefix)
		}
	case "rename", "mv":
		if len(prior) == 1 {
			return matching(profiles, prefix)
		}
	case "init":
		if len(prior) == 1 {
			return matching(completeInitShells, prefix)
		}
	}
	return nil
}

func syncItemTokens() []string {
	out := make([]string, 0, len(syncItems))
	for _, it := range syncItems {
		out = append(out, it.Name)
	}
	return out
}

// matching keeps the candidates that start with prefix, case-insensitively so
// `ccswitch run w<tab>` still finds a profile called Work.
func matching(candidates []string, prefix string) []string {
	p := strings.ToLower(prefix)
	var out []string
	for _, c := range candidates {
		if strings.HasPrefix(strings.ToLower(c), p) {
			out = append(out, c)
		}
	}
	return out
}

// matchingList completes inside a comma-separated value, so `--only
// settings,co<tab>` extends the last element instead of replacing the lot.
func matchingList(candidates []string, prefix string) []string {
	i := strings.LastIndex(prefix, ",")
	if i < 0 {
		return matching(candidates, prefix)
	}
	head, tail := prefix[:i+1], prefix[i+1:]
	var out []string
	for _, m := range matching(candidates, tail) {
		out = append(out, head+m)
	}
	return out
}
