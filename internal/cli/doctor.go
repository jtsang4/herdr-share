package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/jtsang4/herdr-share/internal/hcclient"
	"github.com/jtsang4/herdr-share/internal/version"
	"github.com/jtsang4/herdr-share/mod"
)

type check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

func (a *app) doctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check the relay, herdr-core and the Herdr mod",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			var checks []check
			add := func(name string, ok bool, detail string) {
				checks = append(checks, check{name, ok, detail})
			}

			cfg, err := a.config()
			if err != nil {
				add("config", false, err.Error())
			} else {
				add("config", true, a.paths.ConfigFile)
			}
			relay := cfg.Relay
			if env := os.Getenv("HERDR_SHARE_RELAY"); env != "" {
				relay = env
			}
			if relay == "" {
				add("relay", false, "not set: herdr-share config set relay https://your-relay.example.com")
			} else if err := checkRelay(ctx, relay, firstNonEmpty(os.Getenv(EnvToken), cfg.Token)); err != nil {
				add("relay", false, err.Error())
			} else {
				add("relay", true, relay)
			}

			hctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			hc, err := hcclient.Dial(hctx, a.hcOptions())
			cancel()
			if err != nil {
				add("herdr-core", false, err.Error())
			} else {
				add("herdr-core", true, fmt.Sprintf("hc %s on %s", hc.Hello.Version, hc.Hello.Core.Name))
				_ = hc.Close()
			}

			home, err := AppHome()
			if err == nil {
				dir := filepath.Join(home, "mods", mod.Name)
				b, rerr := os.ReadFile(filepath.Join(dir, "install.json")) //nolint:gosec // the Herdr app folder
				var info installInfo
				switch {
				case rerr != nil:
					add("herdr mod", false, "not installed: herdr-share mod install")
				case json.Unmarshal(b, &info) != nil:
					add("herdr mod", false, "install.json unreadable: herdr-share mod install")
				case info.Version != version.Version:
					add("herdr mod", false, fmt.Sprintf("installed by %s, this is %s: herdr-share mod install", info.Version, version.Version))
				default:
					add("herdr mod", true, dir)
				}
			}

			ok := true
			for _, c := range checks {
				ok = ok && c.OK
			}
			if a.asJSON {
				if err := a.printJSON(map[string]any{"ok": ok, "checks": checks}); err != nil {
					return err
				}
			} else {
				for _, c := range checks {
					mark := "✓"
					if !c.OK {
						mark = "✗"
					}
					fmt.Fprintf(a.stdout, "%s %-11s %s\n", mark, c.Name, c.Detail)
				}
			}
			if !ok {
				return errSilent
			}
			return nil
		},
	}
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}
