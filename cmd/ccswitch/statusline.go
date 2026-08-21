package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Claude Code's statusLine setting runs a command on every update and shows
// its first stdout line at the bottom of the session. ccswitch already knows
// which account a session is — it set CLAUDE_CONFIG_DIR — and how to read the
// rate-limit windows, so `ccswitch statusline` puts both where you're already
// looking:
//
//   work · Opus 4.5 · 5h 42% · wk 12%
//
// The command runs often and must never block the status line on the network,
// so the numbers come from a small cache: statusline itself only reads it, and
// when an entry has gone stale it kicks a detached refresh (the same pattern
// as the update check) whose result shows up a tick or two later. First ever
// call shows just the profile; usage joins it once the first refresh lands.

const (
	// statuslineStale is how old a cache entry may get before a refresh is
	// kicked. The endpoint is undocumented and shared with Claude Code itself;
	// a status line doesn't need fresher numbers than the picker shows.
	statuslineStale = 2 * time.Minute
	// statuslineKickEvery bounds how often a refresh may be spawned while one
	// is already in flight — statusline can fire many times a minute, and each
	// stale render would otherwise fork another fetch.
	statuslineKickEvery = 30 * time.Second
)

// usageCacheEntry is one config directory's last-known usage. A nil Usage
// with a recent FetchedAt means the last fetch failed and shouldn't be
// retried until the entry is stale again.
type usageCacheEntry struct {
	FetchedAt time.Time `json:"fetched_at"`
	KickedAt  time.Time `json:"kicked_at,omitempty"`
	Usage     *Usage    `json:"usage,omitempty"`
}

func usageCachePath() (string, error) {
	root, err := Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "usage-cache.json"), nil
}

// loadUsageCache reads the cache, keyed by lower-cased config directory —
// the one identifier that exists whether or not the session is a ccswitch
// profile. A missing or mangled file is an empty cache; it's regenerable.
func loadUsageCache() map[string]usageCacheEntry {
	out := map[string]usageCacheEntry{}
	p, err := usageCachePath()
	if err != nil {
		return out
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return out
	}
	_ = json.Unmarshal(b, &out)
	if out == nil {
		out = map[string]usageCacheEntry{}
	}
	return out
}

