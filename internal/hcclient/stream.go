package hcclient

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"sync"
)

// Frame is one frame received on a stream.
type Frame struct {
	Kind byte
	Data []byte
}

// Stream is a channel of a framed connection: an attach stream or an event
// subscription. Frames queue without bound on the receiving side; the core's
// credit window bounds how much can be outstanding.
type Stream struct {
	c      *Client
	ch     uint32
	mu     sync.Mutex
	queue  []Frame
	err    error
	signal chan struct{}
	closed bool
}

func newStream(c *Client, ch uint32) *Stream {
	return &Stream{c: c, ch: ch, signal: make(chan struct{}, 1)}
}

// Channel is the stream's channel number.
func (s *Stream) Channel() uint32 { return s.ch }

func (s *Stream) push(f Frame) {
	s.mu.Lock()
	if s.err == nil {
		s.queue = append(s.queue, f)
	}
	s.mu.Unlock()
	s.wake()
}

func (s *Stream) end(err error) {
	s.mu.Lock()
	if s.err == nil {
		s.err = err
	}
	s.mu.Unlock()
	s.wake()
}

func (s *Stream) wake() {
	select {
	case s.signal <- struct{}{}:
	default:
	}
}

// Next returns the next frame. A CLOSE frame is returned as a frame; after
// it, Next returns io.EOF. If the connection drops, Next returns its error.
func (s *Stream) Next(ctx context.Context) (Frame, error) {
	for {
		s.mu.Lock()
		if len(s.queue) > 0 {
			f := s.queue[0]
			s.queue[0] = Frame{}
			s.queue = s.queue[1:]
			s.mu.Unlock()
			return f, nil
		}
		err := s.err
		s.mu.Unlock()
		if err != nil {
			s.c.release(s.ch, s)
			return Frame{}, err
		}
		select {
		case <-s.signal:
		case <-ctx.Done():
			return Frame{}, ctx.Err()
		}
	}
}

// Write sends bytes (keys) as DATA.
func (s *Stream) Write(p []byte) error {
	return s.c.writeFrame(s.ch, KindData, p)
}

// WriteAutomatic sends terminal-generated replies (only when negotiated).
func (s *Stream) WriteAutomatic(p []byte) error {
	return s.c.writeFrame(s.ch, KindAutomaticInput, p)
}

// Credit acknowledges n consumed bytes.
func (s *Stream) Credit(n int) error {
	if n <= 0 || n > maxPayload {
		return nil
	}
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], uint32(n)) //nolint:gosec // bounded above
	return s.c.writeFrame(s.ch, KindCredit, b[:])
}

// Resize announces the viewer's size.
func (s *Stream) Resize(cols, rows int) error {
	cols = min(max(cols, 1), 0xffff)
	rows = min(max(rows, 1), 0xffff)
	var b [4]byte
	binary.LittleEndian.PutUint16(b[0:2], uint16(cols)) //nolint:gosec // clamped above
	binary.LittleEndian.PutUint16(b[2:4], uint16(rows)) //nolint:gosec // clamped above
	return s.c.writeFrame(s.ch, KindResize, b[:])
}

// Close detaches. It does not wait for the core's answering CLOSE.
func (s *Stream) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	err := s.c.writeFrame(s.ch, KindClose, nil)
	s.end(io.EOF)
	s.c.release(s.ch, s)
	if errors.Is(err, ErrClosed) {
		return nil
	}
	return err
}

// ParseResize decodes a RESIZE payload.
func ParseResize(p []byte) (cols, rows int, ok bool) {
	if len(p) < 4 {
		return 0, 0, false
	}
	return int(binary.LittleEndian.Uint16(p[0:2])), int(binary.LittleEndian.Uint16(p[2:4])), true
}

// CloseReason decodes the core's CLOSE payload.
type CloseReason struct {
	Reason   string `json:"reason"`
	ExitCode *int   `json:"exit_code"`
	Signal   *int   `json:"signal"`
}

// ParseClose decodes a CLOSE payload; empty payloads give an empty reason.
func ParseClose(p []byte) CloseReason {
	var r CloseReason
	if len(p) > 0 {
		_ = json.Unmarshal(p, &r)
	}
	return r
}
