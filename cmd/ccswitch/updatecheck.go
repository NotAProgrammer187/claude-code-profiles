package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Passive update nudges. Neither ccswitch nor the claude binary it launches
// announces new versions on its own, so a detached helper refreshes a small
// cache at most once a day, and the moments you're back at a prompt — Claude
// Code exiting, the picker closing — print one line per stale tool. Nothing
// ever installs itself, and no launch waits on the network: nudges read only
// the cache, and the refresh runs in a separate process.

// updateCheckEvery is how stale the cache may get before a refresh is spawned.
const updateCheckEvery = 24 * time.Hour

// noUpdateCheckEnv silences the whole feature: no refresh, no nudges.
const noUpdateCheckEnv = "CCSWITCH_NO_UPDATE_CHECK"

// claudeLatestURL is npm's dist-tag endpoint for Claude Code — the registry
// every install method tracks, and a stable one-field JSON answer.
const claudeLatestURL = "https://registry.npmjs.org/@anthropic-ai/claude-code/latest"

type updateCache struct {
	CheckedAt      time.Time `json:"checked_at"`
	CcswitchLatest string    `json:"ccswitch_latest"` // without the leading v
	ClaudeLatest   string    `json:"claude_latest"`
	ClaudeHave     string    `json:"claude_installed"`
}

func updateCachePath() (string, error) {
	root, err := Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "update-check.json"), nil
}

func loadUpdateCache() updateCache {
	var c updateCache
	path, err := updateCachePath()
	if err != nil {
		return c
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return c
	}
	_ = json.Unmarshal(data, &c)
	return c
}

// kickUpdateCheck spawns the refresh helper if the cache has gone stale. It
// must never delay the caller, so all it does is fork and forget.
func kickUpdateCheck() {
	if os.Getenv(noUpdateCheckEnv) != "" {
		return
	}
	if time.Since(loadUpdateCache().CheckedAt) < updateCheckEvery {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	startDetached(exec.Command(exe, "update-check"))
}

// cmdUpdateCheck is the detached helper behind kickUpdateCheck. It records
// whatever it managed to learn and stamps the cache either way, so an offline
// machine retries tomorrow instead of forking on every run. Silent by design:
// its console may be someone else's.
func cmdUpdateCheck() error {
	c := loadUpdateCache()
	c.CheckedAt = time.Now()
	if rel, err := latestRelease(); err == nil {
		c.CcswitchLatest = strings.TrimPrefix(rel.TagName, "v")
	}
	if v, err := npmLatestVersion(claudeLatestURL); err == nil {
		c.ClaudeLatest = v
	}
	if v, err := installedClaudeVersion(); err == nil {
		c.ClaudeHave = v
	}

	path, err := updateCachePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func npmLatestVersion(url string) (string, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "ccswitch-update-check")

	resp, err := httpClient(30 * time.Second).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("npm registry returned %s", resp.Status)
	}
	var v struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return "", err
	}
	if v.Version == "" {
		return "", fmt.Errorf("npm registry answer has no version")
	}
	return v.Version, nil
}

func installedClaudeVersion() (string, error) {
	bin, err := ClaudeBinary()
	if err != nil {
		return "", err
	}
	// Generous deadline: `claude --version` boots node, and a cold start on a
	// busy machine is slow. The helper is detached, so nobody is waiting.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := claudeCommand(ctx, bin, "--version").Output()
	if err != nil {
		return "", err
	}
	v := parseClaudeVersion(string(out))
	if v == "" {
		return "", fmt.Errorf("could not find a version in %q", strings.TrimSpace(string(out)))
	}
	return v, nil
}

// parseClaudeVersion pulls the version out of `claude --version` output —
// today "2.1.3 (Claude Code)", but any dotted number in any surrounding
// wording will do.
func parseClaudeVersion(out string) string {
	for _, f := range strings.Fields(out) {
		f = strings.Trim(strings.TrimPrefix(f, "v"), ",;()")
		if f == "" || f[0] < '0' || f[0] > '9' || !strings.Contains(f, ".") {
			continue
		}
		return f
	}
	return ""
}

// newerVersion reports whether a is a strictly newer dotted version than b.
// Anything it can't parse compares as not-newer: a nudge must never fire on
// garbage.
func newerVersion(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		av, bv := 0, 0
		var err error
		if i < len(as) {
			if av, err = strconv.Atoi(as[i]); err != nil {
				return false
			}
		}
		if i < len(bs) {
			if bv, err = strconv.Atoi(bs[i]); err != nil {
				return false
			}
		}
		if av != bv {
			return av > bv
		}
	}
	return false
}

// nudgesFor is the pure half of printUpdateNudges: one line per tool the
// cache says is behind. A tool whose installed version was never read stays
// silent — "you might be out of date" is not a nudge worth interrupting for.
func nudgesFor(c updateCache) []string {
	var out []string
	if newerVersion(c.CcswitchLatest, version) {
		out = append(out, fmt.Sprintf("ccswitch v%s is available (you have v%s) — run: ccswitch upgrade", c.CcswitchLatest, version))
	}
	if newerVersion(c.ClaudeLatest, c.ClaudeHave) {
		out = append(out, fmt.Sprintf("Claude Code v%s is available (you have v%s) — run: claude update", c.ClaudeLatest, c.ClaudeHave))
	}
	return out
}

// printUpdateNudges reports what the cache already knows, on stderr so it
// never lands in eval'd or piped output.
func printUpdateNudges() {
	if os.Getenv(noUpdateCheckEnv) != "" {
		return
	}
	for _, line := range nudgesFor(loadUpdateCache()) {
		fmt.Fprintln(os.Stderr, "ccswitch: "+line)
	}
}
