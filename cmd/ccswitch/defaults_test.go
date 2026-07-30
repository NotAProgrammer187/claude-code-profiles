package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestMergeDefaultsKeepsUnsharedKeys(t *testing.T) {
	shared := map[string]json.RawMessage{"remoteControlAtStartup": json.RawMessage("false")}
	settings := []byte(`{"theme":"dark","model":"opus","permissions":{"allow":["Bash"]}}`)

	out, changed, err := mergeDefaults(shared, settings)
	if err != nil {
		t.Fatalf("mergeDefaults: %v", err)
	}
	if !reflect.DeepEqual(changed, []string{"remoteControlAtStartup"}) {
		t.Fatalf("changed = %v, want just the shared key", changed)
	}

	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	// Sharing one key must not turn a profile's settings.json into the shared
	// file — everything it already had is still its own.
	if got["theme"] != "dark" || got["model"] != "opus" {
		t.Errorf("per-profile keys were lost: %v", got)
	}
	if _, ok := got["permissions"]; !ok {
		t.Error("nested block was dropped")
	}
	if got["remoteControlAtStartup"] != false {
		t.Errorf("shared key = %v, want false", got["remoteControlAtStartup"])
	}
}

// The whole point of "shared" is that the profile doesn't get to drift: a key
// we share is rewritten even when the profile already has a different value.
func TestMergeDefaultsOverwritesDrift(t *testing.T) {
	shared := map[string]json.RawMessage{"remoteControlAtStartup": json.RawMessage("false")}

	out, changed, err := mergeDefaults(shared, []byte(`{"remoteControlAtStartup":true}`))
	if err != nil {
		t.Fatalf("mergeDefaults: %v", err)
	}
	if len(changed) != 1 {
		t.Fatalf("changed = %v, want the key to be rewritten", changed)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got["remoteControlAtStartup"] != false {
		t.Errorf("got %v, want false", got["remoteControlAtStartup"])
	}
}

func TestMergeDefaultsIsIdempotent(t *testing.T) {
	shared := map[string]json.RawMessage{
		"remoteControlAtStartup": json.RawMessage("false"),
		"theme":                  json.RawMessage(`"dark"`),
	}
	// Same values, different spacing and order: not a change, so a launch that
	// changes nothing stays silent.
	settings := []byte("{\n  \"theme\" : \"dark\",\n  \"remoteControlAtStartup\":false\n}")

	out, changed, err := mergeDefaults(shared, settings)
	if err != nil {
		t.Fatalf("mergeDefaults: %v", err)
	}
	if out != nil || len(changed) != 0 {
		t.Fatalf("rewrote a profile that already agreed (changed=%v)", changed)
	}
}

func TestMergeDefaultsEmptyInputs(t *testing.T) {
	if out, changed, err := mergeDefaults(nil, []byte(`{"theme":"dark"}`)); out != nil || changed != nil || err != nil {
		t.Fatalf("nothing shared should be a no-op: %v %v", changed, err)
	}
	// A profile that has never been launched has no settings.json at all.
	out, changed, err := mergeDefaults(
		map[string]json.RawMessage{"theme": json.RawMessage(`"dark"`)}, nil)
	if err != nil || len(changed) != 1 {
		t.Fatalf("merge into missing settings: changed=%v err=%v", changed, err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil || got["theme"] != "dark" {
		t.Errorf("expected the file to be created: %v %v", got, err)
	}
}

func TestMergeDefaultsRefusesBrokenSettings(t *testing.T) {
	shared := map[string]json.RawMessage{"theme": json.RawMessage(`"dark"`)}
	if _, _, err := mergeDefaults(shared, []byte(`{oops`)); err == nil {
		t.Fatal("expected an error for unparseable settings.json, got none")
	}
}

func TestApplyDefaultsWritesAndReportsOnce(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root) // os.UserHomeDir on Windows

	if err := saveDefaults(map[string]json.RawMessage{
		"remoteControlAtStartup": json.RawMessage("false"),
	}); err != nil {
		t.Fatalf("saveDefaults: %v", err)
	}

	p := Profile{Name: "work", Dir: filepath.Join(root, "profiles", "work")}
	if err := os.MkdirAll(p.Dir, 0o700); err != nil {
		t.Fatal(err)
	}

	changed, err := ApplyDefaults(p)
	if err != nil || len(changed) != 1 {
		t.Fatalf("first apply: changed=%v err=%v", changed, err)
	}
	body, err := os.ReadFile(filepath.Join(p.Dir, settingsFile))
	if err != nil {
		t.Fatalf("settings.json was not written: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil || got["remoteControlAtStartup"] != false {
		t.Fatalf("wrote %s", body)
	}

	// Second run: in step, so nothing is written and nothing is announced.
	if changed, err := ApplyDefaults(p); err != nil || len(changed) != 0 {
		t.Fatalf("second apply: changed=%v err=%v", changed, err)
	}
}

func TestSaveDefaultsRemovesEmptyFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)

	if err := saveDefaults(map[string]json.RawMessage{"theme": json.RawMessage(`"dark"`)}); err != nil {
		t.Fatal(err)
	}
	path, err := defaultsPath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("defaults file missing: %v", err)
	}

	if err := saveDefaults(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("unsharing the last key should remove the file, got %v", err)
	}
	// And reading it back is empty rather than an error.
	if m, err := loadDefaults(); err != nil || len(m) != 0 {
		t.Errorf("loadDefaults after removal: %v %v", m, err)
	}
}

func TestParseValue(t *testing.T) {
	cases := []struct{ in, want string }{
		{"false", "false"},
		{"true", "true"},
		{"3", "3"},
		{"dark", `"dark"`},
		// Not valid JSON on its own, and a real settings value: must not be
		// mangled into something else.
		{"opus[1m]", `"opus[1m]"`},
		{`{"A":"b"}`, `{"A":"b"}`},
		{`"quoted"`, `"quoted"`},
	}
	for _, c := range cases {
		if got := string(parseValue(c.in)); got != c.want {
			t.Errorf("parseValue(%q) = %s, want %s", c.in, got, c.want)
		}
	}
}
