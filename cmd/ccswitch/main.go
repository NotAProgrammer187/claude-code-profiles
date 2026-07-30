package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

const version = "0.1.5"

const usage = `ccswitch — run Claude Code as any of your accounts, without logging out

  ccswitch                 open the picker
  ccswitch run <name>      launch straight into a profile ('run' with no name
                           uses the profile this directory is linked to)
  ccswitch run <name> -- --resume
                           extra args after -- go to claude
  ccswitch use <name>      pin this shell to a profile (prints the command;
                           eval it, e.g. Invoke-Expression (ccswitch use work))
  ccswitch use --unset     print the command that unpins this shell
  ccswitch init <shell>    print shell integration: makes 'use' apply itself and
                           adds tab completion (pwsh, bash, zsh, fish)
  ccswitch usage           show each account's rate-limit usage
  ccswitch sync --from <name>
                           copy shared config (settings, CLAUDE.md, commands,
                           agents, skills, MCP servers) into your other
                           profiles; --to a,b, --only <items>, -n to preview
  ccswitch defaults        show the settings shared by every profile
  ccswitch defaults set <key> <value>
                           share one settings.json key with every profile, now
                           and on each profile's first login, e.g.
                           ccswitch defaults set remoteControlAtStartup false
  ccswitch defaults unset <key>
                           stop sharing it (profiles keep what they have)
  ccswitch defaults apply [name...]
                           write the shared settings into profiles now
  ccswitch link <name>     use this profile for the current directory and
                           everything under it
  ccswitch unlink          drop this directory's link
  ccswitch links           list linked directories
  ccswitch new <name>      create an empty profile (sign in on first launch)
  ccswitch import <name>   copy the current ~/.claude into a new profile
  ccswitch rename <a> <b>  rename a profile
  ccswitch rm <name>       delete a profile and its login (-y skips the prompt)
  ccswitch list            print profiles
  ccswitch current         print the profile this shell is set to
  ccswitch where <name>    print a profile's config directory
  ccswitch upgrade         update ccswitch to the latest release
  ccswitch version
  ccswitch help

Each profile is its own CLAUDE_CONFIG_DIR, so accounts never share
credentials, settings, MCP servers or history — and you can run two at
once in two terminals.
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "ccswitch: "+err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	cleanupOldBinary()

	if len(args) == 0 {
		return interactive()
	}

	switch args[0] {
	case "run":
		return cmdRun(args[1:])
	case "use":
		return cmdUse(args[1:])
	case "usage", "limits":
		return cmdUsage()
	case "sync":
		return cmdSync(args[1:])
	case "defaults":
		return cmdDefaults(args[1:])
	case "link":
		return cmdLink(args[1:])
	case "unlink":
		return cmdUnlink(args[1:])
	case "links":
		return cmdLinks()
	case "init":
		return cmdInit(args[1:])
	case "complete":
		// Internal: called by the snippets `ccswitch init` prints.
		return cmdComplete(args[1:])
	case "new", "add", "create":
		return cmdNew(args[1:])
	case "import":
		return cmdImport(args[1:])
	case "rename", "mv":
		return cmdRename(args[1:])
	case "rm", "remove":
		return cmdRm(args[1:])
	case "list", "ls":
		return cmdList()
	case "current":
		return cmdCurrent()
	case "where":
		return cmdWhere(args[1:])
	case "upgrade", "self-update":
		return cmdUpgrade()
	case "version", "--version", "-v":
		fmt.Println("ccswitch " + version)
		return nil
	case "help", "--help", "-h":
		fmt.Print(usage)
		return nil
	default:
		return fmt.Errorf("unknown command %q (try: ccswitch help)", args[0])
	}
}

func interactive() error {
	p := tea.NewProgram(newModel(), tea.WithAltScreen())
	_, err := p.Run()
	return err
}

func cmdRun(args []string) error {
	// No profile named: fall back to the one this directory is linked to, so
	// `ccswitch run` inside a linked tree just works.
	var name string
	rest := args
	if len(args) > 0 && args[0] != "--" {
		name, rest = args[0], args[1:]
	}
	if len(rest) > 0 && rest[0] == "--" {
		rest = rest[1:]
	}
	if name == "" {
		linked, source, ok := CurrentDirProfile()
		if !ok {
			return fmt.Errorf("usage: ccswitch run <profile> [-- claude args...]\n" +
				"       or link this directory once: ccswitch link <profile>")
		}
		name = linked
		fmt.Fprintf(os.Stderr, "ccswitch: %s (linked from %s)\n", name, source)
	}

	p, err := Find(name)
	if err != nil {
		return err
	}
	// Before the process starts, so Claude Code reads the shared settings on
	// this run rather than the next one.
	applyDefaultsOnLaunch(p)
	cmd, err := Command(p, rest)
	if err != nil {
		return err
	}
	TouchProfile(p.Name)

	if k := ApiKeyInEnv(); k != "" {
		fmt.Fprintf(os.Stderr, "ccswitch: unsetting %s so the subscription login is used\n", k)
	}

	if err := cmd.Run(); err != nil {
		// Pass Claude Code's own exit code straight through.
		var ee *exec.ExitError
		if ok := asExitError(err, &ee); ok {
			os.Exit(ee.ExitCode())
		}
		return err
	}
	return nil
}

// confirm asks a yes/no question on the terminal. Anything other than y/yes —
// including EOF, so a piped-in run doesn't silently proceed — means no.
func confirm(question string) bool {
	fmt.Print(question)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		fmt.Println()
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}

func asExitError(err error, target **exec.ExitError) bool {
	if ee, ok := err.(*exec.ExitError); ok {
		*target = ee
		return true
	}
	return false
}

func cmdList() error {
	ps, err := List()
	if err != nil {
		return err
	}
	if len(ps) == 0 {
		fmt.Println("no profiles yet — run ccswitch and press i or n")
		return nil
	}
	for _, p := range ps {
		status, _ := p.Status()
		fmt.Printf("%-18s %-30s %s\n", p.Name, p.Label(), status)
	}
	return nil
}

func cmdCurrent() error {
	ps, err := List()
	if err != nil {
		return err
	}
	if name := ActiveProfileName(ps); name != "" {
		fmt.Println(name)
		return nil
	}
	if dir := ActiveConfigDir(); dir != "" {
		fmt.Printf("CLAUDE_CONFIG_DIR points at %s (not a ccswitch profile)\n", dir)
		return nil
	}
	fmt.Println("no active profile — CLAUDE_CONFIG_DIR is not set in this shell")
	// The shell isn't pinned, but this directory may still decide what a bare
	// `ccswitch run` would launch.
	if name, source, ok := CurrentDirProfile(); ok {
		fmt.Printf("this directory is linked to %s (from %s)\n", name, source)
	}
	return nil
}

func cmdWhere(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: ccswitch where <profile>")
	}
	p, err := Find(args[0])
	if err != nil {
		return err
	}
	fmt.Println(p.Dir)
	return nil
}

func cmdNew(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: ccswitch new <name>")
	}
	p, err := Create(args[0])
	if err != nil {
		return err
	}
	fmt.Printf("Created %s — sign in with: ccswitch run %s\n", p.Name, p.Name)
	return nil
}

func cmdImport(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: ccswitch import <name>")
	}
	p, err := Import(args[0])
	if err != nil {
		return err
	}
	if p.Email != "" {
		fmt.Printf("Imported %s into %s.\n", p.Email, p.Name)
	} else {
		fmt.Printf("Imported existing config into %s.\n", p.Name)
	}
	return nil
}

func cmdRename(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: ccswitch rename <old> <new>")
	}
	if err := Rename(args[0], args[1]); err != nil {
		return err
	}
	fmt.Printf("Renamed %s to %s.\n", args[0], args[1])
	return nil
}

func cmdRm(args []string) error {
	var name string
	yes := false
	for _, a := range args {
		switch {
		case a == "-y" || a == "--yes":
			yes = true
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("unknown flag %q (usage: ccswitch rm <profile> [-y])", a)
		case name == "":
			name = a
		default:
			return fmt.Errorf("unexpected argument %q", a)
		}
	}
	if name == "" {
		return fmt.Errorf("usage: ccswitch rm <profile> [-y]")
	}

	// Resolve first so the prompt names the real profile, not a typo.
	p, err := Find(name)
	if err != nil {
		return err
	}
	if !yes {
		q := fmt.Sprintf("Delete profile %q and its saved login? This can't be undone. [y/N] ", p.Name)
		if !confirm(q) {
			fmt.Println("Cancelled.")
			return nil
		}
	}
	if err := Delete(p.Name); err != nil {
		return err
	}
	fmt.Printf("Deleted %s.\n", p.Name)
	return nil
}
