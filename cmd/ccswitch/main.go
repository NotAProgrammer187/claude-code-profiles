package main

import (
	"fmt"
	"os"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"
)

const version = "0.1.3"

const usage = `ccswitch — run Claude Code as any of your accounts, without logging out

  ccswitch                 open the picker
  ccswitch run <name>      launch straight into a profile
  ccswitch run <name> -- --resume
                           extra args after -- go to claude
  ccswitch list            print profiles
  ccswitch current         print the profile this shell is set to
  ccswitch where <name>    print a profile's config directory
  ccswitch upgrade         update ccswitch to the latest release
  ccswitch version

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
	if len(args) == 0 {
		return fmt.Errorf("usage: ccswitch run <profile> [-- claude args...]")
	}
	name := args[0]
	rest := args[1:]
	if len(rest) > 0 && rest[0] == "--" {
		rest = rest[1:]
	}

	p, err := Find(name)
	if err != nil {
		return err
	}
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
