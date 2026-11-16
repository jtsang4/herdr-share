package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jtsang4/herdr-share/internal/config"
)

func (a *app) configCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "config",
		Short: "Show or change settings (relay URL, token, defaults)",
		Long: `Settings live in config.json (see "herdr-share config path"):

  relay    the relay's base URL, e.g. https://share.example.com (required)
  token    a token, if the relay requires one to create shares
  mode     default mode for new shares: view or control (default view)
  ttl      default lifetime for new shares: 30m, 2h, 0 = until stopped (default 2h)
  history  scrollback lines new viewers receive (default 1000)
  hc       path to the hc executable, if it is not on PATH`,
	}
	show := &cobra.Command{
		Use:   "show",
		Short: "Print every setting",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			cfg, err := a.config()
			if err != nil {
				return err
			}
			if a.asJSON {
				out := cfg
				hasToken := cfg.Token != ""
				out.Token = ""
				return a.printJSON(struct {
					config.Config
					HasToken   bool   `json:"has_token"`
					ConfigFile string `json:"config_file"`
				}{out, hasToken, a.paths.ConfigFile})
			}
			for _, k := range config.Keys {
				v, _ := cfg.Get(k)
				if k == "token" && v != "" {
					v = "(set)"
				}
				if v == "" {
					v = "-"
				}
				fmt.Fprintf(a.stdout, "%-8s %s\n", k, v)
			}
			return nil
		},
	}
	get := &cobra.Command{
		Use:   "get <key>",
		Short: "Print one setting",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, err := a.config()
			if err != nil {
				return err
			}
			v, err := cfg.Get(args[0])
			if err != nil {
				return err
			}
			if a.asJSON {
				return a.printJSON(map[string]string{args[0]: v})
			}
			fmt.Fprintln(a.stdout, v)
			return nil
		},
	}
	set := &cobra.Command{
		Use:     "set <key> <value>",
		Short:   "Change a setting",
		Example: "  herdr-share config set relay https://share.example.com\n  herdr-share config set token s3cret\n  herdr-share config set ttl 30m",
		Args:    cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			return a.updateConfig(args[0], args[1])
		},
	}
	unset := &cobra.Command{
		Use:   "unset <key>",
		Short: "Remove a setting",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return a.updateConfig(args[0], "")
		},
	}
	path := &cobra.Command{
		Use:   "path",
		Short: "Print where settings and state live",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if a.asJSON {
				return a.printJSON(a.paths)
			}
			fmt.Fprintf(a.stdout, "config  %s\nstate   %s\n", a.paths.ConfigFile, a.paths.StateDir)
			return nil
		},
	}
	c.AddCommand(show, get, set, unset, path)
	return c
}

func (a *app) updateConfig(key, value string) error {
	cfg, err := a.config()
	if err != nil {
		return err
	}
	key = strings.ToLower(strings.TrimSpace(key))
	if err := cfg.Set(key, value); err != nil {
		return err
	}
	if err := config.Save(a.paths, cfg); err != nil {
		return err
	}
	a.cfg = cfg
	v, _ := cfg.Get(key)
	if a.asJSON {
		if key == "token" {
			v = map[bool]string{true: "(set)", false: ""}[v != ""]
		}
		return a.printJSON(map[string]string{key: v})
	}
	switch {
	case value == "":
		fmt.Fprintf(a.stdout, "Unset %s.\n", key)
	case key == "token":
		fmt.Fprintln(a.stdout, "Saved the token.")
	default:
		fmt.Fprintf(a.stdout, "%s = %s\n", key, v)
	}
	return nil
}
