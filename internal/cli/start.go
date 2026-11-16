package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jtsang4/herdr-share/internal/clipboard"
	"github.com/jtsang4/herdr-share/internal/config"
	"github.com/jtsang4/herdr-share/internal/hcclient"
	"github.com/jtsang4/herdr-share/internal/store"
	"github.com/jtsang4/herdr-share/internal/version"
	"github.com/jtsang4/herdr-share/internal/wire"
)

// EnvToken passes a relay token to a detached bridge without putting it on
// a command line.
const EnvToken = "HERDR_SHARE_TOKEN" //nolint:gosec // an environment variable name, not a credential

type shareFlags struct {
	mode     string
	ttl      string
	relay    string
	token    string
	label    string
	history  int
	copyLink bool
	fg       bool
	timeout  time.Duration
}

func (f *shareFlags) register(c *cobra.Command) {
	c.Flags().StringVarP(&f.mode, "mode", "m", "", "view (watch only) or control (viewers can type); default from config, else view")
	c.Flags().StringVarP(&f.ttl, "ttl", "t", "", "how long the link works: 30m, 2h, 0 for until stopped; default from config, else 2h")
	c.Flags().StringVar(&f.relay, "relay", "", "relay base URL (default from config)")
	c.Flags().StringVar(&f.token, "token", "", "relay token (default from config or env "+EnvToken+")")
	c.Flags().StringVar(&f.label, "label", "", "a description shown to viewers, e.g. the space and tab")
	c.Flags().IntVar(&f.history, "history", 0, "scrollback lines new viewers receive (default from config, else 1000)")
}

type resolved struct {
	mode    string
	ttl     time.Duration
	relay   string
	token   string
	history int
}

func (a *app) resolveShare(f shareFlags) (resolved, error) {
	cfg, err := a.config()
	if err != nil {
		return resolved{}, err
	}
	r := resolved{mode: f.mode, relay: f.relay, token: f.token, history: f.history}
	if r.mode == "" {
		r.mode = cfg.Mode
	}
	if r.mode == "" {
		r.mode = config.DefaultMode
	}
	if !wire.ValidMode(r.mode) {
		return r, fmt.Errorf("mode must be view or control, not %q", r.mode)
	}
	ttl := f.ttl
	if ttl == "" {
		ttl = cfg.TTL
	}
	if ttl == "" {
		ttl = config.DefaultTTL
	}
	if r.ttl, err = config.ParseTTL(ttl); err != nil {
		return r, err
	}
	if r.relay == "" {
		r.relay = os.Getenv("HERDR_SHARE_RELAY")
	}
	if r.relay == "" {
		r.relay = cfg.Relay
	}
	if r.relay == "" {
		return r, errors.New("no relay configured: run `herdr-share config set relay https://your-relay.example.com` (deploy one with the herdr-share-relay image)")
	}
	if r.relay, err = config.NormalizeRelay(r.relay); err != nil {
		return r, err
	}
	if r.token == "" {
		r.token = os.Getenv(EnvToken)
	}
	if r.token == "" {
		r.token = cfg.Token
	}
	if r.history <= 0 {
		r.history = cfg.History
	}
	if r.history <= 0 {
		r.history = config.DefaultHistory
	}
	return r, nil
}

