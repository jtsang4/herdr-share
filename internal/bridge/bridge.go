// Package bridge shares one herdr-core terminal through a relay.
//
// The bridge is the only place where permissions are enforced: guests' input
// reaches the terminal only while the share is in control mode, and every
// byte to and from guests is end-to-end encrypted with the share key, which
// the relay never sees.
package bridge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jtsang4/herdr-share/internal/e2ee"
	"github.com/jtsang4/herdr-share/internal/hcclient"
	"github.com/jtsang4/herdr-share/internal/wire"
)

// End reasons reported to guests and in the share state.
const (
	EndStopped        = "stopped"
	EndExpired        = "expired"
	EndTerminalEnded  = "terminal_ended"
	EndRelayLost      = "relay_lost"
	EndCoreLost       = "core_lost"
	EndBridgeShutdown = "bridge_shutdown"
)

// Status values of a share.
const (
	StatusConnecting   = "connecting"
	StatusLive         = "live"
	StatusReconnecting = "reconnecting"
	StatusEnded        = "ended"
)

// Options configure a share.
type Options struct {
	Terminal string
	Mode     string
	// TTL is the share's lifetime; 0 means until stopped (the relay may cap it).
	TTL      time.Duration
	RelayURL string
	Token    string
	// Label is a human description shown to guests (space and tab names).
	Label string
	// History is how many scrollback lines a new guest sees (default 1000).
	History int
	HC      hcclient.Options
	Logger  *slog.Logger
	// OnChange is called after every state change, from any goroutine.
	OnChange func(State)
	// RelayGiveUp is how long the bridge keeps trying to reach a relay that
	// went away before it ends the share (default 10 minutes).
	RelayGiveUp time.Duration
}

func (o Options) relayGiveUp() time.Duration {
	if o.RelayGiveUp > 0 {
		return o.RelayGiveUp
	}
	return 10 * time.Minute
}

// Guest describes a connected guest.
type Guest struct {
	ID       uint32    `json:"id"`
	Name     string    `json:"name"`
	Client   string    `json:"client,omitempty"`
	JoinedAt time.Time `json:"joined_at"`
}

// State is a snapshot of a share.
type State struct {
	ShareID   string    `json:"share_id"`
	Link      string    `json:"link"`
	Relay     string    `json:"relay"`
	Terminal  string    `json:"terminal"`
	Title     string    `json:"title"`
	Label     string    `json:"label,omitempty"`
	Mode      string    `json:"mode"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at,omitzero"`
	Guests    []Guest   `json:"guests"`
	EndReason string    `json:"end_reason,omitempty"`
	Cols      int       `json:"cols"`
	Rows      int       `json:"rows"`
}

type guest struct {
	id       uint32
	ch       *e2ee.Channel
	name     string
	client   string
	joinedAt time.Time
	ready    bool
	cancel   context.CancelFunc // stops its attach pump
	attached bool
	sendMu   sync.Mutex
}

// Bridge is one live share.
type Bridge struct {
	opts Options
	log  *slog.Logger
	key  e2ee.Key

	mu        sync.Mutex
	hc        *hcclient.Client
	state     State
	secret    string
	guests    map[uint32]*guest
	out       chan []byte // current relay connection's outgoing binary frames
	ctrl      chan wire.Control
	input     *inputAttach
	stopOnce  sync.Once
	stopped   chan struct{}
	stopWhy   string
	expiry    *time.Timer
	ready     chan struct{}
	readyOnce sync.Once
}

// New validates options; Run does the work.
func New(o Options) (*Bridge, error) {
	if o.Terminal == "" {
		return nil, errors.New("no terminal to share")
	}
	if o.Mode == "" {
		o.Mode = wire.ModeView
	}
	if !wire.ValidMode(o.Mode) {
		return nil, fmt.Errorf("unknown mode %q (use %s or %s)", o.Mode, wire.ModeView, wire.ModeControl)
	}
	if _, err := relayEndpoint(o.RelayURL); err != nil {
		return nil, err
	}
	if o.History <= 0 {
		o.History = 1000
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	key, err := e2ee.NewKey()
	if err != nil {
		return nil, err
	}
	return &Bridge{
		opts:    o,
		log:     o.Logger,
		key:     key,
		guests:  map[uint32]*guest{},
		stopped: make(chan struct{}),
		ready:   make(chan struct{}),
		state: State{
			Relay:     strings.TrimRight(o.RelayURL, "/"),
			Terminal:  o.Terminal,
			Label:     o.Label,
			Mode:      o.Mode,
			Status:    StatusConnecting,
			CreatedAt: time.Now().UTC(),
			Guests:    []Guest{},
		},
	}, nil
}

// relayEndpoint turns the relay base URL into its host WebSocket URL.
func relayEndpoint(base string) (string, error) {
	if base == "" {
		return "", errors.New("no relay configured: set one with `herdr-share config set relay https://relay.example.com`")
	}
	u, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid relay URL %q", base)
	}
	switch u.Scheme {
	case "https", "wss":
		u.Scheme = "wss"
	case "http", "ws":
		u.Scheme = "ws"
	default:
		return "", fmt.Errorf("relay URL %q must start with https:// or http://", base)
	}
	u.Path = strings.TrimRight(u.Path, "/") + wire.HostPath
	u.RawQuery, u.Fragment = "", ""
	return u.String(), nil
}

