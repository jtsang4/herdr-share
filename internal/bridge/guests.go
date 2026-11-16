package bridge

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/jtsang4/herdr-share/internal/e2ee"
	"github.com/jtsang4/herdr-share/internal/hcclient"
	"github.com/jtsang4/herdr-share/internal/version"
	"github.com/jtsang4/herdr-share/internal/wire"
)

const outputChunk = 32 << 10

// sendTo seals and queues under the guest's send lock, so sequence numbers
// reach the guest in order.
func (b *Bridge) sendTo(ctx context.Context, g *guest, plaintext []byte) bool {
	g.sendMu.Lock()
	defer g.sendMu.Unlock()
	sealed, err := g.ch.Seal(plaintext)
	if err != nil {
		return false
	}
	return b.sendRaw(ctx, g.id, append([]byte{wire.EnvFrame}, sealed...))
}

func (b *Bridge) broadcast(m wire.AppMessage) {
	b.mu.Lock()
	var gs []*guest
	for _, g := range b.guests {
		if g.ready {
			gs = append(gs, g)
		}
	}
	b.mu.Unlock()
	pt := wire.EncodeApp(m)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, g := range gs {
		b.sendTo(ctx, g, pt)
	}
}

func (b *Bridge) viewers() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, g := range b.guests {
		if g.ready {
			n++
		}
	}
	return n
}

func (b *Bridge) guestJoined(gid uint32) {
	cid, err := e2ee.NewCID()
	if err != nil {
		return
	}
	ch, err := e2ee.NewChannel(b.key, cid, e2ee.ToGuest)
	if err != nil {
		return
	}
	g := &guest{id: gid, ch: ch, joinedAt: time.Now().UTC()}
	b.mu.Lock()
	old := b.guests[gid]
	b.guests[gid] = g
	b.mu.Unlock()
	if old != nil && old.cancel != nil {
		old.cancel()
	}
	sealed, err := ch.Seal(wire.EncodeApp(wire.AppMessage{T: wire.AppInit, Version: version.Version}))
	if err != nil {
		return
	}
	env := make([]byte, 0, 1+e2ee.CIDSize+len(sealed))
	env = append(env, wire.EnvInit)
	env = append(env, cid[:]...)
	env = append(env, sealed...)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	g.sendMu.Lock()
	b.sendRaw(ctx, gid, env)
	g.sendMu.Unlock()
}

func (b *Bridge) guestLeft(gid uint32) {
	b.mu.Lock()
	g := b.guests[gid]
	delete(b.guests, gid)
	b.mu.Unlock()
	if g == nil {
		return
	}
	if g.cancel != nil {
		g.cancel()
	}
	if g.ready {
		b.log.Info("guest left", "guest", gid, "name", g.name)
		b.broadcast(wire.AppMessage{T: wire.AppViewers, Viewers: b.viewers()})
		b.changed()
	}
}

func (b *Bridge) kick(gid uint32, why string) {
	b.log.Warn("disconnecting guest", "guest", gid, "why", why)
	b.sendControl(wire.Control{Type: wire.TypeKick, Guest: gid, Reason: why})
	b.guestLeft(gid)
}

func (b *Bridge) fromGuest(gid uint32, payload []byte) {
	b.mu.Lock()
	g := b.guests[gid]
	b.mu.Unlock()
	if g == nil {
		return
	}
	if len(payload) == 0 || payload[0] != wire.EnvFrame {
		b.kick(gid, "malformed frame")
		return
	}
	pt, err := g.ch.Open(payload[1:])
	if err != nil {
		// Wrong key (a mangled link) or a tampering relay.
		b.kick(gid, "failed authentication")
		return
	}
	kind, body, msg, err := wire.DecodePlain(pt)
	if err != nil {
		return
	}
	switch kind {
	case wire.MsgControl:
		if msg.T == wire.AppHello {
			b.guestHello(g, msg)
		}
	case wire.MsgInput:
		b.mu.Lock()
		ok := g.ready && b.state.Mode == wire.ModeControl && b.state.Status != StatusEnded
		b.mu.Unlock()
		if ok {
			if err := b.writeInput(body); err != nil {
				b.log.Warn("input not delivered", "err", err)
			}
		}
	}
}

func cleanName(s string, id uint32) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	if r := []rune(s); len(r) > 32 {
		s = string(r[:32])
	}
	if s == "" {
		s = fmt.Sprintf("Guest %d", id)
	}
	return s
}

func (b *Bridge) guestHello(g *guest, msg wire.AppMessage) {
	b.mu.Lock()
	if g.ready || b.guests[g.id] != g {
		b.mu.Unlock()
		return
	}
	g.ready = true
	g.name = cleanName(msg.Name, g.id)
	g.client = cleanName(msg.Client, 0)
	if len(g.client) > 40 {
		g.client = g.client[:40]
	}
	b.mu.Unlock()
	b.log.Info("guest joined", "guest", g.id, "name", g.name)
	b.startPump(g)
	b.broadcast(wire.AppMessage{T: wire.AppViewers, Viewers: b.viewers()})
	b.changed()
}

func (b *Bridge) startPump(g *guest) {
	ctx, cancel := context.WithCancel(context.Background())
	b.mu.Lock()
	if g.cancel != nil {
		g.cancel()
	}
	g.cancel = cancel
	g.attached = true
	b.mu.Unlock()
	go func() {
		defer cancel()
		if err := b.pump(ctx, g); err != nil && ctx.Err() == nil {
			b.log.Warn("guest stream ended", "guest", g.id, "err", err)
		}
		b.mu.Lock()
		if b.guests[g.id] == g {
			g.attached = false
		}
		b.mu.Unlock()
	}()
}

