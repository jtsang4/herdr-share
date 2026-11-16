package relay

import (
	"sync"
	"time"
)

// limiter is a per-key fixed-window counter: at most n events per window.
type limiter struct {
	mu     sync.Mutex
	n      int
	window time.Duration
	seen   map[string]*bucket
	swept  time.Time
}

type bucket struct {
	start time.Time
	count int
}

func newLimiter(n int, window time.Duration) *limiter {
	return &limiter{n: n, window: window, seen: map[string]*bucket{}}
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if now.Sub(l.swept) > l.window {
		for k, b := range l.seen {
			if now.Sub(b.start) > l.window {
				delete(l.seen, k)
			}
		}
		l.swept = now
	}
	b := l.seen[key]
	if b == nil || now.Sub(b.start) > l.window {
		b = &bucket{start: now}
		l.seen[key] = b
	}
	if b.count >= l.n {
		return false
	}
	b.count++
	return true
}
