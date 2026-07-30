package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Some Claude Code preferences are about you, not about an account: whether
// Remote Control starts with every session, which theme you read, whether
// sessions upload. Profiles are isolated, so each one keeps its own
// settings.json — and a preference you turned off in one profile is back on
// the first time you sign in to another, because an absent key means "default".
// Turning it off again in every profile, every time you add one, is the bit
// that doesn't scale.
//
// Shared defaults are the fix: ~/.ccswitch/settings.json holds the keys you
// want to be the same everywhere, and ccswitch writes them into a profile's
// settings.json whenever it launches or pins that profile — including the very
// first launch of a brand-new profile, before Claude Code's login flow runs.
//
// The shared file is authoritative for the keys it names, and only those keys.
// Everything else in a profile's settings.json is left exactly as it is, so a
// per-profile model or MCP permission survives; a key you share stops being a
// per-profile choice, which is the point of sharing it.
const (
	settingsFile = "settings.json"
	defaultsFile = settingsFile // inside ~/.ccswitch, alongside state.json
)

func defaultsPath() (string, error) {
	root, err := Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, defaultsFile), nil
}

// loadDefaults reads the shared settings. A missing file is not an error — the
// feature is opt-in and empty until you set something.
func loadDefaults() (map[string]json.RawMessage, error) {
	p, err := defaultsPath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, nil
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	return m, nil
}

func saveDefaults(m map[string]json.RawMessage) error {
	p, err := defaultsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	if len(m) == 0 {
		// Nothing shared: remove the file rather than leave an empty object
		// behind, so `ccswitch defaults` reads as genuinely unset.
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(b, '\n'), 0o600)
}

// mergeDefaults applies the shared keys to one profile's settings.json body and
// returns the new bytes plus the keys it changed. A nil result means the profile
// already agrees and nothing should be written.
func mergeDefaults(shared map[string]json.RawMessage, settings []byte) ([]byte, []string, error) {
	if len(shared) == 0 {
		return nil, nil, nil
	}
	top := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(settings)) > 0 {
		if err := json.Unmarshal(settings, &top); err != nil {
			return nil, nil, err
		}
	}

	var changed []string
	for k, v := range shared {
		if cur, ok := top[k]; ok && jsonEqual(cur, v) {
			continue
		}
		top[k] = v
		changed = append(changed, k)
	}
	if len(changed) == 0 {
		return nil, nil, nil
	}
	sort.Strings(changed)

	out, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return append(out, '\n'), changed, nil
}

// ApplyDefaults writes the shared settings into one profile and returns the keys
// it actually changed — empty when the profile was already in step, which is the
// normal case after the first time.
func ApplyDefaults(p Profile) ([]string, error) {
	shared, err := loadDefaults()
	if err != nil || len(shared) == 0 {
		return nil, err
	}

	path := filepath.Join(p.Dir, settingsFile)
	out, changed, err := mergeDefaults(shared, readFileOrEmpty(path))
	if err != nil {
		// A settings.json we can't parse is someone's hand-edit in progress:
		// say so and change nothing, rather than replace it with our own idea
		// of what it should contain.
		return nil, fmt.Errorf("%s: %s: %w", p.Name, settingsFile, err)
	}
	if len(changed) == 0 {
		return nil, nil
	}
	if err := os.MkdirAll(p.Dir, 0o700); err != nil {
		return nil, err
	}
	return changed, os.WriteFile(path, out, 0o600)
}

// defaultsDrift reports which shared keys a profile doesn't match yet, without
// writing anything.
func defaultsDrift(shared map[string]json.RawMessage, p Profile) ([]string, error) {
	_, changed, err := mergeDefaults(shared, readFileOrEmpty(filepath.Join(p.Dir, settingsFile)))
	return changed, err
}

// applyDefaultsOnLaunch is the hook every launch and pin goes through. It never
// fails the operation: you asked to run Claude Code, not to manage settings, so
// a problem here is a warning on stderr and nothing more. stderr matters — the
// stdout of `ccswitch use` is eval'd by your shell.
func applyDefaultsOnLaunch(p Profile) {
	changed, err := ApplyDefaults(p)
	switch {
	case err != nil:
		fmt.Fprintf(os.Stderr, "ccswitch: shared settings not applied — %v\n", err)
	case len(changed) > 0:
		fmt.Fprintf(os.Stderr, "ccswitch: applied %s to %s (%s)\n",
			plural(len(changed), "shared setting"), p.Name, strings.Join(changed, ", "))
	}
}

// defaultsNoticeOnLaunch is the picker's version of the same hook. It can't
// write to stderr — the alt screen still owns the terminal — so it hands back a
// line for the notice bar, empty when there is nothing to say. Like the CLI
// hook, a problem never stops the launch.
func defaultsNoticeOnLaunch(p Profile) string {
	changed, err := ApplyDefaults(p)
	switch {
	case err != nil:
		return "Shared settings not applied — " + err.Error()
	case len(changed) > 0:
		return "Applied " + plural(len(changed), "shared setting") + " to " + p.Name
	}
	return ""
}

// ---------------------------------------------------------------------------
// Commands
// ---------------------------------------------------------------------------

// Keys are top-level settings.json names. Nesting isn't addressable — share a
// whole block instead, by giving its JSON as the value.
var defaultsKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

func cmdDefaults(args []string) error {
	if len(args) == 0 {
		return showDefaults()
	}
	switch strings.ToLower(args[0]) {
	case "set":
		return defaultsSet(args[1:])
	case "unset", "rm", "remove":
		return defaultsUnset(args[1:])
	case "apply":
		return defaultsApply(args[1:])
	default:
		return fmt.Errorf("unknown subcommand %q (try: ccswitch defaults [set <key> <value> | unset <key> | apply])", args[0])
	}
}

