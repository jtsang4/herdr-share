// Package cli implements the herdr-share command line.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/jtsang4/herdr-share/internal/config"
	"github.com/jtsang4/herdr-share/internal/hcclient"
	"github.com/jtsang4/herdr-share/internal/version"
)

type app struct {
	home    string
	asJSON  bool
	stdout  io.Writer
	stderr  io.Writer
	paths   config.Paths
	cfg     config.Config
	loadErr error
}

// Execute runs the CLI and returns the process exit code.
func Execute() int {
	a := &app{stdout: os.Stdout, stderr: os.Stderr}
	root := a.rootCommand()
	if err := root.Execute(); err != nil {
		a.fail(err)
		return 1
	}
	return 0
}

// errSilent is returned after a command already reported its failure.
var errSilent = errors.New("")

func (a *app) fail(err error) {
	if errors.Is(err, errSilent) {
		return
	}
	if a.asJSON {
		_ = json.NewEncoder(a.stdout).Encode(map[string]string{"error": err.Error()})
		return
	}
	fmt.Fprintln(a.stderr, "herdr-share:", err)
}

func (a *app) rootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "herdr-share",
		Short: "Share a running Herdr terminal session through a relay",
		Long: `herdr-share shares one running herdr-core terminal (an agent session, a
shell) with other people through a relay you deploy. Viewers open a link in
their browser; whether they can type is decided when you share and can be
changed at any time. Everything between you and them is end-to-end encrypted.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version.String(),
		PersistentPreRunE: func(*cobra.Command, []string) error {
			p, err := config.Resolve(a.home)
			if err != nil {
				return err
			}
			a.paths = p
			a.cfg, a.loadErr = config.Load(p)
			return nil
		},
	}
	root.SetOut(a.stdout)
	root.SetErr(a.stderr)
	root.PersistentFlags().StringVar(&a.home, "home", "", "use this folder for all herdr-share files (env "+config.EnvHome+")")
	root.PersistentFlags().BoolVar(&a.asJSON, "json", false, "print JSON")
	root.SetVersionTemplate("herdr-share {{.Version}}\n")

	root.AddCommand(
		a.startCommand(),
		a.serveCommand(),
		a.listCommand(),
		a.statusCommand(),
		a.linkCommand(),
		a.modeCommand(),
		a.extendCommand(),
		a.stopCommand(),
		a.configCommand(),
		a.modCommand(),
		a.doctorCommand(),
		a.versionCommand(),
	)
	return root
}

func (a *app) config() (config.Config, error) {
	return a.cfg, a.loadErr
}

func (a *app) hcOptions() hcclient.Options {
	return hcclient.Options{Bin: a.cfg.HC}
}

func (a *app) printJSON(v any) error {
	enc := json.NewEncoder(a.stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func (a *app) versionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if a.asJSON {
				return a.printJSON(map[string]any{
					"version": version.Version, "commit": version.Commit, "date": version.Date,
					"protocol": version.ProtocolVersion,
				})
			}
			fmt.Fprintln(a.stdout, "herdr-share", version.String())
			return nil
		},
	}
}

func humanUntil(t time.Time) string {
	if t.IsZero() {
		return "until stopped"
	}
	d := time.Until(t).Round(time.Minute)
	if d <= 0 {
		return "now"
	}
	local := t.Local().Format("15:04")
	if d >= 24*time.Hour {
		local = t.Local().Format("Jan 2 15:04")
	}
	return fmt.Sprintf("at %s (in %s)", local, shortDuration(d))
}

func shortDuration(d time.Duration) string {
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	switch {
	case h > 0 && m > 0:
		return fmt.Sprintf("%dh%dm", h, m)
	case h > 0:
		return fmt.Sprintf("%dh", h)
	default:
		return fmt.Sprintf("%dm", m)
	}
}

func modeWords(mode string) string {
	if mode == "control" {
		return "viewers can type"
	}
	return "view only"
}
