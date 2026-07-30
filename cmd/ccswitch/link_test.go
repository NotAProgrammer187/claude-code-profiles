package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withHome points ccswitch at a throwaway home directory containing the named
// profiles, so link state can be written and read without touching the real
// ~/.ccswitch.
func withHome(t *testing.T, profiles ...string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("USERPROFILE", home) // Windows
	t.Setenv("HOME", home)        // everywhere else
	for _, name := range profiles {
		if err := os.MkdirAll(filepath.Join(home, ".ccswitch", "profiles", name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func TestSetAndResolveLink(t *testing.T) {
	withHome(t, "work")
	dir := t.TempDir()

	if _, _, ok := LinkedProfile(dir); ok {
		t.Fatal("a fresh directory should resolve to nothing")
	}

	l, err := SetLink(dir, "work")
	if err != nil {
		t.Fatalf("SetLink: %v", err)
	}
	if l.Profile != "work" {
		t.Errorf("profile = %q", l.Profile)
	}

	name, source, ok := LinkedProfile(dir)
	if !ok || name != "work" {
		t.Fatalf("LinkedProfile = %q, %q, %v", name, source, ok)
	}
	if !sameDir(source, dir) {
		t.Errorf("source = %q, want %q", source, dir)
	}
}

// Subdirectories inherit, and the nearest link wins — the point of walking up
// rather than matching exactly.
func TestLinkedProfileWalksUpAndNearestWins(t *testing.T) {
	withHome(t, "work", "side")
	top := t.TempDir()
	deep := filepath.Join(top, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o700); err != nil {
		t.Fatal(err)
	}

	if _, err := SetLink(top, "work"); err != nil {
		t.Fatal(err)
	}
	if name, _, _ := LinkedProfile(deep); name != "work" {
		t.Errorf("deep directory should inherit work, got %q", name)
	}

	mid := filepath.Join(top, "a", "b")
	if _, err := SetLink(mid, "side"); err != nil {
		t.Fatal(err)
	}
	if name, source, _ := LinkedProfile(deep); name != "side" {
		t.Errorf("nearest link should win: got %q from %q", name, source)
	}
	if name, _, _ := LinkedProfile(filepath.Join(top, "a")); name != "work" {
		t.Errorf("a sibling branch should still resolve to work, got %q", name)
	}
}

func TestSetLinkReplacesExisting(t *testing.T) {
	withHome(t, "work", "side")
	dir := t.TempDir()

	if _, err := SetLink(dir, "work"); err != nil {
		t.Fatal(err)
	}
	if _, err := SetLink(dir, "side"); err != nil {
		t.Fatal(err)
	}
	if n := len(LinkList()); n != 1 {
		t.Errorf("got %d links, want the second to replace the first", n)
	}
	if name, _, _ := LinkedProfile(dir); name != "side" {
		t.Errorf("got %q, want side", name)
	}
}

func TestSetLinkRejectsUnknownProfileAndFile(t *testing.T) {
	withHome(t, "work")
	if _, err := SetLink(t.TempDir(), "ghost"); err == nil {
		t.Error("expected an error for a profile that doesn't exist")
	}

	f := filepath.Join(t.TempDir(), "notadir")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := SetLink(f, "work"); err == nil {
		t.Error("expected an error when the target isn't a directory")
	}
}

func TestRemoveLink(t *testing.T) {
	withHome(t, "work")
	top := t.TempDir()
	sub := filepath.Join(top, "sub")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := SetLink(top, "work"); err != nil {
		t.Fatal(err)
	}

	// Unlinking only removes a link made for that exact directory; an
	// inherited one belongs to the parent.
	if _, err := RemoveLink(sub); err == nil {
		t.Error("expected unlinking an inherited directory to fail")
	}
	if _, err := RemoveLink(top); err != nil {
		t.Fatalf("RemoveLink: %v", err)
	}
	if _, _, ok := LinkedProfile(top); ok {
		t.Error("link survived removal")
	}
}

func TestLinksFollowRenameAndDelete(t *testing.T) {
	withHome(t, "work")
	dir := t.TempDir()
	if _, err := SetLink(dir, "work"); err != nil {
		t.Fatal(err)
	}

	if err := Rename("work", "job"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if name, _, _ := LinkedProfile(dir); name != "job" {
		t.Errorf("link should follow the rename, got %q", name)
	}

	if err := Delete("job"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if name, _, ok := LinkedProfile(dir); ok {
		t.Errorf("link should be dropped with the profile, got %q", name)
	}
}

func TestReadLinkFile(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) {
		if err := os.WriteFile(filepath.Join(dir, linkFile), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write("work\n")
	if got := readLinkFile(dir); got != "work" {
		t.Errorf("got %q", got)
	}
	write("# which account this repo belongs to\n\n  side  \n")
	if got := readLinkFile(dir); got != "side" {
		t.Errorf("comments and blank lines should be skipped, got %q", got)
	}
	// Someone else's .ccswitch file, or junk: ignore it rather than fail.
	write("../../etc/passwd\n")
	if got := readLinkFile(dir); got != "" {
		t.Errorf("an invalid name should be ignored, got %q", got)
	}
	write("")
	if got := readLinkFile(dir); got != "" {
		t.Errorf("an empty file should resolve to nothing, got %q", got)
	}
}

func TestLinkFileResolvesButLosesToRealLink(t *testing.T) {
	withHome(t, "work", "side")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, linkFile), []byte("side\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	name, source, ok := LinkedProfile(dir)
	if !ok || name != "side" {
		t.Fatalf("expected the .ccswitch file to resolve, got %q %v", name, ok)
	}
	if filepath.Base(source) != linkFile {
		t.Errorf("source should be the file, got %q", source)
	}

	// An explicit link for the same directory is the stronger statement.
	if _, err := SetLink(dir, "work"); err != nil {
		t.Fatal(err)
	}
	if name, _, _ := LinkedProfile(dir); name != "work" {
		t.Errorf("an explicit link should beat the file, got %q", name)
	}
}

func TestShortPath(t *testing.T) {
	home := withHome(t)
	if got := shortPath(home); got != "~" {
		t.Errorf("got %q, want ~", got)
	}
	inside := filepath.Join(home, "src", "repo")
	if got := shortPath(inside); !strings.HasPrefix(got, "~") {
		t.Errorf("got %q, want a ~-relative path", got)
	}
	outside := filepath.VolumeName(home) + string(filepath.Separator) + "definitely-not-home"
	if got := shortPath(outside); strings.HasPrefix(got, "~") {
		t.Errorf("a path outside home shouldn't be abbreviated: %q", got)
	}
}
