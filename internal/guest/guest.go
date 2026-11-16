// Package guest is a Go implementation of the guest side of a share: the
// same protocol the browser client speaks. It is used by tests and tools.
package guest

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"github.com/coder/websocket"

	"github.com/jtsang4/herdr-share/internal/e2ee"
	"github.com/jtsang4/herdr-share/internal/version"
	"github.com/jtsang4/herdr-share/internal/wire"
)

// Link is a parsed share link.
type Link struct {
	Base    string // relay base URL
	ShareID string
	Key     e2ee.Key
}

// ParseLink parses https://relay/s/<id>#<key>.
func ParseLink(s string) (Link, error) {
	u, err := url.Parse(s)
	if err != nil {
		return Link{}, err
	}
	i := strings.LastIndex(u.Path, wire.SharePage)
	if i < 0 {
		return Link{}, errors.New("not a share link")
	}
	id := strings.Trim(u.Path[i+len(wire.SharePage):], "/")
	key, err := e2ee.ParseKey(u.Fragment)
	if err != nil {
		return Link{}, fmt.Errorf("share link key: %w", err)
	}
	base := *u
	base.Path = u.Path[:i]
	base.Fragment, base.RawQuery = "", ""
	return Link{Base: strings.TrimRight(base.String(), "/"), ShareID: id, Key: key}, nil
}

// Event is something that arrived from the share.
type Event struct {
	// Output is terminal output (Kind "output").
	Output []byte
	// App is an end-to-end message from the bridge (Kind "app").
	App wire.AppMessage
	// Relay is a relay control message (Kind "relay").
	Relay wire.Control
	Kind  string
}

// Client is one guest connection.
type Client struct {
	ws   *websocket.Conn
	link Link
	name string
	mu   sync.Mutex
	ch   *e2ee.Channel
}

// Dial connects to a share. name is the display name sent to the sharer.
func Dial(ctx context.Context, link, name string, opts *websocket.DialOptions) (*Client, error) {
	l, err := ParseLink(link)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(l.Base)
	if err != nil {
		return nil, err
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	}
	u.Path = strings.TrimRight(u.Path, "/") + wire.GuestPath + l.ShareID + "/guest"
	if opts == nil {
		opts = &websocket.DialOptions{}
	}
	ws, _, err := websocket.Dial(ctx, u.String(), opts)
	if err != nil {
		return nil, err
	}
	ws.SetReadLimit(16 << 20)
	return &Client{ws: ws, link: l, name: name}, nil
}

// Next returns the next event. Init frames are handled internally (the
// hello is answered), so callers see the welcome and what follows.
func (c *Client) Next(ctx context.Context) (Event, error) {
	for {
		typ, data, err := c.ws.Read(ctx)
		if err != nil {
			return Event{}, err
		}
		if typ == websocket.MessageText {
			m, err := wire.ParseControl(data)
			if err != nil {
				continue
			}
			return Event{Kind: "relay", Relay: m}, nil
		}
		if len(data) == 0 {
			continue
		}
		switch data[0] {
		case wire.EnvInit:
			if len(data) < 1+e2ee.CIDSize {
				return Event{}, errors.New("short init frame")
			}
			var cid e2ee.CID
			copy(cid[:], data[1:1+e2ee.CIDSize])
			ch, err := e2ee.NewChannel(c.link.Key, cid, e2ee.ToBridge)
			if err != nil {
				return Event{}, err
			}
			pt, err := ch.Open(data[1+e2ee.CIDSize:])
			if err != nil {
				return Event{}, fmt.Errorf("cannot decrypt the share (wrong link?): %w", err)
			}
			if _, _, m, err := wire.DecodePlain(pt); err != nil || m.T != wire.AppInit {
				return Event{}, errors.New("unexpected init message")
			}
			c.mu.Lock()
			c.ch = ch
			c.mu.Unlock()
			if err := c.sendPlain(ctx, wire.EncodeApp(wire.AppMessage{T: wire.AppHello, Name: c.name, Client: "go/" + version.Version})); err != nil {
				return Event{}, err
			}
		case wire.EnvFrame:
			c.mu.Lock()
			ch := c.ch
			c.mu.Unlock()
			if ch == nil {
				continue
			}
			pt, err := ch.Open(data[1:])
			if err != nil {
				return Event{}, err
			}
			kind, body, m, err := wire.DecodePlain(pt)
			if err != nil {
				return Event{}, err
			}
			switch kind {
			case wire.MsgOutput:
				return Event{Kind: "output", Output: body}, nil
			case wire.MsgControl:
				return Event{Kind: "app", App: m}, nil
			}
		}
	}
}

func (c *Client) sendPlain(ctx context.Context, pt []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ch == nil {
		return errors.New("not connected to the bridge yet")
	}
	sealed, err := c.ch.Seal(pt)
	if err != nil {
		return err
	}
	return c.ws.Write(ctx, websocket.MessageBinary, append([]byte{wire.EnvFrame}, sealed...))
}

// Type sends keyboard input. The bridge drops it unless the share is in
// control mode.
func (c *Client) Type(ctx context.Context, data []byte) error {
	return c.sendPlain(ctx, wire.EncodeData(wire.MsgInput, data))
}

// SendRaw sends an arbitrary binary message (tests use it to tamper).
func (c *Client) SendRaw(ctx context.Context, b []byte) error {
	return c.ws.Write(ctx, websocket.MessageBinary, b)
}

// Close disconnects.
func (c *Client) Close() error { return c.ws.Close(websocket.StatusNormalClosure, "bye") }
