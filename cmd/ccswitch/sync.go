package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

// Profiles are isolated on purpose — that's what stops one login from
// clobbering another — but isolation also duplicates everything that is *not*
// account-specific: settings, CLAUDE.md, slash commands, subagents, skills and
// MCP servers. Without sync, a second account starts blank and you set all of
// it up again by hand.
//
// syncItems is an allowlist, which is the important safety property here:
// credentials, history, projects and per-machine caches are never candidates,
// so a sync cannot move a login between profiles.
type syncItem struct {
	Name string // token accepted by --only
	Path string // path inside the config dir; empty for the .claude.json merge
	Dir  bool
}

var syncItems = []syncItem{
	{Name: "settings", Path: settingsFile},
	{Name: "claude-md", Path: "CLAUDE.md"},
	{Name: "commands", Path: "commands", Dir: true},
	{Name: "agents", Path: "agents", Dir: true},
	{Name: "skills", Path: "skills", Dir: true},
	{Name: "output-styles", Path: "output-styles", Dir: true},
	{Name: "hooks", Path: "hooks", Dir: true},
	{Name: "mcp", Path: ""}, // merged into .claude.json, see mergeMCP
}

const claudeJSONFile = ".claude.json"

// label is how the item reads in the plan we print.
func (it syncItem) label() string {
	switch {
	case it.Name == "mcp":
		return "mcp servers"
	case it.Dir:
		return it.Path + "/"
	default:
		return it.Path
	}
}

func syncItemNames() string {
	names := make([]string, 0, len(syncItems))
	for _, it := range syncItems {
		names = append(names, it.Name)
	}
	return strings.Join(names, ", ")
}

// selectItems resolves --only. Empty (or "all") means every item.
func selectItems(only string) ([]syncItem, error) {
	only = strings.TrimSpace(only)
	if only == "" || only == "all" {
		return syncItems, nil
	}
	var out []syncItem
	for _, tok := range strings.Split(only, ",") {
		tok = strings.TrimSpace(strings.ToLower(tok))
		if tok == "" {
			continue
		}
		found := false
		for _, it := range syncItems {
			if it.Name == tok {
				out = append(out, it)
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("unknown item %q (available: %s)", tok, syncItemNames())
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("--only needs at least one item (available: %s)", syncItemNames())
	}
	return out, nil
}

// change is one item that would actually be written into one target.
type change struct {
	target Profile
	item   syncItem
	note   string // what the write does: "new", "replaced", "3 files", "2 servers"
}

func cmdSync(args []string) error {
	var from, to, only string
	dry, yes := false, false

	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func(flag string) (string, error) {
			i++
			if i >= len(args) {
				return "", fmt.Errorf("%s needs a value", flag)
			}
			return args[i], nil
		}
		var err error
		switch {
		case a == "--from":
			from, err = next("--from")
		case strings.HasPrefix(a, "--from="):
			from = strings.TrimPrefix(a, "--from=")
		case a == "--to":
			to, err = next("--to")
		case strings.HasPrefix(a, "--to="):
			to = strings.TrimPrefix(a, "--to=")
		case a == "--only":
			only, err = next("--only")
		case strings.HasPrefix(a, "--only="):
			only = strings.TrimPrefix(a, "--only=")
		case a == "--dry-run" || a == "-n":
			dry = true
		case a == "-y" || a == "--yes":
			yes = true
		case strings.HasPrefix(a, "-"):
			err = fmt.Errorf("unknown flag %q (try: ccswitch help)", a)
		case from == "":
			from = a
		default:
			err = fmt.Errorf("unexpected argument %q", a)
		}
		if err != nil {
			return err
		}
	}

	items, err := selectItems(only)
	if err != nil {
		return err
	}

	ps, err := List()
	if err != nil {
		return err
	}
	if len(ps) < 2 {
		return fmt.Errorf("sync needs at least two profiles (you have %d)", len(ps))
	}

	// No --from: the profile this shell is pinned to is the obvious source.
	if from == "" {
		from = ActiveProfileName(ps)
		if from == "" {
			return fmt.Errorf("which profile to copy from? try: ccswitch sync --from <profile>")
		}
	}
	src, err := Find(from)
	if err != nil {
		return err
	}

	targets, err := syncTargets(ps, src, to)
	if err != nil {
		return err
	}

	// Work out the whole plan before writing anything, so --dry-run and the
	// confirmation prompt show exactly what a real run would do.
	var changes []change
	var warnings []string
	for _, t := range targets {
		for _, it := range items {
			note, err := planItem(it, src, t)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("%s: %s — %v", t.Name, it.label(), err))
				continue
			}
			if note != "" {
				changes = append(changes, change{target: t, item: it, note: note})
			}
		}
	}

	printSyncPlan(src, targets, changes, warnings)
	if len(changes) == 0 {
		return nil
	}
	if dry {
		fmt.Println("\nDry run — nothing written.")
		return nil
	}
	if !yes {
		q := fmt.Sprintf("\nCopy this into %s? Same-named files are overwritten. [y/N] ", plural(len(targets), "profile"))
		if !confirm(q) {
			fmt.Println("Cancelled.")
			return nil
		}
	}

	written := 0
	for _, c := range changes {
		if err := applyItem(c.item, src, c.target); err != nil {
			return fmt.Errorf("%s: %s: %w", c.target.Name, c.item.label(), err)
		}
		written++
	}
	fmt.Printf("Synced %s into %s.\n", plural(written, "item"), plural(len(targets), "profile"))
	return nil
}

