package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Profile is one isolated Claude Code config directory.
type Profile struct {
	Name     string
	Dir      string
	Email    string
	Org      string
	Plan     string
	SignedIn bool
	Expires  time.Time
	LastUsed time.Time
}

// Label renders the account identity, falling back to the profile name.
func (p Profile) Label() string {
	if p.Email != "" {
		return p.Email
	}
	return "—"
}

// Status is a short human-readable auth state.
func (p Profile) Status() (string, StatusKind) {
	if !p.SignedIn {
		return "not signed in", StatusIdle
	}
	if p.Expires.IsZero() {
		return "signed in", StatusOK
	}
	if time.Now().After(p.Expires) {
		// Not fatal: Claude Code silently refreshes using the refresh token.
		return "will refresh", StatusWarn
	}
	return "signed in", StatusOK
}

type StatusKind int

const (
	StatusOK StatusKind = iota
	StatusWarn
	StatusIdle
)

// ---------------------------------------------------------------------------
// Paths
// ---------------------------------------------------------------------------

// Root is ~/.ccswitch — holds every profile plus our own state file.
func Root() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ccswitch"), nil
}

func profilesDir() (string, error) {
	root, err := Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "profiles"), nil
}

// DefaultClaudeDir is the config directory Claude Code uses when
// CLAUDE_CONFIG_DIR is unset.
func DefaultClaudeDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude"), nil
}

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,31}$`)

func ValidName(s string) error {
	if !nameRe.MatchString(s) {
		return fmt.Errorf("use 1-32 chars: letters, digits, dot, dash, underscore")
	}
	return nil
}

// ---------------------------------------------------------------------------
// State (last-used timestamps)
// ---------------------------------------------------------------------------

type state struct {
	LastUsed map[string]time.Time `json:"lastUsed"`
}

func statePath() (string, error) {
	root, err := Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "state.json"), nil
}

func loadState() state {
	st := state{LastUsed: map[string]time.Time{}}
	p, err := statePath()
	if err != nil {
		return st
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return st
	}
	_ = json.Unmarshal(b, &st)
	if st.LastUsed == nil {
		st.LastUsed = map[string]time.Time{}
	}
	return st
}

func (s state) save() error {
	p, err := statePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o600)
}

func TouchProfile(name string) {
	st := loadState()
	st.LastUsed[name] = time.Now()
	_ = st.save()
}

// ---------------------------------------------------------------------------
// Reading account metadata
// ---------------------------------------------------------------------------
//
// The two files below are Claude Code internals and are not a documented,
// stable API. We only read them to *display* who is signed in — everything
// still works if the shape changes, the row just shows less detail.

type claudeConfig struct {
	OAuthAccount struct {
		EmailAddress     string `json:"emailAddress"`
		OrganizationName string `json:"organizationName"`
	} `json:"oauthAccount"`
}

type credentials struct {
	ClaudeAiOauth struct {
		RefreshToken     string `json:"refreshToken"`
		ExpiresAt        int64  `json:"expiresAt"` // epoch millis
		SubscriptionType string `json:"subscriptionType"`
	} `json:"claudeAiOauth"`
}

func readMeta(p *Profile) {
	if b, err := os.ReadFile(filepath.Join(p.Dir, ".claude.json")); err == nil {
		var cc claudeConfig
		if json.Unmarshal(b, &cc) == nil {
			p.Email = cc.OAuthAccount.EmailAddress
			p.Org = cc.OAuthAccount.OrganizationName
		}
	}
	if b, err := os.ReadFile(filepath.Join(p.Dir, ".credentials.json")); err == nil {
		var cr credentials
		if json.Unmarshal(b, &cr) == nil {
			o := cr.ClaudeAiOauth
			p.SignedIn = o.RefreshToken != ""
			if o.ExpiresAt > 0 {
				p.Expires = time.UnixMilli(o.ExpiresAt)
			}
			p.Plan = strings.TrimSpace(o.SubscriptionType)
		}
	}
}

// ---------------------------------------------------------------------------
// CRUD
// ---------------------------------------------------------------------------

// List returns every profile, most recently used first.
func List() ([]Profile, error) {
	dir, err := profilesDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	st := loadState()

	var out []Profile
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		p := Profile{
			Name:     e.Name(),
			Dir:      filepath.Join(dir, e.Name()),
			LastUsed: st.LastUsed[e.Name()],
		}
		readMeta(&p)
		out = append(out, p)
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].LastUsed.Equal(out[j].LastUsed) {
			return out[i].Name < out[j].Name
		}
		return out[i].LastUsed.After(out[j].LastUsed)
	})
	return out, nil
}

func Find(name string) (Profile, error) {
	ps, err := List()
	if err != nil {
		return Profile{}, err
	}
	for _, p := range ps {
		if strings.EqualFold(p.Name, name) {
			return p, nil
		}
	}
	return Profile{}, fmt.Errorf("no profile named %q", name)
}

func Create(name string) (Profile, error) {
	if err := ValidName(name); err != nil {
		return Profile{}, err
	}
	dir, err := profilesDir()
	if err != nil {
		return Profile{}, err
	}
	target := filepath.Join(dir, name)
	if _, err := os.Stat(target); err == nil {
		return Profile{}, fmt.Errorf("profile %q already exists", name)
	}
	if err := os.MkdirAll(target, 0o700); err != nil {
		return Profile{}, err
	}
	return Profile{Name: name, Dir: target}, nil
}

func Delete(name string) error {
	p, err := Find(name)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(p.Dir); err != nil {
		return err
	}
	st := loadState()
	delete(st.LastUsed, name)
	return st.save()
}

func Rename(old, newName string) error {
	if err := ValidName(newName); err != nil {
		return err
	}
	p, err := Find(old)
	if err != nil {
		return err
	}
	dir, err := profilesDir()
	if err != nil {
		return err
	}
	target := filepath.Join(dir, newName)
	if _, err := os.Stat(target); err == nil {
		return fmt.Errorf("profile %q already exists", newName)
	}
	if err := os.Rename(p.Dir, target); err != nil {
		return err
	}
	st := loadState()
	if t, ok := st.LastUsed[old]; ok {
		st.LastUsed[newName] = t
		delete(st.LastUsed, old)
	}
	return st.save()
}

// Import copies the machine's existing ~/.claude (plus ~/.claude.json) into a
// new profile, so the account already logged in keeps its settings, MCP
// servers and session history instead of starting from nothing.
func Import(name string) (Profile, error) {
	src, err := DefaultClaudeDir()
	if err != nil {
		return Profile{}, err
	}
	if fi, err := os.Stat(src); err != nil || !fi.IsDir() {
		return Profile{}, fmt.Errorf("no existing config at %s", src)
	}

	p, err := Create(name)
	if err != nil {
		return Profile{}, err
	}

	// shell-snapshots is a regenerable cache and can be large.
	skip := map[string]bool{"shell-snapshots": true}
	if err := copyTree(src, p.Dir, skip); err != nil {
		_ = os.RemoveAll(p.Dir)
		return Profile{}, err
	}

	// ~/.claude.json lives beside ~/.claude by default, but *inside* the
	// directory once CLAUDE_CONFIG_DIR is in play.
	home, err := os.UserHomeDir()
	if err == nil {
		outer := filepath.Join(home, ".claude.json")
		if _, err := os.Stat(outer); err == nil {
			_ = copyFile(outer, filepath.Join(p.Dir, ".claude.json"))
		}
	}

	readMeta(&p)
	return p, nil
}

func copyTree(src, dst string, skipTop map[string]bool) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}
	for _, e := range entries {
		if skipTop[e.Name()] {
			continue
		}
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		switch {
		case e.IsDir():
			if err := copyTree(s, d, nil); err != nil {
				return err
			}
		case e.Type().IsRegular():
			if err := copyFile(s, d); err != nil {
				return err
			}
		default:
			// symlinks / sockets: skip rather than guess
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	// 0600 — these files hold live OAuth tokens.
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
