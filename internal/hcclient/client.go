// Package hcclient speaks the herdr-core API (JSON-RPC 2.0 in framed mode)
// through `hc connect`, so it reaches the same core, session and machines as
// the hc CLI does on every platform.
//
// Framed mode: every frame is [channel u32 LE][kind u8][len u32 LE][payload].
// Channel 0 carries JSON-RPC; attach and event streams get their own channels.
// See herdr-core's schema/README.md.
package hcclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Frame kinds.
const (
	KindData           byte = 0
	KindCredit         byte = 1
	KindClose          byte = 2
	KindResize         byte = 3
	KindFocus          byte = 4
	KindFacts          byte = 5
	KindAutomaticInput byte = 6
)

const (
	headerSize = 9
	maxPayload = 16 << 20
	// Generation is the protocol generation this client speaks.
	Generation = 1
)

// Options says how to reach the core.
type Options struct {
	// Bin is the hc executable. Empty: FindHC.
	Bin string
	// Session selects an hc session (like HC_SESSION). Empty: the default.
	Session string
	// Env adds variables to hc's environment.
	Env []string
}

// FindHC locates the hc executable: $HC_BIN, PATH, then the usual install
// folders.
func FindHC() (string, error) {
	if b := os.Getenv("HC_BIN"); b != "" {
		return b, nil
	}
	if p, err := exec.LookPath("hc"); err == nil {
		return p, nil
	}
	home, _ := os.UserHomeDir()
	name := "hc"
	if runtime.GOOS == "windows" {
		name = "hc.exe"
	}
	for _, dir := range []string{
		filepath.Join(home, ".local", "bin"),
		"/opt/homebrew/bin",
		"/usr/local/bin",
		filepath.Join(home, ".cargo", "bin"),
	} {
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", errors.New("hc (herdr-core) not found: install it from https://herdr.dev or set HC_BIN")
}

// RPCError is an error answered by the core.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Kind    string `json:"-"`
	Hint    string `json:"-"`
}

func (e *RPCError) Error() string {
	if e.Kind != "" {
		return fmt.Sprintf("hc: %s (%s)", e.Message, e.Kind)
	}
	return "hc: " + e.Message
}

// IsKind reports whether err is an RPCError of the given kind.
func IsKind(err error, kind string) bool {
	var e *RPCError
	return errors.As(err, &e) && e.Kind == kind
}

// ErrClosed means the connection to the core is gone.
var ErrClosed = errors.New("hc: connection closed")

type response struct {
	result json.RawMessage
	err    error
}

// Client is one framed connection to a core.
type Client struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	wmu    sync.Mutex
	stderr *limitedBuffer
	Hello  HelloResult

	nextID  atomic.Uint64
	mu      sync.Mutex
	pending map[uint64]chan response
	streams map[uint32]*Stream
	done    chan struct{}
	err     error
}

