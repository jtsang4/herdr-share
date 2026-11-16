package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/jtsang4/herdr-share/internal/config"
	"github.com/jtsang4/herdr-share/internal/version"
	"github.com/jtsang4/herdr-share/mod"
)

// AppHome is the Herdr app's folder: $HERDR_APP_HOME, else
// $XDG_CONFIG_HOME/herdr-app, else ~/.config/herdr-app (as Herdr resolves it).
func AppHome() (string, error) {
	if h := os.Getenv("HERDR_APP_HOME"); h != "" {
		return h, nil
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "herdr-app"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "herdr-app"), nil
}

// installInfo is written beside the mod so it can find this binary and
// its settings, even when Herdr's PATH lacks it.
type installInfo struct {
	Bin     string `json:"bin"`
	Home    string `json:"home,omitempty"`
	Version string `json:"version"`
}

func defaultBinding() string {
	if runtime.GOOS == "darwin" {
		return "cmd-shift-s"
	}
	return "ctrl-shift-s"
}

const bindAction = "mod.open:" + mod.Name

func (a *app) modCommand() *cobra.Command {
	var appHome, bind string
	var noBind bool
	c := &cobra.Command{
		Use:   "mod",
		Short: "Install or remove the Herdr mod (the Share dialog)",
	}
	install := &cobra.Command{
		Use:   "install",
		Short: "Install the Herdr mod and bind a key to open it",
		Long: `Install the herdr-share mod into the Herdr app and bind a key that opens
the Share dialog for the focused terminal (default ` + defaultBinding() + `).

Binding a key edits Herdr's keys.json. Herdr treats an edited keys.json as
yours and stops adding new default shortcuts to it on upgrades; use
--no-bind to leave keys.json alone and open the dialog another way
(herdr-app do mod.open --name herdr-share).`,
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			home := appHome
			if home == "" {
				var err error
				if home, err = AppHome(); err != nil {
					return err
				}
			}
			dir := filepath.Join(home, "mods", mod.Name)
			if err := installModFiles(dir); err != nil {
				return err
			}
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			if r, err := filepath.EvalSymlinks(exe); err == nil {
				exe = r
			}
			info := installInfo{Bin: exe, Home: a.paths.Home, Version: version.Version}
			b, _ := json.MarshalIndent(info, "", "  ")
			if err := config.WriteFileAtomic(filepath.Join(dir, "install.json"), append(b, '\n'), 0o644); err != nil {
				return err
			}
			result := map[string]any{"mod": dir, "bin": exe}
			msg := []string{fmt.Sprintf("Installed the Herdr mod in %s.", dir)}
			if !noBind {
				key := bind
				if key == "" {
					key = defaultBinding()
				}
				bound, note, err := addBinding(filepath.Join(home, "keys.json"), key)
				if err != nil {
					return err
				}
				result["key"] = key
				result["bound"] = bound
				msg = append(msg, note)
			}
			if a.asJSON {
				return a.printJSON(result)
			}
			for _, m := range msg {
				fmt.Fprintln(a.stdout, m)
			}
			if a.cfg.Relay == "" {
				fmt.Fprintln(a.stdout, "Next: set your relay, in the dialog or with `herdr-share config set relay https://...`.")
			}
			return nil
		},
	}
	install.Flags().StringVar(&appHome, "app-home", "", "the Herdr app folder (default $HERDR_APP_HOME or ~/.config/herdr-app)")
	install.Flags().StringVar(&bind, "bind", "", "key that opens the Share dialog (default "+defaultBinding()+")")
	install.Flags().BoolVar(&noBind, "no-bind", false, "do not edit keys.json")

	uninstall := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the Herdr mod and its key binding",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			home := appHome
			if home == "" {
				var err error
				if home, err = AppHome(); err != nil {
					return err
				}
			}
			dir := filepath.Join(home, "mods", mod.Name)
			if err := os.RemoveAll(dir); err != nil {
				return err
			}
			removed, err := removeBinding(filepath.Join(home, "keys.json"))
			if err != nil {
				return err
			}
			if a.asJSON {
				return a.printJSON(map[string]any{"removed": dir, "unbound": removed})
			}
			fmt.Fprintf(a.stdout, "Removed %s.\n", dir)
			if removed {
				fmt.Fprintln(a.stdout, "Removed its key binding from keys.json.")
			}
			return nil
		},
	}
	uninstall.Flags().StringVar(&appHome, "app-home", "", "the Herdr app folder")

	path := &cobra.Command{
		Use:   "path",
		Short: "Print where the mod is (or would be) installed",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			home := appHome
			if home == "" {
				var err error
				if home, err = AppHome(); err != nil {
					return err
				}
			}
			fmt.Fprintln(a.stdout, filepath.Join(home, "mods", mod.Name))
			return nil
		},
	}
	path.Flags().StringVar(&appHome, "app-home", "", "the Herdr app folder")
	c.AddCommand(install, uninstall, path)
	return c
}

