package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A mangled state.json holds every directory link; it must be preserved for
// recovery, not silently replaced by the next save.
func TestLoadStatePreservesCorruptFile(t *testing.T) {
	home := withHome(t)
	path := filepath.Join(home, ".ccswitch", "state.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"lastUsed": {"work"`), 0o600); err != nil {
		t.Fatal(err)
	}

	st := loadState()
	if len(st.LastUsed) != 0 || len(st.Links) != 0 {
		t.Errorf("corrupt state must load as empty, got %+v", st)
	}
	if _, err := os.Stat(path + ".corrupt"); err != nil {
		t.Error("the corrupt file must be moved aside, not left to be overwritten")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("the corrupt file must no longer sit where the next save lands")
	}
}

func TestStateSaveRoundTrips(t *testing.T) {
	withHome(t)
	st := loadState()
	st.LastUsed["work"] = time.Now()
	if err := st.save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, ok := loadState().LastUsed["work"]; !ok {
		t.Error("saved state must load back")
	}
}

// Find resolves names case-insensitively, so profiles differing only in case
// would be ambiguous everywhere — Create must refuse them even on filesystems
// that would happily hold both directories.
func TestCreateRejectsCaseCollision(t *testing.T) {
	withHome(t, "work")
	if _, err := Create("Work"); err == nil {
		t.Error("creating a case-variant of an existing profile must fail")
	}
	if _, err := Create("side"); err != nil {
		t.Errorf("an unrelated name must still create: %v", err)
	}
}

// Rename resolves the old name case-insensitively, and must migrate the
// last-used timestamp under the profile's real name, not the caller's spelling.
func TestRenameMigratesLastUsed(t *testing.T) {
	withHome(t, "work")
	TouchProfile("work")

	if err := Rename("WORK", "job"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	st := loadState()
	if _, ok := st.LastUsed["job"]; !ok {
		t.Error("the new name must carry the old last-used timestamp")
	}
	if _, ok := st.LastUsed["work"]; ok {
		t.Error("the old name must not linger in state")
	}
}

func TestRenameCaseOnly(t *testing.T) {
	withHome(t, "work")
	if err := Rename("work", "Work"); err != nil {
		t.Fatalf("a case-only rename of the same profile must be allowed: %v", err)
	}
	p, err := Find("work")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "Work" {
		t.Errorf("profile is named %q, want %q", p.Name, "Work")
	}
}

func TestReadFileIfPresent(t *testing.T) {
	dir := t.TempDir()
	if b, err := readFileIfPresent(filepath.Join(dir, "absent.json")); b != nil || err != nil {
		t.Errorf("a missing file is empty, not an error: %v %v", b, err)
	}
	path := filepath.Join(dir, "present.json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if b, err := readFileIfPresent(path); err != nil || string(b) != "{}" {
		t.Errorf("an existing file must read back: %q %v", b, err)
	}
}
