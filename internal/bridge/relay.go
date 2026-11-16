package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/jtsang4/herdr-share/internal/version"
	"github.com/jtsang4/herdr-share/internal/wire"
)

const relayReadLimit = 4 << 20

// relayConn is one WebSocket to the relay.
type relayConn struct {
	ws   *websocket.Conn
	out  chan []byte
	ctrl chan wire.Control
}

// errPermanent marks relay answers that retrying cannot fix.
type errPermanent struct{ err error }

func (e errPermanent) Error() string { return e.err.Error() }
func (e errPermanent) Unwrap() error { return e.err }

func decodeJSON(raw json.RawMessage, v any) error { return json.Unmarshal(raw, v) }

// dialRelay connects and creates the share (first) or resumes it.
func (b *Bridge) dialRelay(ctx context.Context, first bool) (*relayConn, error) {
	endpoint, err := relayEndpoint(b.opts.RelayURL)
	if err != nil {
		return nil, errPermanent{err}
	}
	dctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ws, resp, err := websocket.Dial(dctx, endpoint, &websocket.DialOptions{
		HTTPHeader: http.Header{"User-Agent": []string{"herdr-share/" + version.Version}},
	})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return nil, errPermanent{fmt.Errorf("no herdr-share relay at %s (HTTP 404)", b.opts.RelayURL)}
		}
		return nil, fmt.Errorf("connect to relay %s: %w", b.opts.RelayURL, err)
	}
	ws.SetReadLimit(relayReadLimit)

	hello := wire.Control{Protocol: version.ProtocolVersion, Client: "herdr-share/" + version.Version}
	if first {
		hello.Type = wire.TypeCreate
		hello.Token = b.opts.Token
		hello.TTLSeconds = int64(b.opts.TTL / time.Second)
	} else {
		b.mu.Lock()
		hello.Type = wire.TypeResume
		hello.ShareID = b.state.ShareID
		hello.Secret = b.secret
		b.mu.Unlock()
	}
	if err := ws.Write(dctx, websocket.MessageText, hello.Marshal()); err != nil {
		_ = ws.CloseNow()
		return nil, fmt.Errorf("relay handshake: %w", err)
	}
	typ, data, err := ws.Read(dctx)
	if err != nil {
		_ = ws.CloseNow()
		return nil, fmt.Errorf("relay handshake: %w", err)
	}
	if typ != websocket.MessageText {
		_ = ws.CloseNow()
		return nil, errors.New("relay handshake: unexpected binary reply")
	}
	reply, err := wire.ParseControl(data)
	if err != nil {
		_ = ws.CloseNow()
		return nil, fmt.Errorf("relay handshake: %w", err)
	}
	switch reply.Type {
	case wire.TypeCreated, wire.TypeResumed:
	case wire.TypeError:
		_ = ws.CloseNow()
		msg := fmt.Errorf("relay refused: %s", reply.Message)
		switch reply.Code {
		case "unauthorized", "not_found", "protocol":
			return nil, errPermanent{msg}
		}
		return nil, msg
	default:
		_ = ws.CloseNow()
		return nil, fmt.Errorf("relay handshake: unexpected %q", reply.Type)
	}

	b.mu.Lock()
	if first {
		b.secret = reply.Secret
		b.state.ShareID = reply.ShareID
		base := strings.TrimRight(reply.GuestBase, "/")
		if base == "" {
			base = strings.TrimRight(b.opts.RelayURL, "/")
		}
		b.state.Link = base + wire.SharePage + reply.ShareID + "#" + b.key.String()
	}
	b.state.Status = StatusLive
	b.mu.Unlock()
	b.setExpiry(reply.ExpiresAt)

	rc := &relayConn{ws: ws, out: make(chan []byte, 256), ctrl: make(chan wire.Control, 16)}
	b.mu.Lock()
	b.out = rc.out
	b.ctrl = rc.ctrl
	b.mu.Unlock()
	return rc, nil
}

// setExpiry follows the relay's (possibly capped) expiry, enforcing it locally too.
func (b *Bridge) setExpiry(unix int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.expiry != nil {
		b.expiry.Stop()
		b.expiry = nil
	}
	if unix <= 0 {
		b.state.ExpiresAt = time.Time{}
		return
	}
	at := time.Unix(unix, 0).UTC()
	b.state.ExpiresAt = at
	b.expiry = time.AfterFunc(time.Until(at), func() { b.Stop(EndExpired) })
}