// Ready is closed once the share is live (or has failed to start).
func (b *Bridge) Ready() <-chan struct{} { return b.ready }

// Snapshot returns the current state.
func (b *Bridge) Snapshot() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.snapshotLocked()
}

func (b *Bridge) snapshotLocked() State {
	s := b.state
	s.Guests = []Guest{}
	for _, g := range b.guests {
		if g.ready {
			s.Guests = append(s.Guests, Guest{ID: g.id, Name: g.name, Client: g.client, JoinedAt: g.joinedAt})
		}
	}
	sortGuests(s.Guests)
	return s
}

func sortGuests(gs []Guest) {
	for i := 1; i < len(gs); i++ {
		for j := i; j > 0 && gs[j].ID < gs[j-1].ID; j-- {
			gs[j], gs[j-1] = gs[j-1], gs[j]
		}
	}
}

func (b *Bridge) changed() {
	if b.opts.OnChange != nil {
		b.opts.OnChange(b.Snapshot())
	}
}

// Stop ends the share. It is safe to call more than once.
func (b *Bridge) Stop(reason string) {
	b.stopOnce.Do(func() {
		b.mu.Lock()
		b.stopWhy = reason
		b.mu.Unlock()
		close(b.stopped)
	})
}

// SetMode switches between view and control.
func (b *Bridge) SetMode(mode string) error {
	if !wire.ValidMode(mode) {
		return fmt.Errorf("unknown mode %q", mode)
	}
	b.mu.Lock()
	if b.state.Mode == mode {
		b.mu.Unlock()
		return nil
	}
	b.state.Mode = mode
	// The input attach stays open in view mode (input is dropped before it):
	// detaching a typing viewer would make herdr-core recompute the size.
	b.mu.Unlock()
	b.broadcast(wire.AppMessage{T: wire.AppMode, Mode: mode})
	b.log.Info("mode changed", "mode", mode)
	b.changed()
	return nil
}

// Extend sets a new lifetime counted from now (0: until stopped, subject to
// the relay's maximum).
func (b *Bridge) Extend(ttl time.Duration) error {
	b.mu.Lock()
	ctrl := b.ctrl
	b.mu.Unlock()
	if ctrl == nil {
		return errors.New("the relay is not connected right now; try again shortly")
	}
	select {
	case ctrl <- wire.Control{Type: wire.TypeExtend, TTLSeconds: int64(ttl / time.Second)}:
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("the relay connection is busy; try again")
	}
}

// Run shares the terminal until Stop, expiry, the terminal ending, or a
// fatal error. It returns nil when the share ended normally.
func (b *Bridge) Run(ctx context.Context) (err error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer b.readyOnce.Do(func() { close(b.ready) })

	hc, term, err := b.connectCore(ctx)
	if err != nil {
		return err
	}
	b.mu.Lock()
	b.hc = hc
	b.state.Title = term.DisplayName()
	b.state.Cols, b.state.Rows = term.Cols, term.Rows
	b.mu.Unlock()

	reason := make(chan string, 4)
	end := func(r string) {
		select {
		case reason <- r:
		default:
		}
	}

	// The first relay connection must succeed for the share to exist.
	conn, err := b.dialRelay(ctx, true)
	if err != nil {
		_ = hc.Close()
		return err
	}
	b.readyOnce.Do(func() { close(b.ready) })
	b.changed()
	b.log.Info("share is live", "terminal", b.opts.Terminal, "mode", b.opts.Mode)

	go b.coreLoop(ctx, hc, end)
	go b.relayLoop(ctx, conn, end)

	var why string
	select {
	case <-b.stopped:
		b.mu.Lock()
		why = b.stopWhy
		b.mu.Unlock()
	case why = <-reason:
	case <-ctx.Done():
		why = EndBridgeShutdown
	}
	b.finish(why)
	cancel()
	return nil
}

// finish tells guests and the relay that the share is over.
func (b *Bridge) finish(why string) {
	b.log.Info("share ending", "reason", why)
	b.broadcast(wire.AppMessage{T: wire.AppEnd, Reason: why})
	b.mu.Lock()
	ctrl := b.ctrl
	b.state.Status = StatusEnded
	b.state.EndReason = why
	if b.expiry != nil {
		b.expiry.Stop()
	}
	guests := b.guests
	b.guests = map[uint32]*guest{}
	in := b.input
	b.input = nil
	hc := b.hc
	b.mu.Unlock()
	if ctrl != nil {
		select {
		case ctrl <- wire.Control{Type: wire.TypeClose, Reason: relayReason(why)}:
		case <-time.After(2 * time.Second):
		}
		// Give the writer a moment to flush the goodbye frames.
		time.Sleep(300 * time.Millisecond)
	}
	for _, g := range guests {
		if g.cancel != nil {
			g.cancel()
		}
	}
	if in != nil {
		in.close()
	}
	if hc != nil {
		_ = hc.Close()
	}
	b.changed()
}

