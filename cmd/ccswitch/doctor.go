package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// ccswitch doctor answers "why isn't this working?" in one pass: the claude
// binary, the environment, our own files, every profile's config, and the
// directory links — each as one line saying it's fine or what to do about it.
// It is strictly read-only. Nothing is repaired, moved aside or rewritten,
// because half the point of a diagnostic is that you can run it while deciding
// what to do; even state.json is read directly here rather than through
// loadState, which quarantines a corrupt file as a side effect.

// checkup collects the verdicts. Problems are things that break a command
// (exit code 1); warnings are things that will bite later; notes are context.
type checkup struct {
	problems, warnings int
}

func (c *checkup) ok(what, detail string)   { printCheck("ok", what, detail) }
func (c *checkup) note(what, detail string) { printCheck("--", what, detail) }
func (c *checkup) warn(what, detail string) {
	c.warnings++
	printCheck("warn", what, detail)
}
func (c *checkup) fail(what, detail string) {
	c.problems++
	printCheck("FAIL", what, detail)
}

func printCheck(mark, what, detail string) {
	fmt.Printf("  %-5s %s — %s\n", mark, what, detail)
}

func cmdDoctor(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("usage: ccswitch doctor")
	}
	kickUpdateCheck()
	fmt.Printf("ccswitch %s on %s/%s\n\n", version, runtime.GOOS, runtime.GOARCH)

	c := &checkup{}

	// List first: the environment and link checks both need to know which
	// profiles exist. If the profiles directory itself can't be read there is
	// nothing meaningful to check against, so say so and stop.
	ps, listErr := List()
	if listErr != nil {
		c.fail("profiles", listErr.Error())
	}

	checkBinary(c)
	checkClaude(c)
	checkEnv(c, ps)
	checkRoot(c)
	checkState(c)
	checkDefaultsFile(c, ps)
	if listErr == nil {
		checkProfiles(c, ps)
		checkLinks(c, ps)
	}

	fmt.Println()
	switch {
	case c.problems == 0 && c.warnings == 0:
		fmt.Println("Everything looks fine.")
	case c.problems == 0:
		fmt.Printf("%s — nothing broken, but worth a look.\n", plural(c.warnings, "warning"))
	default:
		fmt.Printf("%s, %s.\n", plural(c.problems, "problem"), plural(c.warnings, "warning"))
	}
	printUpdateNudges()

	if c.problems > 0 {
		return fmt.Errorf("found %s", plural(c.problems, "problem"))
	}
	return nil
}

// checkBinary reports which ccswitch is running, who owns it, and whether the
// name `ccswitch` on PATH would start a different copy — the classic two-install
// drift where you upgrade one and keep running the other.
func checkBinary(c *checkup) {
	self, err := selfPath()
	if err != nil {
		c.warn("binary", err.Error())
		return
	}
	if name, hint := managedInstaller(self, scoopRootsFromEnv()); name != "" {
		c.ok("binary", fmt.Sprintf("%s — installed by %s (update with: %s)", shortPath(self), name, hint))
	} else {
		c.ok("binary", shortPath(self))
	}

	onPath, err := exec.LookPath("ccswitch")
	if err != nil {
		c.note("PATH", "ccswitch itself isn't on PATH — you're running it by full path")
		return
	}
	resolved, err := filepath.EvalSymlinks(onPath)
	if err != nil {
		return
	}
	if !sameDir(resolved, self) && !isLauncherShim(resolved, scoopRootsFromEnv()) {
		c.warn("PATH", fmt.Sprintf("`ccswitch` resolves to %s, not the copy you're running — two installs drift apart", shortPath(resolved)))
	}
}

// isLauncherShim reports whether path is a package manager's launcher rather
// than the binary itself. A Scoop shim is a real .exe that execs the app from
// apps/<name>/current, so it never EvalSymlinks-resolves to the running binary
// — comparing them raw would warn on every healthy Scoop install.
func isLauncherShim(path string, scoopRoots []string) bool {
	p := normInstallPath(path)
	if strings.Contains(p, "/scoop/shims/") {
		return true
	}
	for _, r := range scoopRoots {
		if r = normInstallPath(r); r != "" && strings.HasPrefix(p, r+"/shims/") {
			return true
		}
	}
	return false
}

func checkClaude(c *checkup) {
	bin, err := ClaudeBinary()
	if err != nil {
		c.fail("claude", err.Error())
		return
	}
	c.ok("claude", shortPath(bin))
}

func checkEnv(c *checkup, ps []Profile) {
	clean := true
	if k := ApiKeyInEnv(); k != "" {
		clean = false
		c.warn("environment", k+" is set — ccswitch strips it at launch, but a plain `claude` will bill the key instead of using your login")
	}
	if dir := ActiveConfigDir(); dir != "" {
		clean = false
		if name := ActiveProfileName(ps); name != "" {
			c.ok("environment", "this shell is pinned to "+name+" (CLAUDE_CONFIG_DIR)")
		} else {
			c.warn("environment", fmt.Sprintf("CLAUDE_CONFIG_DIR points at %s — not a ccswitch profile, so `claude` here uses none of them", shortPath(dir)))
		}
	}
	if clean {
		c.ok("environment", "nothing overriding")
	}
}

func checkRoot(c *checkup) {
	root, err := Root()
	if err != nil {
		c.fail("storage", err.Error())
		return
	}
	if m := cloudSyncMarker(root); m != "" {
		c.warn("storage", fmt.Sprintf("%s sits inside %s — profiles hold live OAuth tokens and shouldn't sync to cloud storage", shortPath(root), m))
		return
	}
	c.ok("storage", shortPath(root))
}

