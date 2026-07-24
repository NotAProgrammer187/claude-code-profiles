package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMergeMCPKeepsOtherKeys(t *testing.T) {
	src := []byte(`{"mcpServers":{"github":{"command":"gh-mcp"}},"oauthAccount":{"emailAddress":"a@example.com"}}`)
	dst := []byte(`{"oauthAccount":{"emailAddress":"b@example.com"},"projects":{"/tmp":{"allowed":true}}}`)

	out, n, err := mergeMCP(src, dst)
	if err != nil {
		t.Fatalf("mergeMCP: %v", err)
	}
	if n != 1 {
		t.Fatalf("wrote %d servers, want 1", n)
	}

	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	// The target's own identity and project state must survive the merge —
	// that's the whole reason we don't copy .claude.json wholesale.
	acct := got["oauthAccount"].(map[string]any)
	if acct["emailAddress"] != "b@example.com" {
		t.Errorf("target identity was overwritten: %v", acct["emailAddress"])
	}
	if _, ok := got["projects"]; !ok {
		t.Error("target lost its projects block")
	}
	servers := got["mcpServers"].(map[string]any)
	if _, ok := servers["github"]; !ok {
		t.Error("github server was not merged in")
	}
}

func TestMergeMCPAddsToExistingServers(t *testing.T) {
	src := []byte(`{"mcpServers":{"github":{"command":"gh-mcp"}}}`)
	dst := []byte(`{"mcpServers":{"local":{"command":"./local"}}}`)

	out, n, err := mergeMCP(src, dst)
	if err != nil {
		t.Fatalf("mergeMCP: %v", err)
	}
	if n != 1 {
		t.Fatalf("wrote %d servers, want 1", n)
	}
	servers, err := mcpBlock(out)
	if err != nil {
		t.Fatalf("mcpBlock: %v", err)
	}
	if len(servers) != 2 {
		t.Fatalf("got %d servers, want both", len(servers))
	}
}

func TestMergeMCPIsIdempotent(t *testing.T) {
	src := []byte(`{"mcpServers":{"github":{"command":"gh-mcp","args":["--stdio"]}}}`)
	// Same servers, different key order and spacing: not a change.
	dst := []byte("{\n  \"mcpServers\": {\n    \"github\": { \"args\": [\"--stdio\"], \"command\": \"gh-mcp\" }\n  }\n}")

	out, n, err := mergeMCP(src, dst)
	if err != nil {
		t.Fatalf("mergeMCP: %v", err)
	}
	if n != 0 || out != nil {
		t.Fatalf("re-wrote an already-synced target (n=%d)", n)
	}
}

func TestMergeMCPEmptyInputs(t *testing.T) {
	if _, n, err := mergeMCP(nil, nil); err != nil || n != 0 {
		t.Fatalf("no source servers should be a no-op, got n=%d err=%v", n, err)
	}
	// A target that has never been launched has no .claude.json at all.
	out, n, err := mergeMCP([]byte(`{"mcpServers":{"a":{}}}`), nil)
	if err != nil || n != 1 {
		t.Fatalf("merge into missing target: n=%d err=%v", n, err)
	}
	if servers, _ := mcpBlock(out); len(servers) != 1 {
		t.Errorf("expected the server to be created, got %v", servers)
	}
}

func TestMergeMCPRejectsBrokenTarget(t *testing.T) {
	if _, _, err := mergeMCP([]byte(`{"mcpServers":{"a":{}}}`), []byte(`{oops`)); err == nil {
		t.Fatal("expected an error for an unreadable target, got none")
	}
}

func TestSelectItems(t *testing.T) {
	all, err := selectItems("")
	if err != nil || len(all) != len(syncItems) {
		t.Fatalf("empty --only should mean everything: %v %v", len(all), err)
	}
	if all, err := selectItems("all"); err != nil || len(all) != len(syncItems) {
		t.Fatalf(`--only all should mean everything: %v %v`, len(all), err)
	}
	got, err := selectItems("settings, MCP")
	if err != nil {
		t.Fatalf("selectItems: %v", err)
	}
	if len(got) != 2 || got[0].Name != "settings" || got[1].Name != "mcp" {
		t.Errorf("got %v, want settings and mcp", got)
	}
	if _, err := selectItems("nope"); err == nil {
		t.Error("expected an error for an unknown item")
	}
}

// writeFile is a test helper: create parents, then write.
func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPlanAndApplyFile(t *testing.T) {
	src := Profile{Name: "src", Dir: t.TempDir()}
	dst := Profile{Name: "dst", Dir: t.TempDir()}
	item := syncItem{Name: "settings", Path: "settings.json"}

	// Nothing in the source: nothing to do.
	if note, err := planItem(item, src, dst); err != nil || note != "" {
		t.Fatalf("empty source: note=%q err=%v", note, err)
	}

	writeFile(t, filepath.Join(src.Dir, "settings.json"), `{"theme":"dark"}`)
	if note, _ := planItem(item, src, dst); note != "new" {
		t.Fatalf("note=%q, want new", note)
	}
	if err := applyItem(item, src, dst); err != nil {
		t.Fatalf("applyItem: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dst.Dir, "settings.json"))
	if err != nil || string(got) != `{"theme":"dark"}` {
		t.Fatalf("copy failed: %q %v", got, err)
	}

	// Already identical: a second run reports nothing.
	if note, _ := planItem(item, src, dst); note != "" {
		t.Errorf("second run should be a no-op, got %q", note)
	}

	writeFile(t, filepath.Join(dst.Dir, "settings.json"), `{"theme":"light"}`)
	if note, _ := planItem(item, src, dst); note != "replaced" {
		t.Errorf("note=%q, want replaced", note)
	}
}

func TestPlanAndApplyDir(t *testing.T) {
	src := Profile{Name: "src", Dir: t.TempDir()}
	dst := Profile{Name: "dst", Dir: t.TempDir()}
	item := syncItem{Name: "commands", Path: "commands", Dir: true}

	writeFile(t, filepath.Join(src.Dir, "commands", "ship.md"), "ship it")
	writeFile(t, filepath.Join(src.Dir, "commands", "nested", "deep.md"), "deep")
	// A file only the target has must survive: sync merges, it doesn't mirror.
	writeFile(t, filepath.Join(dst.Dir, "commands", "mine.md"), "keep me")

	note, err := planItem(item, src, dst)
	if err != nil {
		t.Fatalf("planItem: %v", err)
	}
	if note != "2 files" {
		t.Fatalf("note=%q, want 2 files", note)
	}
	if err := applyItem(item, src, dst); err != nil {
		t.Fatalf("applyItem: %v", err)
	}
	for _, rel := range []string{"ship.md", filepath.Join("nested", "deep.md"), "mine.md"} {
		if _, err := os.Stat(filepath.Join(dst.Dir, "commands", rel)); err != nil {
			t.Errorf("missing %s after sync: %v", rel, err)
		}
	}
	if note, _ := planItem(item, src, dst); note != "" {
		t.Errorf("second run should be a no-op, got %q", note)
	}
}

func TestSyncNeverCarriesCredentials(t *testing.T) {
	for _, it := range syncItems {
		switch it.Path {
		case ".credentials.json", claudeJSONFile, "history.jsonl", "projects":
			t.Errorf("%q is in the sync allowlist and must not be", it.Path)
		}
	}
}

func TestPlural(t *testing.T) {
	if got := plural(1, "file"); got != "1 file" {
		t.Errorf("got %q", got)
	}
	if got := plural(3, "server"); got != "3 servers" {
		t.Errorf("got %q", got)
	}
}
