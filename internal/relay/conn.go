package relay

import (
	"context"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const writeTimeout = 30 * time.Second

type outMsg struct {
	typ    websocket.MessageType
	data   []byte
	close  bool
	code   websocket.StatusCode
	reason string
}

// conn is one WebSocket with a bounded outgoing queue and its own writer.
type conn struct {
	ws     *websocket.Conn
	addr   string
	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	queue   []outMsg
	bytes   int
	max     int
	closing bool
	signal  chan struct{}
	done    chan struct{}
}

func newConn(ws *websocket.Conn, addr string, maxQueued int) *conn {
	ctx, cancel := context.WithCancel(context.Background())
	c := &conn{
		ws:     ws,
		addr:   addr,
		ctx:    ctx,
		cancel: cancel,
		max:    maxQueued,
		signal: make(chan struct{}, 1),
		done:   make(chan struct{}),
	}
	go c.writer()
	return c
}

func (c *conn) wake() {
	select {
	case c.signal <- struct{}{}:
	default:
	}
}

// send queues a message. It returns false when the queue is over its budget
// or the connection is closing; the caller decides what that means.
func (c *conn) send(typ websocket.MessageType, data []byte) bool {
	c.mu.Lock()
	if c.closing {
		c.mu.Unlock()
		return false
	}
	if c.max > 0 && c.bytes+len(data) > c.max {
		c.mu.Unlock()
		return false
	}
	c.queue = append(c.queue, outMsg{typ: typ, data: data})
	c.bytes += len(data)
	c.mu.Unlock()
	c.wake()
	return true
}

func (c *conn) sendText(b []byte) bool   { return c.send(websocket.MessageText, b) }
func (c *conn) sendBinary(b []byte) bool { return c.send(websocket.MessageBinary, b) }

// closeAfter queues a close frame behind what is already queued.
func (c *conn) closeAfter(code websocket.StatusCode, reason string) {
	c.mu.Lock()
	if !c.closing {
		c.closing = true
		c.queue = append(c.queue, outMsg{close: true, code: code, reason: reason})
	}
	c.mu.Unlock()
	c.wake()
}

// closeNow drops anything queued and closes.
func (c *conn) closeNow(code websocket.StatusCode, reason string) {
	c.mu.Lock()
	c.closing = true
	c.queue = []outMsg{{close: true, code: code, reason: reason}}
	c.bytes = 0
	c.mu.Unlock()
	c.wake()
}

// fail drops anything queued, sends one last text message and closes.
func (c *conn) fail(text []byte, code websocket.StatusCode, reason string) {
	c.mu.Lock()
	c.closing = true
	c.queue = []outMsg{{typ: websocket.MessageText, data: text}, {close: true, code: code, reason: reason}}
	c.bytes = len(text)
	c.mu.Unlock()
	c.wake()
}

func (c *conn) writer() {
	defer close(c.done)
	defer c.cancel()
	for {
		c.mu.Lock()
		if len(c.queue) == 0 {
			c.mu.Unlock()
			select {
			case <-c.signal:
				continue
			case <-c.ctx.Done():
				_ = c.ws.CloseNow()
				return
			}
		}
		m := c.queue[0]
		c.queue[0] = outMsg{}
		c.queue = c.queue[1:]
		c.bytes -= len(m.data)
		c.mu.Unlock()

		if m.close {
			_ = c.ws.Close(m.code, m.reason)
			return
		}
		ctx, cancel := context.WithTimeout(c.ctx, writeTimeout)
		err := c.ws.Write(ctx, m.typ, m.data)
		cancel()
		if err != nil {
			_ = c.ws.CloseNow()
			return
		}
	}
}

// keepalive pings until the connection ends.
func (c *conn) keepalive(every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-t.C:
			ctx, cancel := context.WithTimeout(c.ctx, every)
			err := c.ws.Ping(ctx)
			cancel()
			if err != nil {
				c.cancel()
				_ = c.ws.CloseNow()
				return
			}
		}
	}
}
