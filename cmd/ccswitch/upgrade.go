package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"
)

// releaseRepo is where upgrade looks for the latest published binary.
const releaseRepo = "NotAProgrammer187/claude-code-profiles"

// assetName is the release asset that matches the platform we're running on.
// It mirrors the names produced by .github/workflows/release.yml.
func assetName() (string, error) {
	switch runtime.GOOS {
	case "windows":
		if runtime.GOARCH == "amd64" {
			return "ccswitch.exe", nil
		}
	case "darwin":
		switch runtime.GOARCH {
		case "arm64":
			return "ccswitch-darwin-arm64", nil
		case "amd64":
			return "ccswitch-darwin-amd64", nil
		}
	case "linux":
		if runtime.GOARCH == "amd64" {
			return "ccswitch-linux-amd64", nil
		}
	}
	return "", fmt.Errorf("no prebuilt binary for %s/%s — build from source instead", runtime.GOOS, runtime.GOARCH)
}

// A single overall timeout is the wrong shape for a download. It can't tell a
// slow connection from a stalled one, so a 7 MB binary on a thin link fails at
// the deadline however healthy the transfer was — which is exactly what
// happened on a real upgrade. Bound the parts that should always be quick
// (connect, TLS, waiting for headers) and let the body take as long as it needs
// while it's still making progress; stallTimeout catches the case where it
// isn't.
const (
	connectTimeout = 15 * time.Second
	headerTimeout  = 30 * time.Second
	stallTimeout   = 60 * time.Second
)

// httpClient bounds setup but not transfer. overall may be zero for "no total
// deadline", which is what a download of unknown size on an unknown link wants.
func httpClient(overall time.Duration) *http.Client {
	return &http.Client{
		Timeout: overall,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   connectTimeout,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout:   connectTimeout,
			ResponseHeaderTimeout: headerTimeout,
			ExpectContinueTimeout: time.Second,
		},
	}
}

// progressReader notes when bytes last arrived, so a watchdog can tell a slow
// download from a dead one.
type progressReader struct {
	r    io.Reader
	last atomic.Int64 // unix nanos
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.last.Store(time.Now().UnixNano())
	}
	return n, err
}

func (p *progressReader) idle() time.Duration {
	return time.Since(time.Unix(0, p.last.Load()))
}

type ghRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func latestRelease() (ghRelease, error) {
	var rel ghRelease
	req, err := http.NewRequest("GET", "https://api.github.com/repos/"+releaseRepo+"/releases/latest", nil)
	if err != nil {
		return rel, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "ccswitch-upgrade")

	// Metadata is a few KB, so a total deadline is fine here.
	resp, err := httpClient(30 * time.Second).Do(req)
	if err != nil {
		return rel, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return rel, fmt.Errorf("GitHub returned %s fetching the latest release", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return rel, err
	}
	return rel, nil
}

// selfPath is the running binary with symlinks resolved — Homebrew's bin/
// entry is a symlink into the Cellar, and both upgrade and install-manager
// detection need the real file, not the link. One helper, so the nudge and
// the command it recommends can never disagree about the same binary.
func selfPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("could not locate the running binary: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("could not resolve the running binary's path: %w", err)
	}
	return resolved, nil
}

// managedInstaller names the package manager that owns the binary at path, if
// any, plus the command that updates it. Detection is by install layout,
// anchored to this app's own directory so an unrelated folder that merely
// contains "scoop" or "cellar" never locks the user out of upgrading: Scoop
// keeps every app under <root>/apps/<name>/ (default root ~/scoop; scoopRoots
// carries relocated ones — see scoopRootsFromEnv), Homebrew keeps kegs under
// Cellar/<name>/ on macOS and Linuxbrew alike. The path must already have
// symlinks resolved.
func managedInstaller(path string, scoopRoots []string) (name, hint string) {
	p := normInstallPath(path)
	for _, r := range scoopRoots {
		if r = normInstallPath(r); r != "" && strings.HasPrefix(p, r+"/apps/ccswitch/") {
			return "Scoop", "scoop update ccswitch"
		}
	}
	switch {
	case strings.Contains(p, "/scoop/apps/ccswitch/"):
		return "Scoop", "scoop update ccswitch"
	case strings.Contains(p, "/cellar/ccswitch/"):
		return "Homebrew", "brew upgrade ccswitch"
	}
	return "", ""
}