// HelloResult is what the core says about itself.
type HelloResult struct {
	Generation int      `json:"generation"`
	Version    string   `json:"version"`
	Framed     bool     `json:"framed"`
	Features   []string `json:"features"`
	Core       struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"core"`
}

// HasFeature reports whether the core advertises a feature.
func (h HelloResult) HasFeature(f string) bool {
	for _, x := range h.Features {
		if x == f {
			return true
		}
	}
	return false
}

// Dial starts `hc connect` and switches it to framed mode.
func Dial(ctx context.Context, o Options) (*Client, error) {
	bin := o.Bin
	if bin == "" {
		var err error
		if bin, err = FindHC(); err != nil {
			return nil, err
		}
	}
	args := []string{"connect"}
	if o.Session != "" {
		args = append(args, "--session", o.Session)
	}
	// The process lives until Close, not as long as the dial context.
	cmd := exec.CommandContext(context.WithoutCancel(ctx), bin, args...) //nolint:gosec // runs the hc the user installed or configured
	cmd.Env = append(os.Environ(), o.Env...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr := &limitedBuffer{max: 64 << 10}
	cmd.Stderr = stderr
	hideWindow(cmd)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s connect: %w", bin, err)
	}
	c := &Client{
		cmd:     cmd,
		stdin:   stdin,
		stderr:  stderr,
		pending: map[uint64]chan response{},
		streams: map[uint32]*Stream{},
		done:    make(chan struct{}),
	}
	br := bufio.NewReaderSize(stdout, 64<<10)

	hello := fmt.Sprintf(`{"jsonrpc":"2.0","id":0,"method":"hello","params":{"framed":true,"generation":%d}}`+"\n", Generation)
	type lineResult struct {
		line []byte
		err  error
	}
	got := make(chan lineResult, 1)
	go func() {
		if _, err := io.WriteString(stdin, hello); err != nil {
			got <- lineResult{err: err}
			return
		}
		line, err := br.ReadBytes('\n')
		got <- lineResult{line, err}
	}()
	timeout := time.NewTimer(15 * time.Second)
	defer timeout.Stop()
	var lr lineResult
	select {
	case lr = <-got:
	case <-ctx.Done():
		c.kill()
		return nil, ctx.Err()
	case <-timeout.C:
		c.kill()
		return nil, fmt.Errorf("hc connect: no hello reply%s", c.stderrSuffix())
	}
	if lr.err != nil {
		c.kill()
		return nil, fmt.Errorf("hc connect: %w%s", lr.err, c.stderrSuffix())
	}
	var reply struct {
		Result *HelloResult     `json:"result"`
		Error  *json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(lr.line, &reply); err != nil {
		c.kill()
		return nil, fmt.Errorf("hc connect: bad hello reply: %w", err)
	}
	if reply.Error != nil {
		c.kill()
		return nil, parseRPCError(*reply.Error)
	}
	if reply.Result == nil || !reply.Result.Framed {
		c.kill()
		return nil, errors.New("hc connect: core did not switch to framed mode")
	}
	c.Hello = *reply.Result
	go c.readLoop(br)
	return c, nil
}

func (c *Client) stderrSuffix() string {
	s := strings.TrimSpace(c.stderr.String())
	if s == "" {
		return ""
	}
	return ": " + s
}

func (c *Client) kill() {
	_ = c.stdin.Close()
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	_ = c.cmd.Wait()
}

// Close ends the connection.
func (c *Client) Close() error {
	_ = c.stdin.Close()
	select {
	case <-c.done:
	case <-time.After(2 * time.Second):
		if c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
		<-c.done
	}
	return nil
}

// Done is closed when the connection ends.
func (c *Client) Done() <-chan struct{} { return c.done }

// Err is why the connection ended (after Done).
func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *Client) readLoop(r *bufio.Reader) {
	err := c.readFrames(r)
	_ = c.cmd.Wait()
	if errors.Is(err, io.EOF) {
		err = ErrClosed
	}
	if s := c.stderrSuffix(); s != "" {
		err = fmt.Errorf("%w%s", err, s)
	}
	c.mu.Lock()
	c.err = err
	pending := c.pending
	c.pending = map[uint64]chan response{}
	streams := c.streams
	c.streams = map[uint32]*Stream{}
	c.mu.Unlock()
	for _, ch := range pending {
		ch <- response{err: err}
	}
	for _, s := range streams {
		s.end(err)
	}
	close(c.done)
}

func (c *Client) readFrames(r *bufio.Reader) error {
	var header [headerSize]byte
	for {
		if _, err := io.ReadFull(r, header[:]); err != nil {
			return err
		}
		ch := binary.LittleEndian.Uint32(header[0:4])
		kind := header[4]
		n := binary.LittleEndian.Uint32(header[5:9])
		if n > maxPayload {
			return fmt.Errorf("hc: frame of %d bytes", n)
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(r, payload); err != nil {
			return err
		}
		if ch == 0 {
			if kind == KindData {
				c.handleRPC(payload)
			}
			continue
		}
		c.mu.Lock()
		s := c.streams[ch]
		if s == nil {
			// Stream frames can only follow the reply that announced the
			// channel, but the caller may not have claimed it yet.
			s = newStream(c, ch)
			c.streams[ch] = s
		}
		c.mu.Unlock()
		s.push(Frame{Kind: kind, Data: payload})
		if kind == KindClose {
			s.end(io.EOF)
		}
	}
}

func (c *Client) handleRPC(payload []byte) {
	var msg struct {
		ID     *uint64          `json:"id"`
		Result json.RawMessage  `json:"result"`
		Error  *json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(payload, &msg); err != nil || msg.ID == nil {
		return // notifications on channel 0 are not used in framed mode
	}
	c.mu.Lock()
	ch := c.pending[*msg.ID]
	delete(c.pending, *msg.ID)
	c.mu.Unlock()
	if ch == nil {
		return
	}
	if msg.Error != nil {
		ch <- response{err: parseRPCError(*msg.Error)}
		return
	}
	ch <- response{result: msg.Result}
}

func parseRPCError(raw json.RawMessage) error {
	var e struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Kind string `json:"kind"`
			Hint string `json:"hint"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &e); err != nil {
		return fmt.Errorf("hc: unreadable error %s", string(raw))
	}
	return &RPCError{Code: e.Code, Message: e.Message, Kind: e.Data.Kind, Hint: e.Data.Hint}
}

func (c *Client) writeFrame(ch uint32, kind byte, payload []byte) error {
	if len(payload) > maxPayload {
		return fmt.Errorf("hc: frame of %d bytes is too large", len(payload))
	}
	buf := make([]byte, headerSize+len(payload))
	binary.LittleEndian.PutUint32(buf[0:4], ch)
	buf[4] = kind
	binary.LittleEndian.PutUint32(buf[5:9], uint32(len(payload))) //nolint:gosec // bounded by maxPayload above
	copy(buf[headerSize:], payload)
	c.wmu.Lock()
	defer c.wmu.Unlock()
	select {
	case <-c.done:
		return ErrClosed
	default:
	}
	_, err := c.stdin.Write(buf)
	return err
}

// Call invokes a method and decodes its result into out (which may be nil).
func (c *Client) Call(ctx context.Context, method string, params any, out any) error {
	id := c.nextID.Add(1)
	req, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		return err
	}
	ch := make(chan response, 1)
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return err
	}
	c.pending[id] = ch
	c.mu.Unlock()
	if err := c.writeFrame(0, KindData, req); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return err
	}
	select {
	case r := <-ch:
		if r.err != nil {
			return r.err
		}
		if out != nil {
			return json.Unmarshal(r.result, out)
		}
		return nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return ctx.Err()
	}
}

func (c *Client) release(ch uint32, s *Stream) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.streams[ch] == s {
		delete(c.streams, ch)
	}
}

func (c *Client) claim(ch uint32) *Stream {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.streams[ch]
	if s == nil {
		s = newStream(c, ch)
		c.streams[ch] = s
	}
	return s
}

type limitedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := b.max - b.buf.Len(); room > 0 {
		if len(p) > room {
			b.buf.Write(p[:room])
		} else {
			b.buf.Write(p)
		}
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