// syncTargets resolves --to, defaulting to every profile except the source.
func syncTargets(ps []Profile, src Profile, to string) ([]Profile, error) {
	if strings.TrimSpace(to) == "" || to == "all" {
		var out []Profile
		for _, p := range ps {
			if !strings.EqualFold(p.Name, src.Name) {
				out = append(out, p)
			}
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("no other profiles to sync into")
		}
		return out, nil
	}

	var out []Profile
	seen := map[string]bool{}
	for _, tok := range strings.Split(to, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		p, err := Find(tok)
		if err != nil {
			return nil, err
		}
		if strings.EqualFold(p.Name, src.Name) {
			return nil, fmt.Errorf("%s is the source — pick a different --to", p.Name)
		}
		if seen[p.Name] {
			continue
		}
		seen[p.Name] = true
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("--to needs at least one profile")
	}
	return out, nil
}

func printSyncPlan(src Profile, targets []Profile, changes []change, warnings []string) {
	names := make([]string, len(targets))
	for i, t := range targets {
		names[i] = t.Name
	}
	fmt.Printf("Sync %s → %s\n", src.Name, strings.Join(names, ", "))

	for _, t := range targets {
		var mine []change
		for _, c := range changes {
			if c.target.Name == t.Name {
				mine = append(mine, c)
			}
		}
		fmt.Printf("\n  %s\n", t.Name)
		if len(mine) == 0 {
			fmt.Println("    up to date")
			continue
		}
		for _, c := range mine {
			fmt.Printf("    %-16s %s\n", c.item.label(), c.note)
		}
	}

	for _, w := range warnings {
		fmt.Printf("\n  skipped %s", w)
	}
	if len(warnings) > 0 {
		fmt.Println()
	}
	if len(changes) == 0 {
		fmt.Println("\nNothing to copy.")
	}
}

// ---------------------------------------------------------------------------
// Planning and applying one item
// ---------------------------------------------------------------------------

// planItem reports what syncing one item into one profile would write, without
// touching disk. An empty note means there is nothing to do — the source has
// no such item, or the target already matches.
func planItem(it syncItem, src, dst Profile) (string, error) {
	if it.Name == "mcp" {
		_, n, err := mergeMCP(readFileOrEmpty(filepath.Join(src.Dir, claudeJSONFile)),
			readFileOrEmpty(filepath.Join(dst.Dir, claudeJSONFile)))
		if err != nil || n == 0 {
			return "", err
		}
		return plural(n, "server"), nil
	}

	s := filepath.Join(src.Dir, it.Path)
	d := filepath.Join(dst.Dir, it.Path)
	fi, err := os.Stat(s)
	if err != nil {
		return "", nil // source doesn't have it
	}

	if it.Dir {
		if !fi.IsDir() {
			return "", nil
		}
		n, err := countWrites(s, d)
		if err != nil || n == 0 {
			return "", err
		}
		return plural(n, "file"), nil
	}

	if fi.IsDir() {
		return "", nil
	}
	same, err := sameContents(s, d)
	if err != nil {
		return "", err
	}
	if same {
		return "", nil
	}
	if _, err := os.Stat(d); err == nil {
		return "replaced", nil
	}
	return "new", nil
}

