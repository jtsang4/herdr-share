package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/jtsang4/herdr-share/internal/config"
	"github.com/jtsang4/herdr-share/internal/version"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"0.1.1", "0.1.0", true},
		{"0.2.0", "0.10.0", false},
		{"v1.0.0", "0.9.9", true},
		{"0.1.0", "0.1.0", false},
		{"dev", "0.1.0", false},
		{"0.1.0", "dev", true},
		{"1.0.0-rc.1", "0.9.0", true},
	} {
		if got := newer(c.a, c.b); got != c.want {
			t.Errorf("newer(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func TestRefreshModOnlyUpgrades(t *testing.T) {
	appHome := t.TempDir()
	t.Setenv("HERDR_APP_HOME", appHome)
	paths, err := config.Resolve(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := &app{stdout: io.Discard, stderr: io.Discard, paths: paths}
	saved := version.Version
	t.Cleanup(func() { version.Version = saved })

	installed := func() string {
		b, err := os.ReadFile(filepath.Join(appHome, "mods", "herdr-share", "install.json"))
		if err != nil {
			return ""
		}
		var info installInfo
		_ = json.Unmarshal(b, &info)
		return info.Version
	}

	// Nothing installed: nothing written.
	version.Version = "0.2.0"
	a.refreshMod()
	if installed() != "" {
		t.Fatal("refresh installed a mod nobody asked for")
	}

	version.Version = "0.1.0"
	if err := a.writeMod(appHome); err != nil {
		t.Fatal(err)
	}
	version.Version = "0.2.0"
	a.refreshMod()
	if got := installed(); got != "0.2.0" {
		t.Fatalf("after upgrade: %q", got)
	}
	version.Version = "0.1.5"
	a.refreshMod()
	if got := installed(); got != "0.2.0" {
		t.Fatalf("an older binary downgraded the mod to %q", got)
	}
	version.Version = "dev"
	a.refreshMod()
	if got := installed(); got != "0.2.0" {
		t.Fatalf("a development build rewrote the mod: %q", got)
	}
}