// generatedByHerdr are files `herdr-app check` writes into a mod folder;
// Herdr writes its own copies into installed mods.
var generatedByHerdr = map[string]bool{"gpui-kit.d.ts": true, "herdr.d.ts": true, "jsconfig.json": true}

func installModFiles(dir string) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	src, err := fs.Sub(mod.Files, mod.Name)
	if err != nil {
		return err
	}
	return fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if generatedByHerdr[d.Name()] {
			return nil
		}
		target := filepath.Join(dir, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		b, err := fs.ReadFile(src, p)
		if err != nil {
			return err
		}
		return config.WriteFileAtomic(target, b, 0o644)
	})
}

var globalSection = regexp.MustCompile(`"global"\s*:\s*\{`)

// addBinding inserts `"<key>": "mod.open:herdr-share"` into keys.json's
// global section without reformatting the rest of the file.
func addBinding(path, key string) (bool, string, error) {
	b, err := os.ReadFile(path) //nolint:gosec // Herdr's keys.json
	if errors.Is(err, os.ErrNotExist) {
		return false, fmt.Sprintf("Did not bind %s: %s does not exist yet. Open Herdr once, then run this again (or open the dialog with `herdr-app do mod.open --name %s`).", key, path, mod.Name), nil
	}
	if err != nil {
		return false, "", err
	}
	var parsed map[string]map[string]any
	if err := json.Unmarshal(b, &parsed); err != nil {
		return false, "", fmt.Errorf("%s is not valid JSON: %w", path, err)
	}
	for _, keys := range parsed {
		for k, v := range keys {
			if s, ok := v.(string); ok && s == bindAction {
				return true, fmt.Sprintf("%s opens the Share dialog: focus a terminal in Herdr and press it.", k), nil
			}
		}
	}
	if v, taken := parsed["global"][key]; taken {
		return false, fmt.Sprintf("Did not bind %s: keys.json already uses it for %v. Pick another with --bind.", key, v), nil
	}
	loc := globalSection.FindIndex(b)
	var out []byte
	line := fmt.Sprintf("%q: %q", key, bindAction)
	if loc == nil {
		// No global section: add one after the opening brace.
		i := bytes.IndexByte(b, '{')
		if i < 0 {
			return false, "", fmt.Errorf("%s has no JSON object", path)
		}
		out = append(out, b[:i+1]...)
		out = append(out, fmt.Sprintf("\n  \"global\": {\n    %s\n  },", line)...)
		out = append(out, b[i+1:]...)
	} else {
		end := loc[1]
		rest := bytes.TrimLeft(b[end:], " \t\r\n")
		sep := ","
		if len(rest) > 0 && rest[0] == '}' {
			sep = ""
		}
		out = append(out, b[:end]...)
		out = append(out, fmt.Sprintf("\n    %s%s", line, sep)...)
		out = append(out, b[end:]...)
	}
	var check map[string]map[string]any
	if err := json.Unmarshal(out, &check); err != nil || check["global"][key] != bindAction {
		return false, "", fmt.Errorf("could not edit %s safely; add \"%s\": \"%s\" to its global section by hand", path, key, bindAction)
	}
	if err := os.WriteFile(path+".herdr-share-backup", b, 0o600); err != nil { //nolint:gosec // beside Herdr's keys.json
		return false, "", err
	}
	if err := config.WriteFileAtomic(path, out, 0o644); err != nil {
		return false, "", err
	}
	return true, fmt.Sprintf("Bound %s: focus a terminal in Herdr and press it to open the Share dialog (previous keys.json saved as keys.json.herdr-share-backup).", key), nil
}

var bindingLine = regexp.MustCompile(`(?m)^[ \t]*"[^"\n]*"\s*:\s*"` + regexp.QuoteMeta(bindAction) + `"\s*,?[ \t]*\r?\n`)

// removeBinding removes lines binding a key to the Share dialog.
func removeBinding(path string) (bool, error) {
	b, err := os.ReadFile(path) //nolint:gosec // Herdr's keys.json
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	out := bindingLine.ReplaceAll(b, nil)
	if bytes.Equal(out, b) {
		return false, nil
	}
	// Removing the last entry of an object may leave a trailing comma.
	out = regexp.MustCompile(`,(\s*})`).ReplaceAll(out, []byte("$1"))
	var check map[string]any
	if err := json.Unmarshal(out, &check); err != nil {
		return false, fmt.Errorf("could not edit %s safely; remove the %q binding by hand", path, bindAction)
	}
	return true, config.WriteFileAtomic(path, out, 0o644)
}