// applyItem performs the copy. Directories are merged rather than mirrored —
// files the target has and the source doesn't are left alone, because deleting
// someone's per-account customisation is a worse failure than leaving it.
func applyItem(it syncItem, src, dst Profile) error {
	if it.Name == "mcp" {
		dstPath := filepath.Join(dst.Dir, claudeJSONFile)
		out, n, err := mergeMCP(readFileOrEmpty(filepath.Join(src.Dir, claudeJSONFile)),
			readFileOrEmpty(dstPath))
		if err != nil || n == 0 {
			return err
		}
		// 0600: .claude.json can hold an account's own identity block.
		return os.WriteFile(dstPath, out, 0o600)
	}

	s := filepath.Join(src.Dir, it.Path)
	d := filepath.Join(dst.Dir, it.Path)
	if it.Dir {
		return copyTree(s, d, nil)
	}
	if err := os.MkdirAll(filepath.Dir(d), 0o700); err != nil {
		return err
	}
	return copyFile(s, d)
}

// countWrites counts the regular files under src that are missing from dst or
// differ from it — i.e. how many files a sync would actually write.
func countWrites(src, dst string) (int, error) {
	entries, err := os.ReadDir(src)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		switch {
		case e.IsDir():
			sub, err := countWrites(s, d)
			if err != nil {
				return 0, err
			}
			n += sub
		case e.Type().IsRegular():
			same, err := sameContents(s, d)
			if err != nil {
				return 0, err
			}
			if !same {
				n++
			}
		}
	}
	return n, nil
}

// sameContents reports whether both paths hold identical bytes. A missing dst
// counts as different; these are small config files, so a byte compare is fine.
func sameContents(src, dst string) (bool, error) {
	a, err := os.ReadFile(src)
	if err != nil {
		return false, err
	}
	b, err := os.ReadFile(dst)
	if err != nil {
		return false, nil
	}
	return bytes.Equal(a, b), nil
}

func readFileOrEmpty(path string) []byte {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return b
}

// ---------------------------------------------------------------------------
// MCP servers
// ---------------------------------------------------------------------------

// mergeMCP merges the mcpServers block of src into dst and returns the new dst
// bytes plus the number of servers written. Every other key in dst — the
// account identity, project state, history — is carried across untouched,
// which is why we can't simply copy .claude.json between profiles.
//
// A zero count means dst already has every server src does, and nothing should
// be written.
func mergeMCP(src, dst []byte) ([]byte, int, error) {
	srcServers, err := mcpBlock(src)
	if err != nil {
		return nil, 0, fmt.Errorf("source %s: %w", claudeJSONFile, err)
	}
	if len(srcServers) == 0 {
		return nil, 0, nil
	}

	top := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(dst)) > 0 {
		if err := json.Unmarshal(dst, &top); err != nil {
			return nil, 0, fmt.Errorf("target %s: %w", claudeJSONFile, err)
		}
	}
	dstServers, err := mcpBlock(dst)
	if err != nil {
		return nil, 0, fmt.Errorf("target %s: %w", claudeJSONFile, err)
	}
	if dstServers == nil {
		dstServers = map[string]json.RawMessage{}
	}

	written := 0
	for name, cfg := range srcServers {
		if cur, ok := dstServers[name]; ok && jsonEqual(cur, cfg) {
			continue
		}
		dstServers[name] = cfg
		written++
	}
	if written == 0 {
		return nil, 0, nil
	}

	blob, err := json.Marshal(dstServers)
	if err != nil {
		return nil, 0, err
	}
	top["mcpServers"] = blob
	out, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return nil, 0, err
	}
	return append(out, '\n'), written, nil
}

// mcpBlock pulls the mcpServers object out of a .claude.json body. Absent or
// empty input is not an error — a profile that has never been launched has no
// file at all.
func mcpBlock(b []byte) (map[string]json.RawMessage, error) {
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, nil
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(b, &top); err != nil {
		return nil, err
	}
	raw, ok := top["mcpServers"]
	if !ok || len(bytes.TrimSpace(raw)) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var servers map[string]json.RawMessage
	if err := json.Unmarshal(raw, &servers); err != nil {
		return nil, err
	}
	return servers, nil
}

// jsonEqual compares two JSON values by structure, so key order and
// indentation differences don't read as a change.
func jsonEqual(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}
