package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The bug this reproduces: a previous .old still held open by another running
// copy. os.Remove fails, and the old code then renamed onto it anyway and
// reported "Access is denied" against the wrong file. A non-empty directory is
// a portable stand-in for an unremovable path — Remove fails on it everywhere.
func TestAsidePathSkipsUnremovable(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(dir, "ccswitch")
	writeFile(t, self, "current binary")

	blocked := self + ".old"
	if err := os.MkdirAll(filepath.Join(blocked, "child"), 0o700); err != nil {
		t.Fatal(err)
	}

	got, err := asidePath(self)
	if err != nil {
		t.Fatalf("asidePath: %v", err)
	}
	if got != self+".old1" {
		t.Errorf("got %q, want the first free numbered name", got)
	}
}

func TestAsidePathPrefersPlainOld(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(dir, "ccswitch")
	writeFile(t, self, "current")

	// Nothing in the way.
	got, err := asidePath(self)
	if err != nil || got != self+".old" {
		t.Fatalf("got %q err=%v, want %q", got, err, self+".old")
	}

	// A stale but removable .old is reclaimed rather than skipped, so the
	// numbered names don't accumulate on every upgrade.
	writeFile(t, self+".old", "stale")
	got, err = asidePath(self)
	if err != nil || got != self+".old" {
		t.Fatalf("got %q err=%v, want the plain .old to be reused", got, err)
	}
	if _, err := os.Stat(self + ".old"); !os.IsNotExist(err) {
		t.Error("the stale .old should have been removed")
	}
}

func TestAsidePathExhausted(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(dir, "ccswitch")
	writeFile(t, self, "current")

	for i := 0; i < 20; i++ {
		name := self + ".old"
		if i > 0 {
			name = fmt.Sprintf("%s.old%d", self, i)
		}
		if err := os.MkdirAll(filepath.Join(name, "child"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := asidePath(self); err == nil {
		t.Fatal("expected an error when every name is blocked")
	} else if !strings.Contains(err.Error(), "ccswitch processes") {
		t.Errorf("error should say what to do about it, got: %v", err)
	}
}

// A chunked, paused transfer still lands intact — a smoke test for the
// progressReader and watchdog being in the copy path at all. It does not
// reproduce the original failure, which needed a transfer longer than the old
// five-minute deadline; TestHTTPClientTimeouts is the actual regression guard.
func TestDownloadSurvivesSlowTransfer(t *testing.T) {
	body := strings.Repeat("x", 64*1024)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, _ := w.(http.Flusher)
		for i := 0; i < 8; i++ {
			fmt.Fprint(w, body)
			if f != nil {
				f.Flush()
			}
			time.Sleep(40 * time.Millisecond)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	dst := filepath.Join(dir, "out")
	tmpl := filepath.Join(dir, "template")
	writeFile(t, tmpl, "x")

	if err := download(srv.URL, dst, tmpl); err != nil {
		t.Fatalf("a slow but progressing download must succeed: %v", err)
	}
	fi, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if want := int64(len(body) * 8); fi.Size() != want {
		t.Errorf("wrote %d bytes, want %d", fi.Size(), want)
	}
}

func TestDownloadRejectsBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()

	dir := t.TempDir()
	dst := filepath.Join(dir, "out")
	if err := download(srv.URL, dst, filepath.Join(dir, "missing")); err == nil {
		t.Fatal("expected an error for a 404")
	}
	if _, err := os.Stat(dst); err == nil {
		t.Error("a failed download must not leave a partial file behind")
	}
}

// The client used for downloads must have no overall deadline — that's the
// whole point of the fix — while the metadata client keeps one.
func TestHTTPClientTimeouts(t *testing.T) {
	if got := httpClient(0).Timeout; got != 0 {
		t.Errorf("download client has an overall timeout of %s, want none", got)
	}
	if got := httpClient(30 * time.Second).Timeout; got != 30*time.Second {
		t.Errorf("metadata client timeout = %s, want 30s", got)
	}
}

func TestParseChecksums(t *testing.T) {
	manifest := strings.Join([]string{
		"0e5751c026e543b2e8ab2eb06099daa1d1e5df47778f7787faab45cdf12fe3a8  ccswitch.exe",
		"a3f5c1d2e8ab2eb06099daa1d1e5df47778f7787faab45cdf12fe3a80e5751c0 *ccswitch-linux-amd64",
		"",
		"not a checksum line",
	}, "\n")

	sums := parseChecksums(strings.NewReader(manifest))
	if len(sums) != 2 {
		t.Fatalf("parsed %d entries, want 2", len(sums))
	}
	if sums["ccswitch.exe"] != "0e5751c026e543b2e8ab2eb06099daa1d1e5df47778f7787faab45cdf12fe3a8" {
		t.Errorf("wrong hash for ccswitch.exe: %s", sums["ccswitch.exe"])
	}
	// The `*` binary-mode marker must not end up in the filename.
	if _, ok := sums["ccswitch-linux-amd64"]; !ok {
		t.Error("binary-mode entry (*name) was not stripped to its filename")
	}
}

func TestVerifyChecksum(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "asset")
	writeFile(t, path, "hello")
	// sha256("hello")
	const good = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"

	if err := verifyChecksum(path, "asset", map[string]string{"asset": good}); err != nil {
		t.Errorf("matching checksum must verify: %v", err)
	}
	if err := verifyChecksum(path, "asset", map[string]string{"asset": strings.Repeat("0", 64)}); err == nil {
		t.Error("a wrong hash must be rejected")
	}
	if err := verifyChecksum(path, "asset", map[string]string{"other": good}); err == nil {
		t.Error("a manifest that doesn't list the asset must be rejected")
	}
}

func TestManagedInstaller(t *testing.T) {
	cases := []struct {
		path string
		want string // manager name; "" means unmanaged
	}{
		{`C:\Users\r\scoop\apps\ccswitch\0.1.7\ccswitch.exe`, "Scoop"},
		{`D:\Scoop\apps\ccswitch\current\ccswitch.exe`, "Scoop"},
		{"/opt/homebrew/Cellar/ccswitch/0.1.7/bin/ccswitch", "Homebrew"},
		{"/home/linuxbrew/.linuxbrew/Cellar/ccswitch/0.1.7/bin/ccswitch", "Homebrew"},
		{"/usr/local/bin/ccswitch", ""},
		{`C:\Users\r\.local\bin\ccswitch.exe`, ""},
	}
	for _, c := range cases {
		name, hint := managedInstaller(c.path)
		if name != c.want {
			t.Errorf("managedInstaller(%q) = %q, want %q", c.path, name, c.want)
		}
		if (name == "") != (hint == "") {
			t.Errorf("managedInstaller(%q): name and hint must come together, got %q / %q", c.path, name, hint)
		}
	}
}

func TestProgressReaderTracksIdle(t *testing.T) {
	pr := &progressReader{r: strings.NewReader("hello")}
	pr.last.Store(time.Now().Add(-time.Hour).UnixNano())
	if pr.idle() < time.Minute {
		t.Fatal("idle should reflect the stored timestamp")
	}
	buf := make([]byte, 5)
	if _, err := pr.Read(buf); err != nil {
		t.Fatal(err)
	}
	if pr.idle() > time.Second {
		t.Error("reading bytes should reset the idle clock")
	}
}
