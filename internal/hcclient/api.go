package hcclient

import (
	"bytes"
	"context"
	"encoding/json"
)

// Terminal is the subset of herdr-core's terminal record this project uses.
type Terminal struct {
	Address    string            `json:"address"`
	Name       *string           `json:"name"`
	State      string            `json:"state"`
	Cols       int               `json:"cols"`
	Rows       int               `json:"rows"`
	Title      string            `json:"title"`
	Argv       []string          `json:"argv"`
	Cwd        string            `json:"cwd"`
	Labels     map[string]string `json:"labels"`
	Reason     *string           `json:"reason"`
	ExitCode   *int              `json:"exit_code"`
	Parent     *string           `json:"parent"`
	Foreground *struct {
		Name string   `json:"name"`
		Argv []string `json:"argv"`
		Cwd  string   `json:"cwd"`
	} `json:"foreground"`
}

// Live reports whether the terminal's program still runs.
func (t Terminal) Live() bool { return t.State == "live" }

// DisplayName is a short human label: the title, else the foreground
// program, else the argv, else the address.
func (t Terminal) DisplayName() string {
	if t.Title != "" {
		return t.Title
	}
	if t.Foreground != nil && t.Foreground.Name != "" {
		return t.Foreground.Name
	}
	if len(t.Argv) > 0 {
		return t.Argv[0]
	}
	return t.Address
}

// Get returns one terminal's record.
func (c *Client) Get(ctx context.Context, terminal string) (Terminal, error) {
	var t Terminal
	err := c.Call(ctx, "terminal.get", map[string]any{"terminal": terminal}, &t)
	return t, err
}

// List returns live terminals, optionally filtered by labels.
func (c *Client) List(ctx context.Context, labels map[string]string) ([]Terminal, error) {
	params := map[string]any{}
	if len(labels) > 0 {
		var l []string
		for k, v := range labels {
			l = append(l, k+"="+v)
		}
		params["labels"] = l
	}
	var r struct {
		Terminals []Terminal `json:"terminals"`
	}
	err := c.Call(ctx, "terminal.list", params, &r)
	return r.Terminals, err
}

// AttachParams are terminal.attach's parameters.
type AttachParams struct {
	Terminal            string `json:"terminal"`
	Cols                int    `json:"cols"`
	Rows                int    `json:"rows"`
	Observe             bool   `json:"observe"`
	History             int    `json:"history,omitempty"`
	ResizeNotifications bool   `json:"resize_notifications,omitempty"`
}

// AttachResult is terminal.attach's reply.
type AttachResult struct {
	Channel             uint32   `json:"channel"`
	Terminal            Terminal `json:"terminal"`
	ResizeNotifications bool     `json:"resize_notifications"`
	AutomaticInput      bool     `json:"automatic_input"`
}

// Attach opens a raw stream to a live terminal.
func (c *Client) Attach(ctx context.Context, p AttachParams) (*Stream, AttachResult, error) {
	var r AttachResult
	if err := c.Call(ctx, "terminal.attach", p, &r); err != nil {
		return nil, r, err
	}
	return c.claim(r.Channel), r, nil
}

