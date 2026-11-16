// Package config holds herdr-share's local settings and where its files live.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// EnvHome overrides every herdr-share path (config and state) with one folder.
const EnvHome = "HERDR_SHARE_HOME"

// Paths are herdr-share's files.
type Paths struct {
	// ConfigFile is config.json.
	ConfigFile string `json:"config_file"`
	// StateDir holds share records, control sockets and logs.
	StateDir string `json:"state_dir"`
	// Home is the HERDR_SHARE_HOME override, if any.
	Home string `json:"home,omitempty"`
}

// SharesDir holds one JSON record per live share.
func (p Paths) SharesDir() string { return filepath.Join(p.StateDir, "shares") }

// RunDir holds control sockets and ready files.
func (p Paths) RunDir() string { return filepath.Join(p.StateDir, "run") }

// LogDir holds bridge logs.
func (p Paths) LogDir() string { return filepath.Join(p.StateDir, "logs") }

// Resolve returns the paths for home (HERDR_SHARE_HOME when empty).
func Resolve(home string) (Paths, error) {
	if home == "" {
		home = os.Getenv(EnvHome)
	}
	if home != "" {
		abs, err := filepath.Abs(home)
		if err != nil {
			return Paths{}, err
		}
		return Paths{Home: abs, ConfigFile: filepath.Join(abs, "config.json"), StateDir: filepath.Join(abs, "state")}, nil
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	if runtime.GOOS == "windows" {
		cfg := os.Getenv("APPDATA")
		if cfg == "" {
			cfg = filepath.Join(userHome, "AppData", "Roaming")
		}
		state := os.Getenv("LOCALAPPDATA")
		if state == "" {
			state = filepath.Join(userHome, "AppData", "Local")
		}
		return Paths{ConfigFile: filepath.Join(cfg, "herdr-share", "config.json"), StateDir: filepath.Join(state, "herdr-share")}, nil
	}
	cfg := os.Getenv("XDG_CONFIG_HOME")
	if cfg == "" {
		cfg = filepath.Join(userHome, ".config")
	}
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		state = filepath.Join(userHome, ".local", "state")
	}
	return Paths{ConfigFile: filepath.Join(cfg, "herdr-share", "config.json"), StateDir: filepath.Join(state, "herdr-share")}, nil
}

// EnsureState creates the private state folders.
func (p Paths) EnsureState() error {
	for _, d := range []string{p.StateDir, p.SharesDir(), p.RunDir(), p.LogDir()} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	return nil
}

// Config is config.json.
type Config struct {
	// Relay is the relay's base URL, e.g. https://share.example.com.
	Relay string `json:"relay,omitempty"`
	// Token authenticates share creation on relays that require it.
	Token string `json:"token,omitempty"`
	// Mode is the default mode for new shares: view or control.
	Mode string `json:"mode,omitempty"`
	// TTL is the default lifetime for new shares ("2h", "30m", "0" for until stopped).
	TTL string `json:"ttl,omitempty"`
	// HC is the hc executable, when it is not on PATH.
	HC string `json:"hc,omitempty"`
	// History is how many scrollback lines new viewers receive.
	History int `json:"history,omitempty"`
}

// Defaults for unset settings.
const (
	DefaultMode    = "view"
	DefaultTTL     = "2h"
	DefaultHistory = 1000
)

// Keys lists the settable keys in display order.
var Keys = []string{"relay", "token", "mode", "ttl", "hc", "history"}

// Load reads config.json; a missing file is an empty config.
func Load(p Paths) (Config, error) {
	var c Config
	b, err := os.ReadFile(p.ConfigFile)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return c, nil
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("%s: %w", p.ConfigFile, err)
	}
	return c, nil
}

// Save writes config.json privately (it may hold a token).
func Save(p Paths, c Config) error {
	if err := os.MkdirAll(filepath.Dir(p.ConfigFile), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(p.ConfigFile, append(b, '\n'), 0o600)
}

// Get returns a setting by key.
func (c Config) Get(key string) (string, error) {
	switch key {
	case "relay":
		return c.Relay, nil
	case "token":
		return c.Token, nil
	case "mode":
		return c.Mode, nil
	case "ttl":
		return c.TTL, nil
	case "hc":
		return c.HC, nil
	case "history":
		if c.History == 0 {
			return "", nil
		}
		return fmt.Sprint(c.History), nil
	}
	return "", fmt.Errorf("unknown setting %q (known: %s)", key, strings.Join(Keys, ", "))
}

// Set changes a setting after validating it; an empty value unsets it.
func (c *Config) Set(key, value string) error {
	value = strings.TrimSpace(value)
	switch key {
	case "relay":
		if value != "" {
			norm, err := NormalizeRelay(value)
			if err != nil {
				return err
			}
			value = norm
		}
		c.Relay = value
	case "token":
		c.Token = value
	case "mode":
		if value != "" && value != "view" && value != "control" {
			return fmt.Errorf("mode must be view or control")
		}
		c.Mode = value
	case "ttl":
		if value != "" {
			if _, err := ParseTTL(value); err != nil {
				return err
			}
		}
		c.TTL = value
	case "hc":
		c.HC = value
	case "history":
		if value == "" {
			c.History = 0
			return nil
		}
		var n int
		if _, err := fmt.Sscan(value, &n); err != nil || n < 0 || n > 100000 {
			return fmt.Errorf("history must be a number of lines from 0 to 100000")
		}
		c.History = n
	default:
		return fmt.Errorf("unknown setting %q (known: %s)", key, strings.Join(Keys, ", "))
	}
	return nil
}

// NormalizeRelay validates a relay base URL and strips trailing slashes.
func NormalizeRelay(s string) (string, error) {
	s = strings.TrimSpace(s)
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid relay URL %q", s)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return "", fmt.Errorf("relay URL must use https:// or http://")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("relay URL must not have a query or fragment")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String(), nil
}

var dayUnit = regexp.MustCompile(`(\d+)d`)

// ParseDuration is time.ParseDuration plus days: "7d", "1d12h".
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	var days int64
	for _, m := range dayUnit.FindAllStringSubmatch(s, -1) {
		n, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil || n > 100000 {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		days += n
	}
	rest := dayUnit.ReplaceAllString(s, "")
	d := time.Duration(days) * 24 * time.Hour
	if rest == "" {
		if days == 0 {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		return d, nil
	}
	more, err := time.ParseDuration(rest)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	return d + more, nil
}

// ParseTTL parses a share lifetime: a duration ("90m", "2h", "7d"), a number
// of minutes, or 0 / "never" / "none" for until stopped.
func ParseTTL(s string) (time.Duration, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	switch s {
	case "0", "never", "none", "forever", "until-stopped":
		return 0, nil
	}
	if d, err := ParseDuration(s); err == nil {
		if d < time.Minute {
			return 0, fmt.Errorf("lifetime %s is shorter than a minute", d)
		}
		return d, nil
	}
	if mins, err := strconv.Atoi(s); err == nil && mins > 0 {
		return time.Duration(mins) * time.Minute, nil
	}
	return 0, fmt.Errorf("invalid lifetime %q (examples: 30m, 2h, 7d, 0 for until stopped)", s)
}

// WriteFileAtomic writes through a temporary file and a rename.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Chmod(perm); err != nil && runtime.GOOS != "windows" {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