func newID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// checkRelay asks the relay for its info, for an early, clear error.
func checkRelay(ctx context.Context, relay, token string) error {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, relay+wire.InfoPath, nil) //nolint:gosec // the relay URL is the user's own setting
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "herdr-share/"+version.Version)
	resp, err := http.DefaultClient.Do(req) //nolint:gosec // the relay URL is the user's own setting
	if err != nil {
		return fmt.Errorf("cannot reach relay %s: %w", relay, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s does not look like a herdr-share relay (HTTP %d from %s)", relay, resp.StatusCode, wire.InfoPath)
	}
	var info struct {
		Name         string `json:"name"`
		Protocol     int    `json:"protocol"`
		AuthRequired bool   `json:"auth_required"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil || info.Name != "herdr-share-relay" {
		return fmt.Errorf("%s does not look like a herdr-share relay", relay)
	}
	if info.Protocol != version.ProtocolVersion {
		return fmt.Errorf("relay speaks protocol %d, this herdr-share speaks %d: update the older one", info.Protocol, version.ProtocolVersion)
	}
	if info.AuthRequired && token == "" {
		return errors.New("this relay requires a token: run `herdr-share config set token <token>`")
	}
	return nil
}

func (a *app) startCommand() *cobra.Command {
	var f shareFlags
	c := &cobra.Command{
		Use:   "start <terminal>",
		Short: "Start sharing a terminal and print its link",
		Long: `Start sharing a herdr-core terminal (an address like t7, machine/t7, or a
terminal name). The bridge keeps running in the background as its own hc
terminal until you stop the share, it expires, or the terminal ends.

If the terminal is already shared, its existing share is shown instead.`,
		Example: `  herdr-share start t7
  herdr-share start t7 --mode control --ttl 30m --copy
  herdr-share start review --label "voltdance · review" --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.start(cmd.Context(), args[0], f)
		},
	}
	f.register(c)
	c.Flags().BoolVarP(&f.copyLink, "copy", "c", false, "copy the link to the clipboard")
	c.Flags().BoolVar(&f.fg, "foreground", false, "run the bridge in this process instead of a background hc terminal")
	c.Flags().DurationVar(&f.timeout, "timeout", 30*time.Second, "how long to wait for the share to go live")
	return c
}