// pump streams the terminal to one guest through its own observe attach,
// so every guest starts from a full first paint.
func (b *Bridge) pump(ctx context.Context, g *guest) error {
	b.mu.Lock()
	hc := b.hc
	b.mu.Unlock()
	if hc == nil {
		return fmt.Errorf("herdr-core not connected")
	}
	cols, rows := b.size()
	st, ar, err := hc.Attach(ctx, hcclient.AttachParams{
		Terminal: b.opts.Terminal, Cols: max(cols, 1), Rows: max(rows, 1),
		Observe: true, ResizeNotifications: true, History: b.opts.History,
	})
	if err != nil {
		return err
	}
	defer st.Close()
	if ar.Terminal.Cols > 0 {
		cols, rows = ar.Terminal.Cols, ar.Terminal.Rows
		b.setSize(cols, rows, false)
	}
	b.mu.Lock()
	welcome := wire.AppMessage{
		T: wire.AppWelcome, Mode: b.state.Mode, Title: b.state.Title, Label: b.state.Label,
		Terminal: b.opts.Terminal, Cols: cols, Rows: rows, Version: version.Version,
	}
	if !b.state.ExpiresAt.IsZero() {
		welcome.ExpiresAt = b.state.ExpiresAt.Unix()
	}
	b.mu.Unlock()
	welcome.Viewers = b.viewers()
	if !b.sendTo(ctx, g, wire.EncodeApp(welcome)) {
		return ctx.Err()
	}
	for {
		f, err := st.Next(ctx)
		if err != nil {
			return err
		}
		switch f.Kind {
		case hcclient.KindData:
			for off := 0; off < len(f.Data); off += outputChunk {
				end := min(off+outputChunk, len(f.Data))
				if !b.sendTo(ctx, g, wire.EncodeData(wire.MsgOutput, f.Data[off:end])) {
					return ctx.Err()
				}
			}
			_ = st.Credit(len(f.Data))
		case hcclient.KindResize:
			if c, r, ok := hcclient.ParseResize(f.Data); ok {
				b.setSize(c, r, true)
				if !b.sendTo(ctx, g, wire.EncodeApp(wire.AppMessage{T: wire.AppResize, Cols: c, Rows: r})) {
					return ctx.Err()
				}
			}
		case hcclient.KindClose:
			return nil
		}
	}
}

// dropGuests forgets every guest (the relay re-announces them on resume).
func (b *Bridge) dropGuests() {
	b.mu.Lock()
	gs := b.guests
	b.guests = map[uint32]*guest{}
	in := b.input
	b.input = nil
	b.mu.Unlock()
	for _, g := range gs {
		if g.cancel != nil {
			g.cancel()
		}
	}
	if in != nil {
		in.close()
	}
}

// resetGuests stops every stream but keeps the guests (core reconnect).
func (b *Bridge) resetGuests() {
	b.mu.Lock()
	var cancels []context.CancelFunc
	for _, g := range b.guests {
		if g.cancel != nil {
			cancels = append(cancels, g.cancel)
			g.cancel = nil
		}
		g.attached = false
	}
	in := b.input
	b.input = nil
	b.mu.Unlock()
	for _, c := range cancels {
		c()
	}
	if in != nil {
		in.close()
	}
}

// reattachAll gives every ready guest a fresh stream (a new welcome resets
// its screen).
func (b *Bridge) reattachAll() {
	b.mu.Lock()
	var gs []*guest
	for _, g := range b.guests {
		if g.ready {
			gs = append(gs, g)
		}
	}
	b.mu.Unlock()
	for _, g := range gs {
		b.startPump(g)
	}
}

// ---- input ----

type inputAttach struct {
	st         *hcclient.Stream
	cols, rows int
	cancel     context.CancelFunc
}

func (in *inputAttach) close() {
	in.cancel()
	_ = in.st.Close()
}

var inputLock sync.Mutex

var focusReports = [][]byte{[]byte("\x1b[I"), []byte("\x1b[O")}

// writeInput types guest input into the terminal through a shared,
// non-observing attach that always declares the terminal's current size,
// so typing never resizes the sharer's view.
func (b *Bridge) writeInput(data []byte) error {
	for _, f := range focusReports {
		data = bytes.ReplaceAll(data, f, nil)
	}
	if len(data) == 0 {
		return nil
	}
	inputLock.Lock()
	defer inputLock.Unlock()
	b.mu.Lock()
	in := b.input
	hc := b.hc
	cols, rows := b.state.Cols, b.state.Rows
	b.mu.Unlock()
	if hc == nil {
		return fmt.Errorf("herdr-core not connected")
	}
	if in == nil {
		ctx, cancel := context.WithCancel(context.Background())
		st, _, err := hc.Attach(ctx, hcclient.AttachParams{Terminal: b.opts.Terminal, Cols: max(cols, 1), Rows: max(rows, 1)})
		if err != nil {
			cancel()
			return err
		}
		in = &inputAttach{st: st, cols: cols, rows: rows, cancel: cancel}
		go func() {
			// Drain and acknowledge its output; it only exists to type.
			for {
				f, err := st.Next(ctx)
				if err != nil {
					break
				}
				if f.Kind == hcclient.KindData {
					_ = st.Credit(len(f.Data))
				}
				if f.Kind == hcclient.KindClose {
					break
				}
			}
			b.mu.Lock()
			if b.input == in {
				b.input = nil
			}
			b.mu.Unlock()
		}()
		b.mu.Lock()
		if b.state.Mode != wire.ModeControl {
			b.mu.Unlock()
			in.close()
			return nil
		}
		b.input = in
		b.mu.Unlock()
	}
	if in.cols != cols || in.rows != rows {
		if err := in.st.Resize(cols, rows); err != nil {
			return err
		}
		in.cols, in.rows = cols, rows
	}
	return in.st.Write(data)
}
