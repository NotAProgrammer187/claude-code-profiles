package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// envOverride is the variable Claude Code reads to relocate its entire config
// directory — credentials, settings, MCP servers and session history included.
// Pointing it at a per-account directory is what makes switching instant:
// nothing is copied, moved or overwritten, so no account can clobber another.
const envOverride = "CLAUDE_CONFIG_DIR"

// keyVars override a subscription login when present, so we strip them from
// the child environment and surface a warning instead of silently billing the
// wrong thing.
var keyVars = []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"}

// ApiKeyInEnv reports whether the parent shell has an API key set.
func ApiKeyInEnv() string {
	for _, k := range keyVars {
		if os.Getenv(k) != "" {
			return k
		}
	}
	return ""
}

// ClaudeBinary resolves the claude executable, honouring PATHEXT on Windows.
func ClaudeBinary() (string, error) {
	bin, err := exec.LookPath("claude")
	if err != nil {
		return "", fmt.Errorf("claude not found on PATH — install Claude Code first")
	}
	return bin, nil
}

// Command builds the process that runs Claude Code against one profile.
func Command(p Profile, args []string) (*exec.Cmd, error) {
	bin, err := ClaudeBinary()
	if err != nil {
		return nil, err
	}

	var cmd *exec.Cmd
	lower := strings.ToLower(bin)
	if runtime.GOOS == "windows" && (strings.HasSuffix(lower, ".cmd") || strings.HasSuffix(lower, ".bat")) {
		// CreateProcess cannot execute batch files directly; the npm install
		// of Claude Code ships claude.cmd, so route those through cmd.exe.
		cmd = exec.Command("cmd.exe", append([]string{"/c", bin}, args...)...)
	} else {
		cmd = exec.Command(bin, args...)
	}

	cmd.Env = childEnv(p.Dir)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd, nil
}

func childEnv(dir string) []string {
	drop := map[string]bool{strings.ToUpper(envOverride): true}
	for _, k := range keyVars {
		drop[k] = true
	}

	out := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		i := strings.IndexByte(kv, '=')
		if i > 0 && drop[strings.ToUpper(kv[:i])] {
			continue
		}
		out = append(out, kv)
	}
	return append(out, envOverride+"="+dir)
}