func (a *app) start(ctx context.Context, ref string, f shareFlags) error {
	if ctx == nil {
		ctx = context.Background()
	}
	r, err := a.resolveShare(f)
	if err != nil {
		return err
	}
	if err := a.paths.EnsureState(); err != nil {
		return err
	}
	hc, err := hcclient.Dial(ctx, a.hcOptions())
	if err != nil {
		return fmt.Errorf("connect to herdr-core: %w", err)
	}
	defer hc.Close()
	term, err := hc.Get(ctx, ref)
	if err != nil {
		if hcclient.IsKind(err, "not_found") {
			return fmt.Errorf("no terminal %q (see `hc ls`)", ref)
		}
		return err
	}
	if !term.Live() {
		return fmt.Errorf("terminal %s has finished", term.Address)
	}
	if strings.HasPrefix(term.Labels["herdr-share"], "bridge") {
		return errors.New("that terminal is a herdr-share bridge; share the session itself")
	}
	address := term.Address
	if strings.Contains(ref, "/") && !strings.Contains(address, "/") {
		// A remote terminal: keep its machine prefix.
		address = ref[:strings.Index(ref, "/")+1] + address
	}

	if existing, err := store.Find(a.paths, address); err == nil {
		return a.reportStart(existing, f.copyLink, true)
	}
	if err := checkRelay(ctx, r.relay, r.token); err != nil {
		return err
	}

	id := newID()
	if f.fg {
		return a.serve(ctx, serveArgs{id: id, terminal: address, r: r, label: f.label, printLink: true, copyLink: f.copyLink})
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolvedExe, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolvedExe
	}
	ready := filepath.Join(a.paths.RunDir(), id+".ready.json")
	_ = os.Remove(ready)
	ttlArg := "0"
	if r.ttl > 0 {
		ttlArg = strconv.FormatInt(int64(r.ttl/time.Second), 10) + "s"
	}
	argv := []string{exe, "serve",
		"--id", id, "--terminal", address,
		"--mode", r.mode, "--ttl", ttlArg,
		"--relay", r.relay, "--history", strconv.Itoa(r.history),
		"--ready-file", ready,
	}
	if f.label != "" {
		argv = append(argv, "--label", f.label)
	}
	env := map[string]string{}
	if a.paths.Home != "" {
		env[config.EnvHome] = a.paths.Home
	}
	if r.token != "" {
		env[EnvToken] = r.token
	}
	if a.cfg.HC != "" {
		env["HC_BIN"] = a.cfg.HC
	}
	bt, err := hc.Run(ctx, hcclient.RunParams{
		Argv:     argv,
		Name:     "herdr-share-" + id,
		Labels:   map[string]string{"herdr-share": "bridge", "herdr-share.terminal": address, "herdr-share.id": id},
		Env:      env,
		NoParent: true,
		NoNotify: true,
		Remove:   true,
		Size: &struct {
			Cols int `json:"cols"`
			Rows int `json:"rows"`
		}{120, 40},
	})
	if err != nil {
		return fmt.Errorf("start the bridge: %w", err)
	}

	deadline := time.Now().Add(f.timeout)
	for {
		if b, err := os.ReadFile(ready); err == nil && len(b) > 0 { //nolint:gosec // our own state folder
			_ = os.Remove(ready)
			var res readyFile
			if err := json.Unmarshal(b, &res); err != nil {
				return fmt.Errorf("bridge: unreadable ready file: %w", err)
			}
			if res.Error != "" {
				return errors.New(res.Error)
			}
			return a.reportStart(res.Record, f.copyLink, false)
		}
		t, err := hc.Get(ctx, bt.Address)
		if hcclient.IsKind(err, "not_found") {
			// Removed after it exited; its ready file may have just appeared.
			if b, rerr := os.ReadFile(ready); rerr == nil && len(b) > 0 { //nolint:gosec // our own state folder
				continue
			}
			return fmt.Errorf("the bridge exited before the share went live; see %s", filepath.Join(a.paths.LogDir(), id+".log"))
		}
		if err == nil && !t.Live() {
			out, _ := hc.Read(ctx, bt.Address, 50)
			_ = hc.Remove(ctx, bt.Address)
			return fmt.Errorf("the bridge exited before the share went live:\n%s", strings.TrimSpace(out))
		}
		if time.Now().After(deadline) {
			_ = hc.Kill(ctx, bt.Address)
			return fmt.Errorf("the share did not go live within %s (bridge terminal %s)", f.timeout, bt.Address)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

type readyFile struct {
	Error  string       `json:"error,omitempty"`
	Record store.Record `json:"record"`
}

func (a *app) reportStart(rec store.Record, copyLink, existing bool) error {
	copied := false
	if copyLink {
		copied = clipboard.Copy(rec.Link) == nil
	}
	if a.asJSON {
		return a.printJSON(struct {
			store.Record
			Existing bool `json:"existing"`
			Copied   bool `json:"copied"`
		}{rec, existing, copied})
	}
	verb := "Sharing"
	if existing {
		verb = "Already sharing"
	}
	fmt.Fprintf(a.stdout, "%s %s (%s): %s, expires %s.\n", verb, rec.Terminal, rec.Title, modeWords(rec.Mode), humanUntil(rec.ExpiresAt))
	fmt.Fprintf(a.stdout, "\n  %s\n\n", rec.Link)
	if copied {
		fmt.Fprintln(a.stdout, "The link is on your clipboard.")
	}
	fmt.Fprintf(a.stdout, "Anyone with the link can %s. Change it: herdr-share mode %s control|view · stop: herdr-share stop %s\n",
		map[bool]string{true: "type into this terminal as you", false: "watch this terminal"}[rec.Mode == wire.ModeControl], rec.ID, rec.ID)
	return nil
}

// ---- serve: the bridge process ----

type serveArgs struct {
	id        string
	terminal  string
	r         resolved
	label     string
	readyFile string
	printLink bool
	copyLink  bool
}

func (a *app) serveCommand() *cobra.Command {
	var f shareFlags
	var id, terminal, readyPath string
	c := &cobra.Command{
		Use:    "serve",
		Short:  "Run a share's bridge in the foreground (used by start)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := a.resolveShare(f)
			if err != nil {
				writeReady(readyPath, readyFile{Error: err.Error()})
				return err
			}
			if id == "" {
				id = newID()
			}
			return a.serve(cmd.Context(), serveArgs{id: id, terminal: terminal, r: r, label: f.label, readyFile: readyPath})
		},
	}
	f.register(c)
	c.Flags().StringVar(&id, "id", "", "local share id")
	c.Flags().StringVar(&terminal, "terminal", "", "terminal to share")
	c.Flags().StringVar(&readyPath, "ready-file", "", "write the share record (or error) here once live")
	_ = c.MarkFlagRequired("terminal")
	return c
}

func writeReady(path string, r readyFile) {
	if path == "" {
		return
	}
	b, _ := json.Marshal(r)
	_ = config.WriteFileAtomic(path, b, 0o600)
}