func relayReason(why string) string {
	switch why {
	case EndExpired:
		return wire.ReasonExpired
	default:
		return wire.ReasonStopped
	}
}

// ---- herdr-core ----

func (b *Bridge) connectCore(ctx context.Context) (*hcclient.Client, hcclient.Terminal, error) {
	dctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	hc, err := hcclient.Dial(dctx, b.opts.HC)
	if err != nil {
		return nil, hcclient.Terminal{}, fmt.Errorf("connect to herdr-core: %w", err)
	}
	term, err := hc.Get(dctx, b.opts.Terminal)
	if err != nil {
		_ = hc.Close()
		if hcclient.IsKind(err, "not_found") {
			return nil, term, fmt.Errorf("terminal %s not found", b.opts.Terminal)
		}
		return nil, term, err
	}
	if !term.Live() {
		_ = hc.Close()
		return nil, term, fmt.Errorf("terminal %s has finished", b.opts.Terminal)
	}
	return hc, term, nil
}

// coreLoop follows the terminal's events, and reconnects to the core if the
// connection drops (for example when herdr-core restarts after an update).
func (b *Bridge) coreLoop(ctx context.Context, hc *hcclient.Client, end func(string)) {
	for {
		err := b.followEvents(ctx, hc, end)
		if ctx.Err() != nil || err == nil {
			// nil: the terminal ended and the share is ending.
			return
		}
		b.log.Warn("lost herdr-core; reconnecting", "err", err)
		b.resetGuests()
		var term hcclient.Terminal
		deadline := time.Now().Add(2 * time.Minute)
		for delay := 500 * time.Millisecond; ; delay = min(delay*2, 5*time.Second) {
			if time.Now().After(deadline) {
				end(EndCoreLost)
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
			var cerr error
			hc, term, cerr = b.connectCore(ctx)
			if cerr == nil {
				break
			}
			if strings.Contains(cerr.Error(), "not found") || strings.Contains(cerr.Error(), "has finished") {
				end(EndTerminalEnded)
				return
			}
		}
		b.mu.Lock()
		old := b.hc
		b.hc = hc
		b.state.Cols, b.state.Rows = term.Cols, term.Rows
		b.mu.Unlock()
		if old != nil {
			_ = old.Close()
		}
		b.reattachAll()
	}
}

func (b *Bridge) followEvents(ctx context.Context, hc *hcclient.Client, end func(string)) error {
	events, snap, err := hc.Subscribe(ctx, hcclient.SubscribeParams{Terminal: b.opts.Terminal})
	if err != nil {
		return err
	}
	defer events.Close()
	if len(snap) == 0 || !snap[0].Live() {
		end(EndTerminalEnded)
		return nil
	}
	for {
		ev, err := events.Next(ctx)
		if err != nil {
			return err
		}
		switch ev.Method {
		case "missed":
			if len(ev.Terminals) == 0 || !ev.Terminals[0].Live() {
				end(EndTerminalEnded)
				return nil
			}
			b.setSize(ev.Terminals[0].Cols, ev.Terminals[0].Rows, false)
			continue
		case "event":
		default:
			continue
		}
		switch ev.Type {
		case "finished", "removed":
			end(EndTerminalEnded)
			return nil
		case "size":
			var d struct{ Cols, Rows int }
			if decodeJSON(ev.Data, &d) == nil {
				b.setSize(d.Cols, d.Rows, true)
			}
		case "title":
			var d struct{ Title string }
			if decodeJSON(ev.Data, &d) == nil && d.Title != "" {
				b.mu.Lock()
				changed := b.state.Title != d.Title
				b.state.Title = d.Title
				b.mu.Unlock()
				if changed {
					b.broadcast(wire.AppMessage{T: wire.AppTitle, Title: d.Title})
					b.changed()
				}
			}
		case "restarted":
			// A kept terminal got a fresh shell: its old streams closed.
			var t hcclient.Terminal
			if decodeJSON(ev.Data, &t) == nil {
				b.setSize(t.Cols, t.Rows, false)
			}
			b.mu.Lock()
			in := b.input
			b.input = nil
			b.mu.Unlock()
			if in != nil {
				in.close()
			}
			b.reattachAll()
		}
	}
}

// setSize records the terminal grid. Guests whose stream lacks resize
// notifications learn about it here.
func (b *Bridge) setSize(cols, rows int, notify bool) {
	if cols <= 0 || rows <= 0 {
		return
	}
	b.mu.Lock()
	changed := b.state.Cols != cols || b.state.Rows != rows
	b.state.Cols, b.state.Rows = cols, rows
	b.mu.Unlock()
	if changed && notify {
		b.changed()
	}
}

func (b *Bridge) size() (int, int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state.Cols, b.state.Rows
}
