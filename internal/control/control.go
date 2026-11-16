// Package control is the local channel between the herdr-share CLI (and the
// Herdr mod, through the CLI) and a running bridge: one JSON request and one
// JSON response per connection, over a private Unix socket.
package control

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// Operations.
const (
	OpStatus = "status"
	OpMode   = "mode"
	OpExtend = "extend"
	OpStop   = "stop"
)

// Request asks a bridge to do something.
type Request struct {
	Op         string `json:"op"`
	Mode       string `json:"mode,omitempty"`
	TTLSeconds int64  `json:"ttl_seconds,omitempty"`
}

// Response is a bridge's answer; State is the share state after the request.
type Response struct {
	OK    bool            `json:"ok"`
	Error string          `json:"error,omitempty"`
	State json.RawMessage `json:"state,omitempty"`
}

// Handler serves requests.
type Handler func(Request) (any, error)

// SocketPath picks a socket path under dir, falling back to a short path
// when dir is too deep for the OS's socket path limit.
func SocketPath(dir, id string) string {
	p := filepath.Join(dir, id+".sock")
	if len(p) < 100 || runtime.GOOS == "windows" {
		return p
	}
	return filepath.Join("/tmp", fmt.Sprintf("herdr-share-%d-%s.sock", os.Getuid(), id))
}

// Server listens on a socket until closed.
type Server struct {
	ln   net.Listener
	path string
}

// Listen starts serving h on path.
func Listen(path string, h Handler) (*Server, error) {
	_ = os.Remove(path)
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", path)
	if err != nil {
		return nil, err
	}
	_ = os.Chmod(path, 0o600)
	s := &Server{ln: ln, path: path}
	go s.serve(h)
	return s, nil
}

func (s *Server) serve(h Handler) {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer c.Close()
			_ = c.SetDeadline(time.Now().Add(30 * time.Second))
			line, err := bufio.NewReader(c).ReadBytes('\n')
			if err != nil && len(line) == 0 {
				return
			}
			var req Request
			resp := Response{}
			if err := json.Unmarshal(line, &req); err != nil {
				resp.Error = "bad request"
			} else if v, err := h(req); err != nil {
				resp.Error = err.Error()
			} else {
				resp.OK = true
				if v != nil {
					resp.State, _ = json.Marshal(v)
				}
			}
			b, _ := json.Marshal(resp)
			_, _ = c.Write(append(b, '\n'))
		}()
	}
}

// Close stops listening and removes the socket.
func (s *Server) Close() error {
	err := s.ln.Close()
	_ = os.Remove(s.path)
	return err
}

// ErrUnreachable means no bridge answers on the socket.
var ErrUnreachable = errors.New("the share's bridge is not running")

// Call sends one request.
func Call(ctx context.Context, path string, req Request) (Response, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return Response{}, ErrUnreachable
	}
	defer c.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = c.SetDeadline(dl)
	} else {
		_ = c.SetDeadline(time.Now().Add(30 * time.Second))
	}
	b, _ := json.Marshal(req)
	if _, err := c.Write(append(b, '\n')); err != nil {
		return Response{}, err
	}
	line, err := bufio.NewReader(c).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return Response{}, err
	}
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return Response{}, err
	}
	if !resp.OK {
		return resp, errors.New(resp.Error)
	}
	return resp, nil
}
