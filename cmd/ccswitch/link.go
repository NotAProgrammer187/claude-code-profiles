package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Which account a directory belongs to is usually obvious — work repos are work
// — but you still have to say it every time. A link binds a directory to a
// profile, and `ccswitch run` or `ccswitch use` with no profile name resolve it
// from the current directory upwards: the nearest binding wins, the way
// .gitignore or direnv treat a tree. Subdirectories inherit, so one link at the
// top of ~/work covers everything under it.
//
// Links live in ~/.ccswitch/state.json — on this machine, outside any repo. A
// directory can also name its profile in a `.ccswitch` file, which keeps the
// choice with the project instead of in a registry; a real link at the same
// level takes precedence over the file.
const linkFile = ".ccswitch"

// Link is one directory-to-profile binding.
type Link struct {
	Dir     string
	Profile string
}

// SetLink binds dir (default: the current directory) to an existing profile.
func SetLink(dir, profile string) (Link, error) {
	p, err := Find(profile)
	if err != nil {
		return Link{}, err
	}
	abs, err := absDir(dir)
	if err != nil {
		return Link{}, err
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		return Link{}, fmt.Errorf("not a directory: %s", abs)
	}

	st := loadState()
	if st.Links == nil {
		st.Links = map[string]string{}
	}
	// Replace any binding for the same directory, whatever case it was
	// written in — on Windows two spellings of one path are one directory.
	for k := range st.Links {
		if sameDir(k, abs) {
			delete(st.Links, k)
		}
	}
	st.Links[abs] = p.Name
	if err := st.save(); err != nil {
		return Link{}, err
	}
	return Link{Dir: abs, Profile: p.Name}, nil
}

// RemoveLink drops the binding for exactly this directory (not an ancestor's).
func RemoveLink(dir string) (Link, error) {
	abs, err := absDir(dir)
	if err != nil {
		return Link{}, err
	}
	st := loadState()
	for k, v := range st.Links {
		if sameDir(k, abs) {
			delete(st.Links, k)
			if err := st.save(); err != nil {
				return Link{}, err
			}
			return Link{Dir: k, Profile: v}, nil
		}
	}
	return Link{}, fmt.Errorf("%s isn't linked to a profile", abs)
}

// LinkList returns every binding, sorted by directory.
func LinkList() []Link {
	st := loadState()
	out := make([]Link, 0, len(st.Links))
	for dir, profile := range st.Links {
		out = append(out, Link{Dir: dir, Profile: profile})
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Dir) < strings.ToLower(out[j].Dir)
	})
	return out
}

// dropLinksTo forgets bindings that point at a profile that no longer exists,
// so deleting a profile doesn't leave directories resolving to nothing.
func dropLinksTo(profile string) {
	st := loadState()
	changed := false
	for dir, name := range st.Links {
		if strings.EqualFold(name, profile) {
			delete(st.Links, dir)
			changed = true
		}
	}
	if changed {
		_ = st.save()
	}
}

// renameLinksTo follows a profile rename, so links keep working.
func renameLinksTo(old, newName string) {
	st := loadState()
	changed := false
	for dir, name := range st.Links {
		if strings.EqualFold(name, old) {
			st.Links[dir] = newName
			changed = true
		}
	}
	if changed {
		_ = st.save()
	}
}

// LinkedProfile resolves dir to a profile by walking up the tree. It returns
// the profile name and the path that supplied it, so callers can say where the
// answer came from. The name is not checked against the profiles that exist —
// callers report that themselves, with better context than we have here.
func LinkedProfile(dir string) (name, source string, ok bool) {
	abs, err := absDir(dir)
	if err != nil {
		return "", "", false
	}
	st := loadState()
	for {
		for k, v := range st.Links {
			if sameDir(k, abs) {
				return v, k, true
			}
		}
		if n := readLinkFile(abs); n != "" {
			return n, filepath.Join(abs, linkFile), true
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", "", false
		}
		abs = parent
	}
}

// CurrentDirProfile resolves the working directory.
func CurrentDirProfile() (name, source string, ok bool) {
	wd, err := os.Getwd()
	if err != nil {
		return "", "", false
	}
	return LinkedProfile(wd)
}

// readLinkFile reads a directory's .ccswitch file: the first line that isn't
// blank or a comment names the profile. An unusable file is ignored rather than
// treated as an error — it might not be ours.
func readLinkFile(dir string) string {
	f, err := os.Open(filepath.Join(dir, linkFile))
	if err != nil {
		return ""
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if ValidName(line) != nil {
			return ""
		}
		return line
	}
	return ""
}

func absDir(dir string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		return filepath.Clean(wd), nil
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

// ---------------------------------------------------------------------------
// Commands
// ---------------------------------------------------------------------------

func cmdLink(args []string) error {
	var profile, dir string
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("unknown flag %q (usage: ccswitch link <profile> [directory])", a)
		case profile == "":
			profile = a
		case dir == "":
			dir = a
		default:
			return fmt.Errorf("unexpected argument %q", a)
		}
	}
	if profile == "" {
		return fmt.Errorf("usage: ccswitch link <profile> [directory]")
	}

	l, err := SetLink(dir, profile)
	if err != nil {
		return err
	}
	fmt.Printf("Linked %s to %s.\nRunning ccswitch here — or anywhere under it — now uses %s.\n",
		l.Dir, l.Profile, l.Profile)
	return nil
}

func cmdUnlink(args []string) error {
	var dir string
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("unknown flag %q (usage: ccswitch unlink [directory])", a)
		case dir == "":
			dir = a
		default:
			return fmt.Errorf("unexpected argument %q", a)
		}
	}

	l, err := RemoveLink(dir)
	if err != nil {
		return err
	}
	fmt.Printf("Unlinked %s (was %s).\n", l.Dir, l.Profile)
	return nil
}

func cmdLinks() error {
	links := LinkList()
	if len(links) == 0 {
		fmt.Println("no linked directories — try: ccswitch link <profile>")
		return nil
	}

	ps, err := List()
	if err != nil {
		return err
	}
	exists := map[string]bool{}
	for _, p := range ps {
		exists[strings.ToLower(p.Name)] = true
	}

	for _, l := range links {
		note := ""
		if !exists[strings.ToLower(l.Profile)] {
			note = "  (no such profile)"
		}
		fmt.Printf("%-14s %s%s\n", l.Profile, l.Dir, note)
	}

	// A .ccswitch file wins for its own directory but isn't listed above, so
	// say when the directory you're standing in is resolved by one.
	if name, source, ok := CurrentDirProfile(); ok && filepath.Base(source) == linkFile {
		fmt.Printf("\nthis directory resolves to %s via %s\n", name, source)
	}
	return nil
}
