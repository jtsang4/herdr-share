// Package e2e runs a relay, a bridge and guests against a real herdr-core.
//
// It needs an hc core; run it against an isolated session so it never
// touches your own terminals:
//
//	HERDR_SHARE_HC_TEST=1 HC_SESSION=hstest XDG_STATE_HOME=$(mktemp -d) go test ./test/e2e/
package e2e

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jtsang4/herdr-share/internal/bridge"
	"github.com/jtsang4/herdr-share/internal/guest"
	"github.com/jtsang4/herdr-share/internal/hcclient"
	"github.com/jtsang4/herdr-share/internal/relay"
	"github.com/jtsang4/herdr-share/internal/wire"
)

func needCore(t *testing.T) *hcclient.Client {
	t.Helper()
	if os.Getenv("HERDR_SHARE_HC_TEST") == "" {
		t.Skip("set HERDR_SHARE_HC_TEST=1 (with an isolated HC_SESSION) to run end-to-end tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := hcclient.Dial(ctx, hcclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// echoTerminal starts a terminal that echoes lines back with a prefix.
func echoTerminal(t *testing.T, hc *hcclient.Client) hcclient.Terminal {
	t.Helper()
	ctx := context.Background()
	term, err := hc.Run(ctx, hcclient.RunParams{
		Argv:     []string{"sh", "-c", "echo READY-$((6*7)); while IFS= read -r l; do echo \"got:$l\"; done"},
		Name:     fmt.Sprintf("e2e-%d", time.Now().UnixNano()%1e9),
		NoParent: true, NoNotify: true,
		Size: &struct {
			Cols int `json:"cols"`
			Rows int `json:"rows"`
		}{100, 30},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = hc.Kill(context.Background(), term.Address)
		time.Sleep(100 * time.Millisecond)
		_ = hc.Remove(context.Background(), term.Address)
	})
	return term
}

type relayEnv struct {
	srv   *relay.Server
	http  *httptest.Server
	proxy *cutProxy
	url   string
}

func startRelay(t *testing.T, cfg relay.Config) *relayEnv {
	t.Helper()
	srv := relay.New(cfg)
	hs := httptest.NewServer(srv)
	t.Cleanup(hs.Close)
	p := newCutProxy(t, strings.TrimPrefix(hs.URL, "http://"))
	return &relayEnv{srv: srv, http: hs, proxy: p, url: "http://" + p.addr}
}

func startBridge(t *testing.T, o bridge.Options) (*bridge.Bridge, chan error) {
	t.Helper()
	b, err := bridge.New(o)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- b.Run(context.Background()) }()
	select {
	case <-b.Ready():
	case <-time.After(20 * time.Second):
		t.Fatal("bridge not ready")
	}
	if b.Snapshot().Link == "" {
		t.Fatalf("bridge failed: %v", <-done)
	}
	t.Cleanup(func() {
		b.Stop(bridge.EndStopped)
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	return b, done
}

// viewer collects a guest's events in the background.
type viewer struct {
	c      *guest.Client
	mu     sync.Mutex
	out    bytes.Buffer
	apps   []wire.AppMessage
	relays []wire.Control
	err    error
	wake   chan struct{}
}

func join(t *testing.T, link, name string) *viewer {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := guest.Dial(ctx, link, name, nil)
	if err != nil {
		t.Fatal(err)
	}
	v := &viewer{c: c, wake: make(chan struct{}, 1)}
	go func() {
		for {
			ev, err := c.Next(context.Background())
			v.mu.Lock()
			if err != nil {
				v.err = err
				v.mu.Unlock()
				v.poke()
				return
			}
			switch ev.Kind {
			case "output":
				v.out.Write(ev.Output)
			case "app":
				if ev.App.T == wire.AppWelcome {
					v.out.Reset()
				}
				v.apps = append(v.apps, ev.App)
			case "relay":
				v.relays = append(v.relays, ev.Relay)
			}
			v.mu.Unlock()
			v.poke()
		}
	}()
	t.Cleanup(func() { _ = c.Close() })
	return v
}

func (v *viewer) poke() {
	select {
	case v.wake <- struct{}{}:
	default:
	}
}

func (v *viewer) waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.After(15 * time.Second)
	for {
		v.mu.Lock()
		ok := cond()
		v.mu.Unlock()
		if ok {
			return
		}
		select {
		case <-v.wake:
		case <-time.After(100 * time.Millisecond):
		case <-deadline:
			v.mu.Lock()
			defer v.mu.Unlock()
			t.Fatalf("timed out waiting for %s; output %q; apps %+v; relay %+v; err %v", what, v.out.String(), v.apps, v.relays, v.err)
		}
	}
}

func (v *viewer) waitOutput(t *testing.T, s string) {
	t.Helper()
	v.waitFor(t, "output "+s, func() bool { return strings.Contains(v.out.String(), s) })
}

func (v *viewer) waitApp(t *testing.T, typ string, match func(wire.AppMessage) bool) wire.AppMessage {
	t.Helper()
	var got wire.AppMessage
	v.waitFor(t, "app "+typ, func() bool {
		for _, m := range v.apps {
			if m.T == typ && (match == nil || match(m)) {
				got = m
				return true
			}
		}
		return false
	})
	return got
}

func (v *viewer) waitClosed(t *testing.T) {
	t.Helper()
	v.waitFor(t, "disconnect", func() bool { return v.err != nil })
}

func screen(t *testing.T, hc *hcclient.Client, term string) string {
	t.Helper()
	s, err := hc.Read(context.Background(), term, 200)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestShareLifecycle(t *testing.T) {
	hc := needCore(t)
	term := echoTerminal(t, hc)
	env := startRelay(t, relay.Config{})
	var changes []bridge.State
	var chMu sync.Mutex
	b, done := startBridge(t, bridge.Options{
		Terminal: term.Address, Mode: wire.ModeView, TTL: time.Hour, RelayURL: env.url, Label: "e2e · lifecycle",
		OnChange: func(s bridge.State) { chMu.Lock(); changes = append(changes, s); chMu.Unlock() },
	})
	st := b.Snapshot()
	if !strings.Contains(st.Link, "/s/"+st.ShareID+"#") {
		t.Fatalf("link %q", st.Link)
	}
	if st.ExpiresAt.IsZero() {
		t.Fatal("no expiry")
	}

	// A guest sees the screen, including what was printed before it joined.
	alice := join(t, st.Link, "Alice")
	w := alice.waitApp(t, wire.AppWelcome, nil)
	if w.Mode != wire.ModeView || w.Label != "e2e · lifecycle" || w.Cols != 100 || w.Rows != 30 {
		t.Fatalf("welcome %+v", w)
	}
	alice.waitOutput(t, "READY-42")

	// In view mode, input never reaches the terminal.
	if err := alice.c.Type(context.Background(), []byte("view-only-input\r")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(800 * time.Millisecond)
	if strings.Contains(screen(t, hc, term.Address), "view-only-input") {
		t.Fatal("view-mode input reached the terminal")
	}

	// Switching to control lets the guest type, without resizing the terminal.
	if err := b.SetMode(wire.ModeControl); err != nil {
		t.Fatal(err)
	}
	alice.waitApp(t, wire.AppMode, func(m wire.AppMessage) bool { return m.Mode == wire.ModeControl })
	if err := alice.c.Type(context.Background(), []byte("hello from alice\r")); err != nil {
		t.Fatal(err)
	}
	alice.waitOutput(t, "got:hello from alice")
	if g, _ := hc.Get(context.Background(), term.Address); g.Cols != 100 || g.Rows != 30 {
		t.Fatalf("guest typing resized the terminal to %dx%d", g.Cols, g.Rows)
	}

	// A second guest is counted; the state lists both by name.
	bob := join(t, st.Link, "Bob")
	bob.waitApp(t, wire.AppWelcome, func(m wire.AppMessage) bool { return m.Mode == wire.ModeControl })
	bob.waitOutput(t, "got:hello from alice")
	alice.waitApp(t, wire.AppViewers, func(m wire.AppMessage) bool { return m.Viewers == 2 })
	if gs := b.Snapshot().Guests; len(gs) != 2 || gs[0].Name != "Alice" || gs[1].Name != "Bob" {
		t.Fatalf("guests %+v", gs)
	}

	// Back to view: input is dropped again.
	if err := b.SetMode(wire.ModeView); err != nil {
		t.Fatal(err)
	}
	bob.waitApp(t, wire.AppMode, func(m wire.AppMessage) bool { return m.Mode == wire.ModeView })
	_ = bob.c.Type(context.Background(), []byte("after-revoke\r"))
	time.Sleep(800 * time.Millisecond)
	if strings.Contains(screen(t, hc, term.Address), "after-revoke") {
		t.Fatal("input after revoking control reached the terminal")
	}

	// A guest that sends a frame it cannot have sealed is disconnected.
	if err := bob.c.SendRaw(context.Background(), append([]byte{wire.EnvFrame}, bytes.Repeat([]byte{1}, 64)...)); err != nil {
		t.Fatal(err)
	}
	bob.waitClosed(t)
	alice.waitApp(t, wire.AppViewers, func(m wire.AppMessage) bool { return m.Viewers == 1 })

	// Extending the share tells guests the new expiry.
	if err := b.Extend(3 * time.Hour); err != nil {
		t.Fatal(err)
	}
	alice.waitApp(t, wire.AppExpires, func(m wire.AppMessage) bool {
		return m.ExpiresAt > time.Now().Add(2*time.Hour).Unix()
	})

	// Stopping ends the share for everyone, at once and for good.
	b.Stop(bridge.EndStopped)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
		done <- nil // for the cleanup
	case <-time.After(10 * time.Second):
		t.Fatal("bridge did not stop")
	}
	alice.waitApp(t, wire.AppEnd, func(m wire.AppMessage) bool { return m.Reason == bridge.EndStopped })
	alice.waitClosed(t)
	late := join(t, st.Link, "Late")
	late.waitFor(t, "not found", func() bool {
		for _, r := range late.relays {
			if r.Type == wire.TypeEnded && r.Reason == wire.ReasonNotFound {
				return true
			}
		}
		return false
	})
	if shares, _ := env.srv.Stats(); shares != 0 {
		t.Fatalf("relay still has %d shares", shares)
	}
	// The shared terminal keeps running.
	if g, _ := hc.Get(context.Background(), term.Address); !g.Live() {
		t.Fatal("stopping the share ended the terminal")
	}
	chMu.Lock()
	defer chMu.Unlock()
	if len(changes) == 0 || changes[len(changes)-1].Status != bridge.StatusEnded {
		t.Fatalf("last state change %+v", changes)
	}
}

func TestShareEndsWithTerminal(t *testing.T) {
	hc := needCore(t)
	term := echoTerminal(t, hc)
	env := startRelay(t, relay.Config{})
	b, done := startBridge(t, bridge.Options{Terminal: term.Address, RelayURL: env.url, TTL: time.Hour})
	v := join(t, b.Snapshot().Link, "")
	v.waitOutput(t, "READY-42")
	if err := hc.Kill(context.Background(), term.Address); err != nil {
		t.Fatal(err)
	}
	v.waitApp(t, wire.AppEnd, func(m wire.AppMessage) bool { return m.Reason == bridge.EndTerminalEnded })
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
		done <- nil
	case <-time.After(10 * time.Second):
		t.Fatal("bridge kept running after the terminal ended")
	}
}

func TestShareExpires(t *testing.T) {
	hc := needCore(t)
	term := echoTerminal(t, hc)
	env := startRelay(t, relay.Config{})
	b, done := startBridge(t, bridge.Options{Terminal: term.Address, RelayURL: env.url, TTL: 3 * time.Second})
	v := join(t, b.Snapshot().Link, "")
	v.waitOutput(t, "READY-42")
	v.waitFor(t, "expiry", func() bool {
		for _, m := range v.apps {
			if m.T == wire.AppEnd && m.Reason == bridge.EndExpired {
				return true
			}
		}
		for _, r := range v.relays {
			if r.Type == wire.TypeEnded && r.Reason == wire.ReasonExpired {
				return true
			}
		}
		return false
	})
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
		done <- nil
	case <-time.After(10 * time.Second):
		t.Fatal("bridge kept running after expiry")
	}
}

func TestBridgeResumesAfterRelayConnectionDrops(t *testing.T) {
	hc := needCore(t)
	term := echoTerminal(t, hc)
	env := startRelay(t, relay.Config{ResumeGrace: 30 * time.Second})
	b, _ := startBridge(t, bridge.Options{Terminal: term.Address, RelayURL: env.url, TTL: time.Hour, Mode: wire.ModeControl})
	// The guest connects to the relay directly, so only the bridge's link drops.
	link := strings.Replace(b.Snapshot().Link, env.url, env.http.URL, 1)
	v := join(t, link, "Resilient")
	v.waitOutput(t, "READY-42")

	env.proxy.cut()
	v.waitFor(t, "host offline", func() bool {
		for _, r := range v.relays {
			if r.Type == wire.TypeHostOffline {
				return true
			}
		}
		return false
	})
	// The bridge resumes; the guest gets a fresh welcome and a full screen.
	v.waitFor(t, "second welcome", func() bool {
		n := 0
		for _, m := range v.apps {
			if m.T == wire.AppWelcome {
				n++
			}
		}
		return n >= 2
	})
	v.waitOutput(t, "READY-42")
	if err := v.c.Type(context.Background(), []byte("after resume\r")); err != nil {
		t.Fatal(err)
	}
	v.waitOutput(t, "got:after resume")
	if s := b.Snapshot(); s.Status != bridge.StatusLive || len(s.Guests) != 1 {
		t.Fatalf("state after resume: %+v", s)
	}
}

func TestRelayRequiresToken(t *testing.T) {
	hc := needCore(t)
	term := echoTerminal(t, hc)
	env := startRelay(t, relay.Config{Tokens: []string{"right"}})
	b, err := bridge.New(bridge.Options{Terminal: term.Address, RelayURL: env.url, Token: "wrong"})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("wrong token: %v", err)
	}
	ok, _ := startBridge(t, bridge.Options{Terminal: term.Address, RelayURL: env.url, Token: "right"})
	if ok.Snapshot().Link == "" {
		t.Fatal("right token refused")
	}
}

// cutProxy forwards TCP connections and can drop them all at once.
type cutProxy struct {
	ln     net.Listener
	addr   string
	mu     sync.Mutex
	conns  []net.Conn
	target string
}

func newCutProxy(t *testing.T, target string) *cutProxy {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	p := &cutProxy{ln: ln, addr: ln.Addr().String(), target: target}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			u, err := net.Dial("tcp", target)
			if err != nil {
				_ = c.Close()
				continue
			}
			p.mu.Lock()
			p.conns = append(p.conns, c, u)
			p.mu.Unlock()
			go func() { _, _ = io.Copy(u, c); u.Close() }()
			go func() { _, _ = io.Copy(c, u); c.Close() }()
		}
	}()
	return p
}

func (p *cutProxy) cut() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.conns {
		_ = c.Close()
	}
	p.conns = nil
}

func TestShareEndsWhenRelayStaysUnreachable(t *testing.T) {
	hc := needCore(t)
	term := echoTerminal(t, hc)
	env := startRelay(t, relay.Config{})
	b, done := startBridge(t, bridge.Options{Terminal: term.Address, RelayURL: env.url, TTL: time.Hour, RelayGiveUp: 2 * time.Second})
	_ = b
	// Take the relay away for good: the proxy refuses new connections.
	env.proxy.close()
	env.proxy.cut()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
		done <- nil
	case <-time.After(20 * time.Second):
		t.Fatal("bridge kept waiting for a relay that is gone")
	}
	if s := b.Snapshot(); s.EndReason != bridge.EndRelayLost {
		t.Fatalf("end reason %q", s.EndReason)
	}
}

// close stops accepting connections.
func (p *cutProxy) close() { _ = p.ln.Close() }