func showDefaults() error {
	path, err := defaultsPath()
	if err != nil {
		return err
	}
	shared, err := loadDefaults()
	if err != nil {
		return err
	}
	if len(shared) == 0 {
		fmt.Printf("No shared settings yet (%s).\n\n"+
			"Share one and every profile gets it, now and on first login:\n"+
			"  ccswitch defaults set remoteControlAtStartup false\n", path)
		return nil
	}

	fmt.Printf("Shared with every profile (%s)\n\n", path)
	for _, k := range sortedKeys(shared) {
		fmt.Printf("  %-28s %s\n", k, compactJSON(shared[k]))
	}

	ps, err := List()
	if err != nil {
		return err
	}
	if len(ps) == 0 {
		return nil
	}
	fmt.Println()
	stale := 0
	for _, p := range ps {
		drift, err := defaultsDrift(shared, p)
		switch {
		case err != nil:
			fmt.Printf("  %-18s unreadable %s — %v\n", p.Name, settingsFile, err)
		case len(drift) == 0:
			fmt.Printf("  %-18s up to date\n", p.Name)
		default:
			stale++
			fmt.Printf("  %-18s %s\n", p.Name, strings.Join(drift, ", "))
		}
	}
	if stale > 0 {
		fmt.Printf("\n%s out of step — write them now with: ccswitch defaults apply\n",
			plural(stale, "profile"))
	}
	return nil
}

func defaultsSet(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: ccswitch defaults set <key> <value>\n" +
			"  e.g. ccswitch defaults set remoteControlAtStartup false")
	}
	key := args[0]
	if !defaultsKeyRe.MatchString(key) {
		return fmt.Errorf("%q isn't a settings key — use a top-level name from settings.json", key)
	}
	// Join the rest so an unquoted JSON object still arrives in one piece.
	raw := parseValue(strings.Join(args[1:], " "))

	shared, err := loadDefaults()
	if err != nil {
		return err
	}
	if shared == nil {
		shared = map[string]json.RawMessage{}
	}
	if cur, ok := shared[key]; ok && jsonEqual(cur, raw) {
		fmt.Printf("%s is already shared as %s.\n", key, compactJSON(raw))
		return nil
	}
	shared[key] = raw
	if err := saveDefaults(shared); err != nil {
		return err
	}
	fmt.Printf("Sharing %s = %s with every profile.\n", key, compactJSON(raw))
	return applyToAll(nil)
}

func defaultsUnset(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: ccswitch defaults unset <key>")
	}
	key := args[0]
	shared, err := loadDefaults()
	if err != nil {
		return err
	}
	if _, ok := shared[key]; !ok {
		return fmt.Errorf("%q isn't shared (see: ccswitch defaults)", key)
	}
	delete(shared, key)
	if err := saveDefaults(shared); err != nil {
		return err
	}
	// Deliberately not reverted anywhere: each profile keeps the value it has
	// and is free to change it again. Unsharing is about future writes.
	fmt.Printf("Stopped sharing %s. Profiles keep their current value.\n", key)
	return nil
}

func defaultsApply(args []string) error {
	shared, err := loadDefaults()
	if err != nil {
		return err
	}
	if len(shared) == 0 {
		fmt.Println("Nothing shared yet — try: ccswitch defaults set remoteControlAtStartup false")
		return nil
	}
	return applyToAll(args)
}

// applyToAll writes the shared settings into the named profiles, or every
// profile when names is empty.
func applyToAll(names []string) error {
	var targets []Profile
	if len(names) == 0 {
		ps, err := List()
		if err != nil {
			return err
		}
		targets = ps
	} else {
		for _, n := range names {
			if strings.HasPrefix(n, "-") {
				return fmt.Errorf("unknown flag %q (usage: ccswitch defaults apply [profile...])", n)
			}
			p, err := Find(n)
			if err != nil {
				return err
			}
			targets = append(targets, p)
		}
	}
	if len(targets) == 0 {
		fmt.Println("No profiles yet — the next one you create picks these up on its first launch.")
		return nil
	}

	written, failed := 0, 0
	for _, p := range targets {
		changed, err := ApplyDefaults(p)
		switch {
		case err != nil:
			failed++
			fmt.Printf("  %-18s skipped — %v\n", p.Name, err)
		case len(changed) > 0:
			written++
			fmt.Printf("  %-18s %s\n", p.Name, strings.Join(changed, ", "))
		default:
			fmt.Printf("  %-18s up to date\n", p.Name)
		}
	}
	if written > 0 {
		fmt.Printf("Updated %s.\n", plural(written, "profile"))
	}
	if failed > 0 {
		return fmt.Errorf("%s could not be updated", plural(failed, "profile"))
	}
	return nil
}

// parseValue turns a command-line value into JSON. Anything that already parses
// as JSON is taken as written — so `false` is a boolean and `3` a number — and
// everything else is a string, which is what makes `set theme dark` work
// without shell quoting.
func parseValue(s string) json.RawMessage {
	t := strings.TrimSpace(s)
	if json.Valid([]byte(t)) && t != "" {
		var v any
		if json.Unmarshal([]byte(t), &v) == nil {
			return json.RawMessage(t)
		}
	}
	b, err := json.Marshal(s)
	if err != nil { // unreachable for a string
		return json.RawMessage(`""`)
	}
	return json.RawMessage(b)
}

// compactJSON renders a value on one line for display.
func compactJSON(raw json.RawMessage) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return string(raw)
	}
	return buf.String()
}

func sortedKeys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
