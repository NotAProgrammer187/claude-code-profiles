package main

import (
	"fmt"
	"os"
	"runtime"
	"strings"
)

// cmdUse prints the command that pins the *current shell* to a profile, so a
// bare `claude` in that shell runs as that account. A child process can't
// reach up and change its parent's environment, so — like nvm or direnv —
// the caller evals what we print:
//
//	PowerShell:  Invoke-Expression (ccswitch use work)
//	bash/zsh:    eval "$(ccswitch use work)"
//	fish:        ccswitch use work --shell fish | source
//
// `ccswitch use --unset` prints the command that unpins the shell again.
func cmdUse(args []string) error {
	var name, shell string
	unset := false
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--unset" || a == "-u":
			unset = true
		case a == "--shell":
			i++
			if i >= len(args) {
				return fmt.Errorf("--shell needs a value: pwsh, cmd, bash or fish")
			}
			shell = args[i]
		case strings.HasPrefix(a, "--shell="):
			shell = strings.TrimPrefix(a, "--shell=")
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("unknown flag %q (try: ccswitch use <profile> [--shell pwsh|cmd|bash|fish])", a)
		case name == "":
			name = a
		default:
			return fmt.Errorf("unexpected argument %q", a)
		}
	}

	sh, err := normalizeShell(shell)
	if err != nil {
		return err
	}

	if unset {
		emit(sh, clearAssignment(sh), "ccswitch use --unset")
		return nil
	}
	// Like `run`, bare `ccswitch use` takes the profile this directory is
	// linked to. The note goes to stderr so an eval only ever sees the
	// assignment on stdout.
	if name == "" {
		linked, source, ok := CurrentDirProfile()
		if !ok {
			return fmt.Errorf("usage: ccswitch use <profile> | ccswitch use --unset\n" +
				"       or link this directory once: ccswitch link <profile>")
		}
		name = linked
		fmt.Fprintf(os.Stderr, "ccswitch: %s (linked from %s)\n", name, source)
	}

	p, err := Find(name)
	if err != nil {
		return err
	}
	applyDefaultsOnLaunch(p)
	TouchProfile(p.Name)
	emit(sh, assignment(sh, p.Dir), "ccswitch use "+p.Name)
	return nil
}

// emit writes the assignment to stdout — the only thing an eval sees — and,
// when stdout is a terminal (so nothing is eval'ing us), a how-to hint on
// stderr.
func emit(shell, line, invocation string) {
	fmt.Println(line)
	if fi, err := os.Stdout.Stat(); err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return
	}
	var hint string
	switch shell {
	case "powershell":
		hint = "# printed only — apply it to this shell with:\n#   Invoke-Expression (" + invocation + ")"
	case "cmd":
		hint = "rem printed only — apply it by pasting the line above into this prompt"
	case "fish":
		hint = "# printed only — apply it to this shell with:\n#   " + invocation + " --shell fish | source"
	default:
		hint = "# printed only — apply it to this shell with:\n#   eval \"$(" + invocation + ")\""
	}
	fmt.Fprintln(os.Stderr, hint)
}

// normalizeShell maps a --shell value (or, when empty, the environment) to
// one of: powershell, cmd, posix, fish.
func normalizeShell(s string) (string, error) {
	switch strings.ToLower(s) {
	case "pwsh", "powershell":
		return "powershell", nil
	case "cmd", "bat":
		return "cmd", nil
	case "bash", "zsh", "sh", "posix":
		return "posix", nil
	case "fish":
		return "fish", nil
	case "":
		return detectShell(), nil
	}
	return "", fmt.Errorf("unknown shell %q: use pwsh, cmd, bash or fish", s)
}

// detectShell guesses the calling shell. SHELL covers every POSIX shell
// including Git Bash on Windows; otherwise Windows means PowerShell in
// practice (cmd users can pass --shell cmd).
func detectShell() string {
	if os.Getenv("FISH_VERSION") != "" {
		return "fish"
	}
	if os.Getenv("SHELL") != "" {
		return "posix"
	}
	if runtime.GOOS == "windows" {
		return "powershell"
	}
	return "posix"
}

func assignment(shell, dir string) string {
	switch shell {
	case "powershell":
		return "$env:CLAUDE_CONFIG_DIR = '" + strings.ReplaceAll(dir, "'", "''") + "'"
	case "cmd":
		return "set \"CLAUDE_CONFIG_DIR=" + dir + "\""
	case "fish":
		return "set -gx CLAUDE_CONFIG_DIR " + posixQuote(dir)
	default:
		return "export CLAUDE_CONFIG_DIR=" + posixQuote(dir)
	}
}

func clearAssignment(shell string) string {
	switch shell {
	case "powershell":
		// Assigning $null removes the variable entirely.
		return "$env:CLAUDE_CONFIG_DIR = $null"
	case "cmd":
		return "set \"CLAUDE_CONFIG_DIR=\""
	case "fish":
		return "set -e CLAUDE_CONFIG_DIR"
	default:
		return "unset CLAUDE_CONFIG_DIR"
	}
}

func posixQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
