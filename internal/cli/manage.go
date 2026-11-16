package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/jtsang4/herdr-share/internal/bridge"
	"github.com/jtsang4/herdr-share/internal/clipboard"
	"github.com/jtsang4/herdr-share/internal/config"
	"github.com/jtsang4/herdr-share/internal/control"
	"github.com/jtsang4/herdr-share/internal/hcclient"
	"github.com/jtsang4/herdr-share/internal/store"
	"github.com/jtsang4/herdr-share/internal/wire"
)

// findShare resolves a share reference: its id, relay share id, or the
// terminal it shares (address or name).
func (a *app) findShare(ctx context.Context, ref string) (store.Record, error) {
	rec, err := store.Find(a.paths, ref)
	if err == nil || !errors.Is(err, store.ErrNotFound) {
		return rec, err
	}
	// Maybe a terminal name: ask herdr-core for its address.
	hc, herr := hcclient.Dial(ctx, a.hcOptions())
	if herr == nil {
		defer hc.Close()
		if t, gerr := hc.Get(ctx, ref); gerr == nil {
			addr := t.Address
			if i := strings.Index(ref, "/"); i >= 0 && !strings.Contains(addr, "/") {
				addr = ref[:i+1] + addr
			}
			if rec, err := store.Find(a.paths, addr); err == nil {
				return rec, nil
			}
		}
	}
	return store.Record{}, fmt.Errorf("no running share for %q (see `herdr-share list`)", ref)
}

func (a *app) call(ctx context.Context, rec store.Record, req control.Request) (store.Record, error) {
	resp, err := control.Call(ctx, rec.Socket, req)
	if err != nil {
		return rec, err
	}
	var st bridge.State
	if err := json.Unmarshal(resp.State, &st); err != nil {
		return rec, err
	}
	rec.State = st
	return rec, nil
}

func (a *app) listCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List running shares",
		Args:    cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			recs, err := store.Live(a.paths)
			if err != nil {
				return err
			}
			if a.asJSON {
				if recs == nil {
					recs = []store.Record{}
				}
				return a.printJSON(recs)
			}
			if len(recs) == 0 {
				fmt.Fprintln(a.stdout, "No running shares.")
				return nil
			}
			w := tabwriter.NewWriter(a.stdout, 0, 2, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tTERMINAL\tTITLE\tMODE\tVIEWERS\tEXPIRES\tSTATUS")
			for _, r := range recs {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\t%s\n", r.ID, r.Terminal, truncate(r.Title, 32), r.Mode, len(r.Guests), humanUntil(r.ExpiresAt), r.Status)
			}
			return w.Flush()
		},
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func (a *app) statusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status <share|terminal>",
		Short: "Show one share: link, mode, viewers, expiry",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rec, err := a.findShare(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if a.asJSON {
				return a.printJSON(rec)
			}
			a.printRecord(rec)
			return nil
		},
	}
}

func (a *app) printRecord(r store.Record) {
	fmt.Fprintf(a.stdout, "Share %s · %s (%s)\n", r.ID, r.Terminal, r.Title)
	fmt.Fprintf(a.stdout, "  Link:     %s\n", r.Link)
	fmt.Fprintf(a.stdout, "  Mode:     %s (%s)\n", r.Mode, modeWords(r.Mode))
	fmt.Fprintf(a.stdout, "  Expires:  %s\n", humanUntil(r.ExpiresAt))
	fmt.Fprintf(a.stdout, "  Status:   %s\n", r.Status)
	fmt.Fprintf(a.stdout, "  Relay:    %s\n", r.Relay)
	if len(r.Guests) == 0 {
		fmt.Fprintln(a.stdout, "  Viewers:  none")
	} else {
		fmt.Fprintf(a.stdout, "  Viewers:  %d\n", len(r.Guests))
		for _, g := range r.Guests {
			fmt.Fprintf(a.stdout, "    - %s (since %s)\n", g.Name, g.JoinedAt.Local().Format("15:04"))
		}
	}
}