func saveUsageCache(c map[string]usageCacheEntry) error {
	p, err := usageCachePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	// Write-then-rename: statusline and its refresher run concurrently, and a
	// torn write would show up as a mangled (silently dropped) cache.
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func usageCacheKey(dir string) string {
	return strings.ToLower(filepath.Clean(dir))
}

// statuslineTarget resolves which account this status line describes: the
// ccswitch profile the session's CLAUDE_CONFIG_DIR points at, a foreign
// config directory by its base name, or the default ~/.claude for a session
// launched without ccswitch at all.
func statuslineTarget() (label, dir string) {
	if d := ActiveConfigDir(); d != "" {
		if n := profileNameForDir(d); n != "" {
			return n, d
		}
		return filepath.Base(d), d
	}
	d, err := DefaultClaudeDir()
	if err != nil {
		return "claude", ""
	}
	return "claude", d
}

// profileNameForDir matches a config directory against the profiles by name
// alone. Unlike List it reads no per-profile metadata — .claude.json can run
// to megabytes, and this runs on every status line tick.
func profileNameForDir(dir string) string {
	base, err := profilesDir()
	if err != nil {
		return ""
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") &&
			sameDir(filepath.Join(base, e.Name()), dir) {
			return e.Name()
		}
	}
	return ""
}

// statuslineInput is the JSON Claude Code pipes to the statusLine command.
// Only the model name is read; everything else the session shows already.
type statuslineInput struct {
	Model struct {
		DisplayName string `json:"display_name"`
	} `json:"model"`
}

// readStatuslineModel parses the piped session JSON, and reads nothing at all
// on an interactive terminal — someone trying the command by hand must not
// find it hanging on stdin.
func readStatuslineModel(r io.Reader) string {
	var in statuslineInput
	data, err := io.ReadAll(io.LimitReader(r, 256*1024))
	if err != nil || json.Unmarshal(data, &in) != nil {
		return ""
	}
	return strings.TrimSpace(in.Model.DisplayName)
}

// ANSI colours, written directly rather than through lipgloss: the status
// line is not our terminal, and Claude Code renders the escape codes as-is.
const (
	ansiReset  = "\x1b[0m"
	ansiDim    = "\x1b[2m"
	ansiYellow = "\x1b[33m"
	ansiRed    = "\x1b[31m"
)

// statuslineText renders the one line: label, model if known, then a part per
// rate-limit window — amber from 70%, red from 90%, the thresholds every
// other view uses. Nil usage (not fetched yet, or the fetch failed) renders
// what's left; the line degrades, it never errors.
func statuslineText(label, model string, u *Usage) string {
	parts := []string{label}
	if model != "" {
		parts = append(parts, model)
	}
	if u != nil {
		if w := u.Session; w != nil {
			parts = append(parts, statuslineWindow("5h", w))
		}
		if w := u.Week; w != nil {
			parts = append(parts, statuslineWindow("wk", w))
		}
		if w := u.Opus; w != nil && w.Utilization > 0 {
			parts = append(parts, statuslineWindow("opus", w))
		}
	}
	return strings.Join(parts, ansiDim+" · "+ansiReset)
}

func statuslineWindow(label string, w *UsageWindow) string {
	s := fmt.Sprintf("%s %d%%", label, pct(w.Utilization))
	switch {
	case w.Utilization >= 90:
		s = ansiRed + s + ansiReset
		if !w.ResetsAt.IsZero() {
			s += ansiDim + " resets " + resetClock(w.ResetsAt) + ansiReset
		}
	case w.Utilization >= 70:
		s = ansiYellow + s + ansiReset
	}
	return s
}

// cmdStatusline prints the line and exits. All it may do synchronously is
// read files; anything slower happens in the detached refresher.
func cmdStatusline(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("statusline takes no arguments — point Claude Code's statusLine setting at it (see README)")
	}

	model := ""
	if fi, err := os.Stdin.Stat(); err == nil && fi.Mode()&os.ModeCharDevice == 0 {
		model = readStatuslineModel(os.Stdin)
	}

	label, dir := statuslineTarget()
	var u *Usage
	if dir != "" {
		key := usageCacheKey(dir)
		cache := loadUsageCache()
		entry := cache[key]
		u = entry.Usage
		if time.Since(entry.FetchedAt) > statuslineStale &&
			time.Since(entry.KickedAt) > statuslineKickEvery {
			// Stamp before spawning, so a burst of statusline calls in the
			// same stale window forks one refresher, not one each.
			entry.KickedAt = time.Now()
			cache[key] = entry
			_ = saveUsageCache(cache)
			if exe, err := os.Executable(); err == nil {
				startDetached(exec.Command(exe, "statusline-refresh", dir))
			}
		}
	}

	fmt.Println(statuslineText(label, model, u))
	return nil
}

// cmdStatuslineRefresh is the detached helper behind cmdStatusline: fetch one
// directory's usage, record whatever happened, exit. A failed fetch is
// recorded too — with a fresh FetchedAt and no usage — so the status line
// shows less rather than refetching on every render. Silent by design: its
// console is the session's.
func cmdStatuslineRefresh(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: ccswitch statusline-refresh <config-dir>")
	}
	dir := args[0]
	u, _ := FetchUsage(Profile{Dir: dir})

	key := usageCacheKey(dir)
	cache := loadUsageCache()
	entry := cache[key]
	entry.FetchedAt = time.Now()
	entry.KickedAt = time.Time{}
	entry.Usage = u
	cache[key] = entry
	return saveUsageCache(cache)
}