// normInstallPath lower-cases and forward-slashes a path so the layout checks
// above are one comparison on every platform.
func normInstallPath(p string) string {
	return strings.TrimRight(strings.ToLower(strings.ReplaceAll(p, `\`, `/`)), "/")
}

// scoopRootsFromEnv reports where a relocated Scoop keeps its apps: the SCOOP
// and SCOOP_GLOBAL env vars are Scoop's documented way to move its root off
// ~/scoop, and a moved root defeats the default-layout check.
func scoopRootsFromEnv() []string {
	return []string{os.Getenv("SCOOP"), os.Getenv("SCOOP_GLOBAL")}
}

// cmdUpgrade replaces the running binary with the latest release for this
// platform. On Windows a running executable can't be overwritten, so the old
// one is renamed aside and cleaned up on the next run.
func cmdUpgrade() error {
	want, err := assetName()
	if err != nil {
		return err
	}

	self, err := selfPath()
	if err != nil {
		return err
	}
	// A binary a package manager put in place is that manager's to update:
	// replacing it here would be silently undone by the manager's own next
	// upgrade, or leave its bookkeeping describing a binary it didn't install.
	if name, hint := managedInstaller(self, scoopRootsFromEnv()); name != "" {
		return fmt.Errorf("this ccswitch was installed by %s — update it with: %s", name, hint)
	}

	fmt.Println("Checking for the latest release...")
	rel, err := latestRelease()
	if err != nil {
		return err
	}

	tag := strings.TrimPrefix(rel.TagName, "v")
	if tag == version {
		fmt.Printf("Already on the latest version (%s).\n", version)
		return nil
	}

	var url, sumsURL string
	for _, a := range rel.Assets {
		switch a.Name {
		case want:
			url = a.URL
		case checksumAsset:
			sumsURL = a.URL
		}
	}
	if url == "" {
		return fmt.Errorf("release %s has no %s asset", rel.TagName, want)
	}

	fmt.Printf("Downloading %s %s...\n", want, rel.TagName)
	tmp := self + ".new"
	if err := download(url, tmp, self); err != nil {
		return err
	}

	// Verify what we downloaded before swapping it in. Releases before the
	// manifest existed have nothing to check against, so a missing manifest is
	// a loud warning rather than an error — but once one is present, any
	// mismatch is fatal.
	if sumsURL == "" {
		fmt.Printf("Warning: release %s has no %s manifest — checksum not verified.\n", rel.TagName, checksumAsset)
	} else {
		sums, err := fetchChecksums(sumsURL)
		if err != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("could not fetch %s: %w", checksumAsset, err)
		}
		if err := verifyChecksum(tmp, want, sums); err != nil {
			_ = os.Remove(tmp)
			return err
		}
		fmt.Println("Checksum verified.")
	}

	// Swap the new binary into place. Windows can't overwrite a running exe,
	// so move the current one aside first; it's removed on the next launch.
	old, err := asidePath(self)
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(self, old); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("could not move the current binary aside: %w", err)
	}
	if err := os.Rename(tmp, self); err != nil {
		// Roll back so the tool still works.
		_ = os.Rename(old, self)
		_ = os.Remove(tmp)
		return fmt.Errorf("could not install the new binary: %w", err)
	}
	if runtime.GOOS != "windows" {
		_ = os.Remove(old)
	}

	fmt.Printf("Updated ccswitch %s → %s.\n", version, tag)
	return nil
}

// asidePath picks a free name to move the running binary to.
//
// The obvious `.old` isn't always available: on Windows another running copy of
// ccswitch keeps its own binary open, and an open file can be neither deleted
// nor renamed over. The old code ignored the failed delete and then renamed
// onto the locked file anyway, so the upgrade died with "Access is denied" and
// blamed the current binary rather than the leftover. Fall back to numbered
// names instead — each gets cleaned up whenever the copy holding it exits.
func asidePath(self string) (string, error) {
	candidates := []string{self + ".old"}
	for i := 1; i < 20; i++ {
		candidates = append(candidates, fmt.Sprintf("%s.old%d", self, i))
	}
	for _, p := range candidates {
		// Removable or absent both mean the name is ours to take.
		if err := os.Remove(p); err == nil || os.IsNotExist(err) {
			return p, nil
		}
	}
	return "", fmt.Errorf("no free name to move the current binary aside — close other running ccswitch processes and try again")
}

// cleanupOldBinary removes binaries left behind by earlier upgrades. Safe to
// call on every start: names still held open by another running copy simply
// fail to delete and are retried next time.
func cleanupOldBinary() {
	self, err := os.Executable()
	if err != nil {
		return
	}
	matches, err := filepath.Glob(self + ".old*")
	if err != nil {
		return // only returns ErrBadPattern, which self can't produce in practice
	}
	for _, p := range matches {
		_ = os.Remove(p)
	}
}

func download(url, dst, template string) error {
	// The context is what the stall watchdog cancels; without it a transfer
	// that goes quiet mid-body would hang until the process is killed.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "ccswitch-upgrade")

	resp, err := httpClient(0).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: %s", resp.Status)
	}

	// Match the permissions of the current binary so the new one stays runnable.
	mode := os.FileMode(0o755)
	if fi, err := os.Stat(template); err == nil {
		mode = fi.Mode().Perm()
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}

	pr := &progressReader{r: resp.Body}
	pr.last.Store(time.Now().UnixNano())
	stalled := watchStall(ctx, cancel, pr)

	if _, err := io.Copy(out, pr); err != nil {
		out.Close()
		_ = os.Remove(dst)
		if stalled.Load() {
			return fmt.Errorf("download stalled — no data for %s", stallTimeout)
		}
		return err
	}
	return out.Close()
}

// checksumAsset is the manifest release.yml publishes beside the binaries:
// one `<sha256>  <filename>` line per asset, as written by sha256sum.
const checksumAsset = "SHA256SUMS"

// fetchChecksums downloads the manifest and returns filename → expected hash.
func fetchChecksums(url string) (map[string]string, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "ccswitch-upgrade")

	// The manifest is a few hundred bytes; a total deadline is fine.
	resp, err := httpClient(30 * time.Second).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download failed: %s", resp.Status)
	}
	return parseChecksums(resp.Body), nil
}

// parseChecksums reads sha256sum output: hash, whitespace, filename — with the
// `*` binary-mode marker some tools prefix to the name stripped off.
func parseChecksums(r io.Reader) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) != 2 {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		out[name] = strings.ToLower(fields[0])
	}
	return out
}

// verifyChecksum hashes path and compares it to the manifest entry for name.
// A manifest that doesn't mention the asset at all is as fatal as a mismatch —
// it means the release wasn't built the way we expect.
func verifyChecksum(path, name string, sums map[string]string) error {
	want, ok := sums[name]
	if !ok {
		return fmt.Errorf("%s does not list %s — refusing to install", checksumAsset, name)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != want {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s — refusing to install", name, got, want)
	}
	return nil
}

// watchStall cancels the request if the body stops producing bytes for
// stallTimeout, and reports whether that's why it was cancelled — otherwise the
// failure surfaces as a bare "context canceled" that explains nothing.
func watchStall(ctx context.Context, cancel context.CancelFunc, pr *progressReader) *atomic.Bool {
	var stalled atomic.Bool
	go func() {
		t := time.NewTicker(stallTimeout / 4)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if pr.idle() > stallTimeout {
					stalled.Store(true)
					cancel()
					return
				}
			}
		}
	}()
	return &stalled
}