func (a *app) linkCommand() *cobra.Command {
	var copyLink bool
	c := &cobra.Command{
		Use:   "link <share|terminal>",
		Short: "Print (or copy) a share's link",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rec, err := a.findShare(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			copied := false
			if copyLink {
				if err := clipboard.Copy(rec.Link); err != nil {
					return err
				}
				copied = true
			}
			if a.asJSON {
				return a.printJSON(map[string]any{"link": rec.Link, "copied": copied})
			}
			fmt.Fprintln(a.stdout, rec.Link)
			return nil
		},
	}
	c.Flags().BoolVarP(&copyLink, "copy", "c", false, "copy the link to the clipboard")
	return c
}

func (a *app) modeCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "mode <share|terminal> <view|control>",
		Short: "Change whether viewers can type",
		Long: `Change a running share's mode. "view": viewers only watch. "control":
viewers can type into the terminal as you. The change applies at once to
everyone connected.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			mode := args[1]
			switch mode {
			case "read", "readonly", "read-only", "watch":
				mode = wire.ModeView
			case "write", "type", "interactive":
				mode = wire.ModeControl
			}
			if !wire.ValidMode(mode) {
				return fmt.Errorf("mode must be view or control")
			}
			rec, err := a.findShare(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			rec, err = a.call(cmd.Context(), rec, control.Request{Op: control.OpMode, Mode: mode})
			if err != nil {
				return err
			}
			if a.asJSON {
				return a.printJSON(rec)
			}
			fmt.Fprintf(a.stdout, "Share %s is now %s.\n", rec.ID, modeWords(rec.Mode))
			return nil
		},
	}
}

func (a *app) extendCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "extend <share|terminal> <lifetime>",
		Short: "Set how much longer a share lasts, counted from now",
		Long:  "Set a running share's remaining lifetime, counted from now: 30m, 2h, or 0 for until stopped (a relay may cap it).",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ttl, err := config.ParseTTL(args[1])
			if err != nil {
				return err
			}
			rec, err := a.findShare(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			rec, err = a.call(cmd.Context(), rec, control.Request{Op: control.OpExtend, TTLSeconds: int64(ttl / time.Second)})
			if err != nil {
				return err
			}
			if a.asJSON {
				return a.printJSON(rec)
			}
			fmt.Fprintf(a.stdout, "Share %s now expires %s.\n", rec.ID, humanUntil(rec.ExpiresAt))
			return nil
		},
	}
}

func (a *app) stopCommand() *cobra.Command {
	var all bool
	c := &cobra.Command{
		Use:   "stop [share|terminal]",
		Short: "Stop sharing; the link stops working at once",
		Long: `Stop a share. Viewers are disconnected at once and the link stops working
for good. The shared terminal itself keeps running.`,
		Args: func(_ *cobra.Command, args []string) error {
			if all && len(args) > 0 {
				return errors.New("give a share or --all, not both")
			}
			if !all && len(args) != 1 {
				return errors.New("give the share (or its terminal) to stop, or --all")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			var recs []store.Record
			if all {
				var err error
				if recs, err = store.Live(a.paths); err != nil {
					return err
				}
			} else {
				rec, err := a.findShare(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				recs = []store.Record{rec}
			}
			var stopped []store.Record
			var errs []error
			for _, r := range recs {
				r2, err := a.call(cmd.Context(), r, control.Request{Op: control.OpStop})
				if err != nil {
					errs = append(errs, fmt.Errorf("%s: %w", r.ID, err))
					continue
				}
				stopped = append(stopped, r2)
			}
			// Wait briefly for the bridges to finish, so a following `list` is accurate.
			deadline := time.Now().Add(5 * time.Second)
			for _, r := range stopped {
				for time.Now().Before(deadline) {
					if _, alive := store.Alive(r); !alive {
						break
					}
					time.Sleep(100 * time.Millisecond)
				}
			}
			if a.asJSON {
				if stopped == nil {
					stopped = []store.Record{}
				}
				if err := a.printJSON(stopped); err != nil {
					return err
				}
				if len(errs) > 0 {
					return errSilent
				}
				return nil
			}
			for _, r := range stopped {
				fmt.Fprintf(a.stdout, "Stopped sharing %s (%s).\n", r.Terminal, r.ID)
			}
			if all && len(recs) == 0 {
				fmt.Fprintln(a.stdout, "No running shares.")
			}
			return errors.Join(errs...)
		},
	}
	c.Flags().BoolVar(&all, "all", false, "stop every running share")
	return c
}
