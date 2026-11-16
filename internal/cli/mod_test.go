package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const keysFile = `{
  "global": {
    "cmd-q": "app.quit",
    "cmd-t": "tab.new"
  },
  "terminal": {
    "cmd-c": "terminal.copy"
  }
}
`

func readKeys(t *testing.T, path string) map[string]map[string]string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("invalid JSON after edit: %v\n%s", err, b)
	}
	return m
}

func TestAddAndRemoveBinding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.json")
	if err := os.WriteFile(path, []byte(keysFile), 0o600); err != nil {
		t.Fatal(err)
	}
	bound, _, err := addBinding(path, "cmd-shift-s")
	if err != nil || !bound {
		t.Fatalf("bind: %v %v", bound, err)
	}
	k := readKeys(t, path)
	if k["global"]["cmd-shift-s"] != bindAction || k["global"]["cmd-q"] != "app.quit" || k["terminal"]["cmd-c"] != "terminal.copy" {
		t.Fatalf("keys %+v", k)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "\"cmd-q\": \"app.quit\",\n    \"cmd-t\"") {
		t.Fatalf("formatting of the rest changed:\n%s", b)
	}
	if _, err := os.Stat(path + ".herdr-share-backup"); err != nil {
		t.Fatal("no backup")
	}

	// Installing again does not add a second binding.
	bound, note, err := addBinding(path, "cmd-shift-s")
	if err != nil || !bound || !strings.Contains(note, "already") {
		t.Fatalf("again: %v %q %v", bound, note, err)
	}

	removed, err := removeBinding(path)
	if err != nil || !removed {
		t.Fatalf("remove: %v %v", removed, err)
	}
	k = readKeys(t, path)
	if _, ok := k["global"]["cmd-shift-s"]; ok || k["global"]["cmd-t"] != "tab.new" {
		t.Fatalf("after remove %+v", k)
	}
}

func TestBindingNeverTakesAUsedKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.json")
	if err := os.WriteFile(path, []byte(keysFile), 0o600); err != nil {
		t.Fatal(err)
	}
	bound, note, err := addBinding(path, "cmd-t")
	if err != nil || bound || !strings.Contains(note, "already uses") {
		t.Fatalf("used key: %v %q %v", bound, note, err)
	}
	if readKeys(t, path)["global"]["cmd-t"] != "tab.new" {
		t.Fatal("existing binding replaced")
	}
}

func TestBindingWithoutKeysFileOrGlobalSection(t *testing.T) {
	dir := t.TempDir()
	bound, note, err := addBinding(filepath.Join(dir, "keys.json"), "cmd-shift-s")
	if err != nil || bound || !strings.Contains(note, "does not exist") {
		t.Fatalf("missing file: %v %q %v", bound, note, err)
	}
	path := filepath.Join(dir, "keys2.json")
	if err := os.WriteFile(path, []byte("{\n  \"terminal\": {}\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if bound, _, err := addBinding(path, "ctrl-shift-s"); err != nil || !bound {
		t.Fatalf("no global section: %v %v", bound, err)
	}
	if readKeys(t, path)["global"]["ctrl-shift-s"] != bindAction {
		t.Fatal("binding missing")
	}
}

func TestInstallModFiles(t *testing.T) {
	for _, name := range []string{"herdr-share", "herdr-share-button"} {
		dir := filepath.Join(t.TempDir(), "mods", name)
		if err := installModFiles(name, dir); err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{"main.js", "mod.json"} {
			if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
				t.Fatalf("%s/%s not installed", name, f)
			}
		}
	}
}
