package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
)

// `ccswitch exec` runs any command as a profile, not just Claude Code. The
// profile is still only an environment — CLAUDE_CONFIG_DIR pointing at its
// directory — so anything that honours that variable follows along: the VS
// Code extension when you open the editor this way, `claude mcp add` when you
// register a server, a script that drives `claude -p`. It is `run` with the
// binary made a parameter, and it borrows run's manners: shared settings are
// written first, the API-key variables are stripped, Ctrl-C belongs to the
// child, and its exit code comes straight back.
func cmdExec(args []string) error {
	const usage = "usage: ccswitch exec <profile> [--] <command> [args...]\n" +
		"       ccswitch exec --best [--] <command> [args...]\n" +
		"       ccswitch exec -- <command> [args...]   (uses this directory's linked profile)"

	var name string
	best := false
	rest := args
	if len(rest) > 0 && (rest[0] == "--best" || rest[0] == "-b") {
		best = true
		rest = rest[1:]
	}
	if !best && len(rest) > 0 && rest[0] != "--" {
		name, rest = rest[0], rest[1:]
	}
	if len(rest) > 0 && rest[0] == "--" {
		rest = rest[1:]
	}
	if len(rest) == 0 {
		return fmt.Errorf("%s", usage)
	}

	if best {
		chosen, why, err := bestProfile()
		if err != nil {
			return err
		}
		name = chosen
		fmt.Fprintf(os.Stderr, "ccswitch: %s has the most headroom (%s)\n", name, why)
	}
	if name == "" {
		linked, source, ok := CurrentDirProfile()
		if !ok {
			return fmt.Errorf("%s\n       or link this directory once: ccswitch link <profile>", usage)
		}
		name = linked
		fmt.Fprintf(os.Stderr, "ccswitch: %s (linked from %s)\n", name, source)
	}

	p, err := Find(name)
	if err != nil {
		// The likeliest slip is leaving the profile out: `ccswitch exec code .`
		// in an unlinked directory. Say what we took the word for.
		return fmt.Errorf("%v\n(the first argument is the profile — to run %q as this directory's linked profile, write: ccswitch exec -- %s)",
			err, rest[0], strings.Join(rest, " "))
	}

	bin, err := exec.LookPath(rest[0])
	if err != nil {
		return fmt.Errorf("%s not found on PATH", rest[0])
	}
	applyDefaultsOnLaunch(p)
	cmd := childCommand(context.Background(), bin, rest[1:]...)
	cmd.Env = childEnv(p.Dir)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	TouchProfile(p.Name)

	if k := ApiKeyInEnv(); k != "" {
		fmt.Fprintf(os.Stderr, "ccswitch: unsetting %s so the subscription login is used\n", k)
	}

	signal.Ignore(os.Interrupt)
	err = cmd.Run()
	signal.Reset(os.Interrupt)
	if err != nil {
		var ee *exec.ExitError
		if ok := asExitError(err, &ee); ok {
			os.Exit(ee.ExitCode())
		}
		return err
	}
	return nil
}
