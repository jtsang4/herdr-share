package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jtsang4/herdr-share/internal/config"
	"github.com/jtsang4/herdr-share/internal/control"
	"github.com/jtsang4/herdr-share/internal/store"
	"github.com/jtsang4/herdr-share/internal/version"
	"github.com/jtsang4/herdr-share/mod"
)

// semver parses "1.2.3" (a leading v and a pre-release suffix allowed).
func semver(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// newer reports whether release a is newer than b.
func newer(a, b string) bool {
	va, okA := semver(a)
	vb, okB := semver(b)
	if !okA {
		return false
	}
	if !okB {
		return true
	}
	for i := range va {
		if va[i] != vb[i] {
			return va[i] > vb[i]
		}
	}
	return false
}

// refreshMod updates an installed Herdr mod when this binary is a newer
// release than the one that installed it, so updating herdr-share (npm, the
// install script) also updates the Share dialog. Key bindings are untouched.
func (a *app) refreshMod() {
	if _, ok := semver(version.Version); !ok {
		return // a development build never rewrites an installed mod
	}
	home, err := AppHome()
	if err != nil {
		return
	}
	dir := filepath.Join(home, "mods", mod.Name)
	b, err := os.ReadFile(filepath.Join(dir, "install.json")) //nolint:gosec // the Herdr app folder
	if err != nil {
		return
	}
	var info installInfo
	if json.Unmarshal(b, &info) != nil || !newer(version.Version, info.Version) {
		return
	}
	if err := a.writeMod(home); err == nil {
		fmt.Fprintf(a.stderr, "herdr-share: updated the Herdr mod to %s\n", version.Version)
	}
}

// writeMod installs the mod files and install.json into a Herdr app home.
func (a *app) writeMod(home string) error {
	dir := filepath.Join(home, "mods", mod.Name)
	if err := installModFiles(dir); err != nil {
		return err
	}
	bin, args, err := modCommand()
	if err != nil {
		return err
	}
	info := installInfo{Bin: bin, Args: args, Home: a.paths.Home, Version: version.Version}
	b, _ := json.MarshalIndent(info, "", "  ")
	return config.WriteFileAtomic(filepath.Join(dir, "install.json"), append(b, '\n'), 0o644)
}

// modCommand is how the Herdr mod should run herdr-share, so that it keeps
// working whichever way herdr-share was installed:
//   - through npx: `npx -y herdr-share@<this version>` (served from npx's
//     cache after the first run, and moved forward by refreshMod on updates);
//   - through a global npm install: the launcher, with the node that runs it;
//   - otherwise (install script, a build): this binary.
func modCommand() (string, []string, error) {
	if os.Getenv("HERDR_SHARE_VIA_NPX") == "1" {
		npx := os.Getenv("HERDR_SHARE_NPX")
		if npx == "" {
			npx = "npx"
		}
		spec := "herdr-share"
		if _, ok := semver(version.Version); ok {
			spec += "@" + version.Version
		}
		return npx, []string{"-y", "--prefer-offline", spec}, nil
	}
	if launcher := os.Getenv("HERDR_SHARE_LAUNCHER"); launcher != "" {
		if node := os.Getenv("HERDR_SHARE_NODE"); node != "" {
			return node, []string{launcher}, nil
		}
		return launcher, nil, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", nil, err
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return exe, nil, nil
}

func (a *app) uninstallCommand() *cobra.Command {
	var keepConfig bool
	c := &cobra.Command{
		Use:   "uninstall",
		Short: "Stop all shares and remove the Herdr mod and herdr-share's settings",
		Long: `Remove herdr-share from this computer: stop every running share, remove the
Herdr mod and its key binding, and delete settings and state (unless
--keep-config).

The command itself goes with how you installed it: nothing to do with npx,
npm uninstall -g herdr-share for a global install, or delete the file the
install script wrote (~/.local/bin/herdr-share).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			var done []string

			recs, _ := store.Live(a.paths)
			for _, r := range recs {
				cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
				_, err := control.Call(cctx, r.Socket, control.Request{Op: control.OpStop})
				cancel()
				if err == nil {
					done = append(done, "stopped share "+r.ID+" ("+r.Terminal+")")
				}
			}
			// Let the bridges finish before their files go.
			deadline := time.Now().Add(5 * time.Second)
			for _, r := range recs {
				for time.Now().Before(deadline) {
					if _, alive := store.Alive(r); !alive {
						break
					}
					time.Sleep(100 * time.Millisecond)
				}
			}

			if home, err := AppHome(); err == nil {
				dir := filepath.Join(home, "mods", mod.Name)
				if _, err := os.Stat(dir); err == nil {
					if err := os.RemoveAll(dir); err != nil {
						return err
					}
					done = append(done, "removed the Herdr mod")
				}
				if removed, err := removeBinding(filepath.Join(home, "keys.json")); err == nil && removed {
					done = append(done, "removed its key binding")
				}
			}

			if !keepConfig {
				if a.paths.Home != "" {
					if err := os.RemoveAll(a.paths.Home); err != nil {
						return err
					}
					done = append(done, "deleted "+a.paths.Home)
				} else {
					if err := os.RemoveAll(filepath.Dir(a.paths.ConfigFile)); err == nil {
						done = append(done, "deleted settings "+filepath.Dir(a.paths.ConfigFile))
					}
					if err := os.RemoveAll(a.paths.StateDir); err == nil {
						done = append(done, "deleted state "+a.paths.StateDir)
					}
				}
			}

			if a.asJSON {
				return a.printJSON(map[string]any{"done": done})
			}
			if len(done) == 0 {
				fmt.Fprintln(a.stdout, "Nothing to remove.")
			}
			for _, d := range done {
				fmt.Fprintln(a.stdout, "✓", d)
			}
			fmt.Fprintln(a.stdout, "If you installed the command globally, remove it too: npm uninstall -g herdr-share")
			return nil
		},
	}
	c.Flags().BoolVar(&keepConfig, "keep-config", false, "keep the relay address, token and other settings")
	return c
}
