package main

import "testing"

func TestCloudSyncMarker(t *testing.T) {
	cases := []struct {
		path, want string
	}{
		{`C:\Users\ryan\.ccswitch`, ""},
		{`/home/ryan/.ccswitch`, ""},
		{`C:\Users\ryan\OneDrive\.ccswitch`, "OneDrive"},
		{`C:\Users\ryan\onedrive - Contoso\.ccswitch`, "OneDrive"},
		{`/Users/ryan/Dropbox/.ccswitch`, "Dropbox"},
		{`C:\Users\ryan\Google Drive\.ccswitch`, "Google Drive"},
		{`/Users/ryan/Library/Mobile Documents/.ccswitch`, "iCloud Drive"},
	}
	for _, c := range cases {
		if got := cloudSyncMarker(c.path); got != c.want {
			t.Errorf("cloudSyncMarker(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

func TestIsLauncherShim(t *testing.T) {
	cases := []struct {
		path  string
		roots []string
		want  bool
	}{
		// Default Scoop layout, either slash direction.
		{`C:\Users\ryan\scoop\shims\ccswitch.exe`, nil, true},
		{`C:/Users/ryan/scoop/shims/ccswitch.exe`, nil, true},
		// A relocated root only counts via the env-provided roots.
		{`D:\tools\shims\ccswitch.exe`, nil, false},
		{`D:\tools\shims\ccswitch.exe`, []string{`D:\tools`}, true},
		// The app's real binary is not a shim.
		{`C:\Users\ryan\scoop\apps\ccswitch\current\ccswitch.exe`, nil, false},
		// A directory merely named shims elsewhere doesn't match.
		{`C:\shims\ccswitch.exe`, nil, false},
		{`C:\shims\ccswitch.exe`, []string{""}, false},
	}
	for _, c := range cases {
		if got := isLauncherShim(c.path, c.roots); got != c.want {
			t.Errorf("isLauncherShim(%q, %v) = %v, want %v", c.path, c.roots, got, c.want)
		}
	}
}