// relayLoop serves one relay connection after another until the share ends.
func (b *Bridge) relayLoop(ctx context.Context, rc *relayConn, end func(string)) {
	for {
		err := b.serveRelay(ctx, rc)
		if ctx.Err() != nil {
			return
		}
		var ended errEnded
		if errors.As(err, &ended) {
			end(ended.reason)
			return
		}
		b.log.Warn("relay connection lost; reconnecting", "err", err)
		b.mu.Lock()
		b.out, b.ctrl = nil, nil
		b.state.Status = StatusReconnecting
		b.mu.Unlock()
		b.dropGuests()
		b.changed()
		for delay := 500 * time.Millisecond; ; delay = min(delay*2, 10*time.Second) {
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
			rc, err = b.dialRelay(ctx, false)
			if err == nil {
				break
			}
			var perm errPermanent
			if errors.As(err, &perm) {
				b.log.Error("relay forgot the share", "err", err)
				end(EndRelayLost)
				return
			}
			b.log.Warn("relay reconnect failed", "err", err)
		}
		b.log.Info("relay connection restored")
		b.changed()
	}
}

type errEnded struct{ reason string }

func (e errEnded) Error() string { return "share ended: " + e.reason }

func (b *Bridge) serveRelay(ctx context.Context, rc *relayConn) error {
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer func() { _ = rc.ws.CloseNow() }()

	writeErr := make(chan error, 1)
	go func() {
		for {
			var err error
			select {
			case <-cctx.Done():
				return
			case frame := <-rc.out:
				wctx, wcancel := context.WithTimeout(cctx, 30*time.Second)
				err = rc.ws.Write(wctx, websocket.MessageBinary, frame)
				wcancel()
			case m := <-rc.ctrl:
				wctx, wcancel := context.WithTimeout(cctx, 30*time.Second)
				if m.Type == wire.TypeClose {
					// Flush frames queued before the close (the guests' "end").
				drain:
					for err == nil {
						select {
						case frame := <-rc.out:
							err = rc.ws.Write(wctx, websocket.MessageBinary, frame)
						default:
							break drain
						}
					}
				}
				if err == nil {
					err = rc.ws.Write(wctx, websocket.MessageText, m.Marshal())
				}
				wcancel()
				if err == nil && m.Type == wire.TypeClose {
					writeErr <- errEnded{reason: m.Reason}
					return
				}
			}
			if err != nil {
				writeErr <- err
				cancel()
				return
			}
		}
	}()
	go func() {
		t := time.NewTicker(25 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-cctx.Done():
				return
			case <-t.C:
				pctx, pcancel := context.WithTimeout(cctx, 20*time.Second)
				err := rc.ws.Ping(pctx)
				pcancel()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()

	for {
		typ, data, err := rc.ws.Read(cctx)
		if err != nil {
			select {
			case werr := <-writeErr:
				return werr
			default:
			}
			if websocket.CloseStatus(err) == websocket.StatusNormalClosure {
				return errEnded{reason: EndStopped}
			}
			return err
		}
		if typ == websocket.MessageBinary {
			gid, payload, err := wire.UnpackGuestFrame(data)
			if err == nil {
				b.fromGuest(gid, payload)
			}
			continue
		}
		m, err := wire.ParseControl(data)
		if err != nil {
			continue
		}
		switch m.Type {
		case wire.TypeGuestJoin:
			b.guestJoined(m.Guest)
		case wire.TypeGuestLeft:
			b.guestLeft(m.Guest)
		case wire.TypeExtended:
			b.setExpiry(m.ExpiresAt)
			b.mu.Lock()
			exp := b.state.ExpiresAt
			b.mu.Unlock()
			var unix int64
			if !exp.IsZero() {
				unix = exp.Unix()
			}
			b.broadcast(wire.AppMessage{T: wire.AppExpires, ExpiresAt: unix})
			b.changed()
		case wire.TypeExpired:
			return errEnded{reason: EndExpired}
		}
	}
}

// sendRaw queues a frame for a guest on the current relay connection. It
// blocks while the connection is busy, which slows the attach stream down
// through herdr-core's credit window.
func (b *Bridge) sendRaw(ctx context.Context, gid uint32, payload []byte) bool {
	b.mu.Lock()
	out := b.out
	b.mu.Unlock()
	if out == nil {
		return false
	}
	select {
	case out <- wire.PackGuestFrame(gid, payload):
		return true
	case <-ctx.Done():
		return false
	}
}

func (b *Bridge) sendControl(m wire.Control) {
	b.mu.Lock()
	ctrl := b.ctrl
	b.mu.Unlock()
	if ctrl == nil {
		return
	}
	select {
	case ctrl <- m:
	case <-time.After(2 * time.Second):
	}
}
