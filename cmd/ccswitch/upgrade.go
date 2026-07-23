package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
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

// cmdUpgrade replaces the running binary with the latest release for this
// platform. On Windows a running executable can't be overwritten, so the old
// one is renamed aside and cleaned up on the next run.
func cmdUpgrade() error {
	want, err := assetName()
	if err != nil {
		return err
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

	var url string
	for _, a := range rel.Assets {
		if a.Name == want {
			url = a.URL
			break
		}
	}
	if url == "" {
		return fmt.Errorf("release %s has no %s asset", rel.TagName, want)
	}

	self, err := os.Executable()
	if err != nil {
		return err
	}
	self, err = filepath.EvalSymlinks(self)
	if err != nil {
		return err
	}

	fmt.Printf("Downloading %s %s...\n", want, rel.TagName)
	tmp := self + ".new"
	if err := download(url, tmp, self); err != nil {
		return err
	}

	// Swap the new binary into place. Windows can't overwrite a running exe,
	// so move the current one aside first; it's removed on the next launch.
	old := self + ".old"
	_ = os.Remove(old)
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

// cleanupOldBinary removes the previous binary left behind by a Windows upgrade.
// Safe to call on every start; it does nothing when there's nothing to clean.
func cleanupOldBinary() {
	if self, err := os.Executable(); err == nil {
		_ = os.Remove(self + ".old")
	}
}

func download(url, dst, template string) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "ccswitch-upgrade")

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
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
	if _, err := io.Copy(out, resp.Body); err != nil {
		out.Close()
		_ = os.Remove(dst)
		return err
	}
	return out.Close()
}