// RunParams are the terminal.run parameters this project uses.
type RunParams struct {
	Argv     []string          `json:"argv"`
	Name     string            `json:"name,omitempty"`
	Cwd      string            `json:"cwd,omitempty"`
	Labels   map[string]string `json:"labels,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
	NoParent bool              `json:"no_parent,omitempty"`
	NoNotify bool              `json:"no_notify,omitempty"`
	// Remove is terminal.run's `rm`: forget the terminal once it exits.
	Remove bool `json:"rm,omitempty"`
	Size   *struct {
		Cols int `json:"cols"`
		Rows int `json:"rows"`
	} `json:"size,omitempty"`
}

// Run starts a program in a new terminal and returns at once.
func (c *Client) Run(ctx context.Context, p RunParams) (Terminal, error) {
	var t Terminal
	err := c.Call(ctx, "terminal.run", p, &t)
	return t, err
}

// Kill ends a terminal's program.
func (c *Client) Kill(ctx context.Context, terminal string) error {
	return c.Call(ctx, "terminal.kill", map[string]any{"terminal": terminal}, nil)
}

// Remove removes a finished terminal.
func (c *Client) Remove(ctx context.Context, terminal string) error {
	return c.Call(ctx, "terminal.rm", map[string]any{"terminal": terminal}, nil)
}

// Read returns a terminal's screen plus lines of history as text.
func (c *Client) Read(ctx context.Context, terminal string, lines int) (string, error) {
	var r struct {
		Text string `json:"text"`
	}
	err := c.Call(ctx, "terminal.read", map[string]any{"terminal": terminal, "lines": lines}, &r)
	return r.Text, err
}

// Event is one event notification of a subscription.
type Event struct {
	Method string // "event", "heartbeat" or "missed"
	Seq    uint64
	Type   string // for "event": created, finished, size, title, ...
	// Terminal is the event's terminal address (events only).
	Terminal string
	Data     json.RawMessage
	// Terminals is the fresh snapshot of a "missed" notification.
	Terminals []Terminal
}

// Events reads an event subscription's JSON lines.
type Events struct {
	s     *Stream
	carry []byte
	queue []Event
}

// SubscribeParams filter a subscription.
type SubscribeParams struct {
	Terminal string   `json:"terminal,omitempty"`
	Labels   []string `json:"labels,omitempty"`
}

// Subscribe starts an event subscription; it returns the snapshot of
// matching terminals and the stream of later events.
func (c *Client) Subscribe(ctx context.Context, p SubscribeParams) (*Events, []Terminal, error) {
	var r struct {
		Channel   *uint32    `json:"channel"`
		Seq       uint64     `json:"seq"`
		Terminals []Terminal `json:"terminals"`
	}
	if err := c.Call(ctx, "events.subscribe", p, &r); err != nil {
		return nil, nil, err
	}
	if r.Channel == nil {
		return nil, nil, &RPCError{Message: "subscription without a channel"}
	}
	return &Events{s: c.claim(*r.Channel)}, r.Terminals, nil
}

// Next returns the next event.
func (e *Events) Next(ctx context.Context) (Event, error) {
	for len(e.queue) == 0 {
		f, err := e.s.Next(ctx)
		if err != nil {
			return Event{}, err
		}
		switch f.Kind {
		case KindData:
			_ = e.s.Credit(len(f.Data))
			e.carry = append(e.carry, f.Data...)
			for {
				i := bytes.IndexByte(e.carry, '\n')
				if i < 0 {
					break
				}
				line := e.carry[:i]
				e.carry = e.carry[i+1:]
				if ev, ok := parseEvent(line); ok {
					e.queue = append(e.queue, ev)
				}
			}
		case KindClose:
			// Next call returns EOF.
		}
	}
	ev := e.queue[0]
	e.queue = e.queue[1:]
	return ev, nil
}

// Close ends the subscription.
func (e *Events) Close() error { return e.s.Close() }

func parseEvent(line []byte) (Event, bool) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return Event{}, false
	}
	var m struct {
		Method string `json:"method"`
		Params struct {
			Seq       uint64          `json:"seq"`
			Type      string          `json:"type"`
			Terminal  string          `json:"terminal"`
			Data      json.RawMessage `json:"data"`
			Terminals []Terminal      `json:"terminals"`
		} `json:"params"`
	}
	if err := json.Unmarshal(line, &m); err != nil || m.Method == "" {
		return Event{}, false
	}
	return Event{
		Method:    m.Method,
		Seq:       m.Params.Seq,
		Type:      m.Params.Type,
		Terminal:  m.Params.Terminal,
		Data:      m.Params.Data,
		Terminals: m.Params.Terminals,
	}, true
}
