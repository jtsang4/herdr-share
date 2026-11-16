package config

import (
	"path/filepath"
	"testing"
	"time"
)

func TestParseTTL(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"30m": 30 * time.Minute, "2h": 2 * time.Hour, "90": 90 * time.Minute,
		"0": 0, "never": 0, "until-stopped": 0, " 1h30m ": 90 * time.Minute,
	} {
		got, err := ParseTTL(in)
		if err != nil || got != want {
			t.Errorf("ParseTTL(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "soon", "10s", "-5m"} {
		if _, err := ParseTTL(bad); err == nil {
			t.Errorf("ParseTTL(%q) accepted", bad)
		}
	}
}

func TestNormalizeRelay(t *testing.T) {
	for in, want := range map[string]string{
		"https://share.example.com/": "https://share.example.com",
		"share.example.com":          "https://share.example.com",
		"http://10.0.0.5:8080":       "http://10.0.0.5:8080",
		"https://example.com/herdr/": "https://example.com/herdr",
	} {
		got, err := NormalizeRelay(in)
		if err != nil || got != want {
			t.Errorf("NormalizeRelay(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"ftp://x", "https://", "https://x/?a=1", "https://x/#k"} {
		if _, err := NormalizeRelay(bad); err == nil {
			t.Errorf("NormalizeRelay(%q) accepted", bad)
		}
	}
}

func TestSetGetSaveLoad(t *testing.T) {
	p, err := Resolve(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var c Config
	for k, v := range map[string]string{"relay": "share.example.com", "token": "s3cret", "mode": "control", "ttl": "30m", "history": "500"} {
		if err := c.Set(k, v); err != nil {
			t.Fatalf("set %s: %v", k, err)
		}
	}
	if err := c.Set("mode", "write"); err == nil {
		t.Fatal("bad mode accepted")
	}
	if err := c.Set("nope", "x"); err == nil {
		t.Fatal("unknown key accepted")
	}
	if err := Save(p, c); err != nil {
		t.Fatal(err)
	}
	back, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if back != c || back.Relay != "https://share.example.com" || back.History != 500 {
		t.Fatalf("loaded %+v", back)
	}
	if v, _ := back.Get("history"); v != "500" {
		t.Fatalf("history %q", v)
	}
	if err := back.Set("relay", ""); err != nil || back.Relay != "" {
		t.Fatal("unset relay")
	}
}

func TestResolveHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvHome, dir)
	p, err := Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if p.ConfigFile != filepath.Join(dir, "config.json") || p.SharesDir() != filepath.Join(dir, "state", "shares") {
		t.Fatalf("paths %+v", p)
	}
	if err := p.EnsureState(); err != nil {
		t.Fatal(err)
	}
	if c, err := Load(p); err != nil || c != (Config{}) {
		t.Fatalf("missing config: %+v %v", c, err)
	}
}