// cloudSyncMarker names the sync service a path lives under, or "" for a local
// one. "Mobile Documents" is where macOS keeps iCloud Drive.
func cloudSyncMarker(path string) string {
	p := strings.ToLower(filepath.ToSlash(path))
	for _, m := range []struct{ needle, name string }{
		{"onedrive", "OneDrive"},
		{"dropbox", "Dropbox"},
		{"google drive", "Google Drive"},
		{"googledrive", "Google Drive"},
		{"mobile documents", "iCloud Drive"},
	} {
		if strings.Contains(p, m.needle) {
			return m.name
		}
	}
	return ""
}

func checkState(c *checkup) {
	path, err := statePath()
	if err != nil {
		c.fail("state", err.Error())
		return
	}
	b, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		c.ok("state", "none yet")
	case err != nil:
		c.fail("state", fmt.Sprintf("%s is unreadable — %v", shortPath(path), err))
	case json.Unmarshal(b, &state{}) != nil:
		c.fail("state", fmt.Sprintf("%s does not parse — the next ccswitch command will move it aside as state.json.corrupt", shortPath(path)))
	default:
		c.ok("state", shortPath(path))
	}

	if _, err := os.Stat(path + ".corrupt"); err == nil {
		c.warn("state", fmt.Sprintf("%s.corrupt exists — an earlier state file was unreadable; recover your links from it or delete it", shortPath(path)))
	}
}

func checkDefaultsFile(c *checkup, ps []Profile) {
	shared, err := loadDefaults()
	if err != nil {
		c.fail("defaults", err.Error())
		return
	}
	if len(shared) == 0 {
		return // opt-in and unset — nothing to check, nothing to say
	}
	stale := 0
	for _, p := range ps {
		// An unreadable settings.json is reported per profile below; here it
		// just can't tell us about drift.
		if drift, err := defaultsDrift(shared, p); err == nil && len(drift) > 0 {
			stale++
		}
	}
	if stale > 0 {
		c.note("defaults", fmt.Sprintf("%s shared, %s out of step — ccswitch defaults apply (or just launch them)", plural(len(shared), "key"), plural(stale, "profile")))
		return
	}
	c.ok("defaults", fmt.Sprintf("%s shared, every profile up to date", plural(len(shared), "key")))
}

func checkProfiles(c *checkup, ps []Profile) {
	if len(ps) == 0 {
		c.note("profiles", "none yet — run ccswitch and press i to import your current login")
		return
	}
	for _, p := range ps {
		checkProfile(c, p)
	}
	checkDuplicateLogins(c, ps)
}

func checkProfile(c *checkup, p Profile) {
	broken := false

	// The two JSON files Claude Code itself reads on startup. A parse error in
	// either breaks that profile's sessions, not just our display of them.
	for _, f := range []string{settingsFile, ".claude.json"} {
		b, err := readFileIfPresent(filepath.Join(p.Dir, f))
		switch {
		case err != nil:
			broken = true
			c.fail(p.Name, fmt.Sprintf("%s is unreadable — %v", f, err))
		case len(b) > 0 && !json.Valid(b):
			broken = true
			c.fail(p.Name, fmt.Sprintf("%s does not parse — fix it by hand; ccswitch won't touch a file it can't read", f))
		}
	}
	if _, err := readCredentials(p.Dir); err != nil && !os.IsNotExist(err) {
		broken = true
		c.fail(p.Name, fmt.Sprintf(".credentials.json is unreadable — sign in again with: ccswitch run %s", p.Name))
	}
	if broken {
		return
	}

	if !p.SignedIn {
		c.ok(p.Name, fmt.Sprintf("not signed in yet — sign in with: ccswitch run %s", p.Name))
		return
	}
	status, _ := p.Status()
	c.ok(p.Name, p.Label()+", "+status)
}

// checkDuplicateLogins flags two profiles signed in to the same account. It
// works, but the isolation is an illusion: they share one set of rate limits,
// and `run --best` scores the same headroom twice.
func checkDuplicateLogins(c *checkup, ps []Profile) {
	byEmail := map[string][]string{}
	for _, p := range ps {
		if p.SignedIn && p.Email != "" {
			k := strings.ToLower(p.Email)
			byEmail[k] = append(byEmail[k], p.Name)
		}
	}
	emails := make([]string, 0, len(byEmail))
	for e, names := range byEmail {
		if len(names) > 1 {
			emails = append(emails, e)
		}
	}
	sort.Strings(emails)
	for _, e := range emails {
		c.warn("accounts", fmt.Sprintf("%s share one login (%s) — one set of rate limits, and --best counts it twice", strings.Join(byEmail[e], " and "), e))
	}
}

func checkLinks(c *checkup, ps []Profile) {
	links := LinkList()
	if len(links) == 0 {
		return
	}
	exists := map[string]bool{}
	for _, p := range ps {
		exists[strings.ToLower(p.Name)] = true
	}
	good := 0
	for _, l := range links {
		switch {
		case !exists[strings.ToLower(l.Profile)]:
			c.warn("links", fmt.Sprintf("%s points at %q, which doesn't exist — relink it, or drop it with: ccswitch unlink %q", shortPath(l.Dir), l.Profile, l.Dir))
		case !dirExists(l.Dir):
			c.warn("links", fmt.Sprintf("%s no longer exists (was linked to %s) — drop it with: ccswitch unlink %q", shortPath(l.Dir), l.Profile, l.Dir))
		default:
			good++
		}
	}
	if good == len(links) {
		detail := fmt.Sprintf("%d linked directories, all resolve", good)
		if good == 1 {
			detail = "1 linked directory, resolves fine"
		}
		c.ok("links", detail)
	}
}

func dirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}
