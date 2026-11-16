// Package relay is the central service that connects a sharer's bridge with
// guests' browsers. It forwards end-to-end encrypted frames and never sees
// terminal content: it only knows share ids, how many guests are connected
// and when a share expires.
package relay

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/jtsang4/herdr-share/internal/version"
	"github.com/jtsang4/herdr-share/internal/wire"
)

// Config configures a relay.
type Config struct {
	// PublicURL is the base URL guests use (https://share.example.com). Empty:
	// bridges build links from the relay URL they were configured with.
	PublicURL string
	// Tokens, when not empty, are required to create shares.
	Tokens []string
	// MaxShares bounds concurrent shares (0: 1000).
	MaxShares int
	// MaxGuests bounds guests per share (0: 20).
	MaxGuests int
	// MaxTTL caps a share's lifetime (0: unlimited).
	MaxTTL time.Duration
	// ResumeGrace is how long a share waits for its bridge to reconnect (0: 2m).
	ResumeGrace time.Duration
	// GuestBuffer bounds bytes queued for one guest (0: 8 MiB). A guest that
	// falls further behind is disconnected and reconnects with a fresh screen.
	GuestBuffer int
	// TrustProxy uses X-Forwarded-For / X-Forwarded-Proto from a reverse proxy.
	TrustProxy bool
	// AllowedOrigins are extra Origin host patterns accepted for guest
	// WebSockets (the relay's own host is always accepted).
	AllowedOrigins []string
	// CreateRate and JoinRate are per-IP requests per minute (0: 30 and 120).
	CreateRate int
	JoinRate   int
	Logger     *slog.Logger
}

func (c *Config) defaults() {
	if c.MaxShares <= 0 {
		c.MaxShares = 1000
	}
	if c.MaxGuests <= 0 {
		c.MaxGuests = 20
	}
	if c.ResumeGrace <= 0 {
		c.ResumeGrace = 2 * time.Minute
	}
	if c.GuestBuffer <= 0 {
		c.GuestBuffer = 8 << 20
	}
	if c.CreateRate <= 0 {
		c.CreateRate = 30
	}
	if c.JoinRate <= 0 {
		c.JoinRate = 120
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	c.PublicURL = strings.TrimRight(c.PublicURL, "/")
}

const (
	hostReadLimit  = 8 << 20
	guestReadLimit = 1 << 20
	hostQueue      = 32 << 20
	pingEvery      = 25 * time.Second
	helloTimeout   = 15 * time.Second
)

var shareIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)

// Server is a relay.
type Server struct {
	cfg     Config
	log     *slog.Logger
	mu      sync.Mutex
	shares  map[string]*share
	creates *limiter
	joins   *limiter
	closed  bool
	mux     *http.ServeMux
	web     client
}

type share struct {
	id         string
	secretHash [32]byte
	createdAt  time.Time
	expiresAt  time.Time // zero: never
	host       *conn
	guests     map[uint32]*conn
	nextGuest  uint32
	expiry     *time.Timer
	grace      *time.Timer
	ended      bool
}

// New builds a relay.
func New(cfg Config) *Server {
	cfg.defaults()
	s := &Server{
		cfg:     cfg,
		log:     cfg.Logger,
		shares:  map[string]*share{},
		creates: newLimiter(cfg.CreateRate, time.Minute),
		joins:   newLimiter(cfg.JoinRate, time.Minute),
		web:     loadClient(),
	}
	s.mux = http.NewServeMux()
	s.routes()
	return s
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	s.mux.HandleFunc("GET "+wire.InfoPath, s.handleInfo)
	s.mux.HandleFunc("GET "+wire.HostPath, s.handleHost)
	s.mux.HandleFunc("GET /api/v1/shares/{id}/guest", s.handleGuest)
	s.mux.HandleFunc("GET /s/{id}", s.handleSharePage)
	s.mux.HandleFunc("GET /s/{id}/", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if !shareIDPattern.MatchString(id) {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, "../"+id, http.StatusMovedPermanently) //nolint:gosec // id is a validated share id, the target is relative
	})
	s.mux.Handle("GET /_app/", s.assets())
	s.mux.HandleFunc("GET /{$}", s.handleIndex)
}

// Info is what GET /api/v1/info returns.
type Info struct {
	Name          string `json:"name"`
	Version       string `json:"version"`
	Protocol      int    `json:"protocol"`
	AuthRequired  bool   `json:"auth_required"`
	MaxTTLSeconds int64  `json:"max_ttl_seconds"`
	MaxGuests     int    `json:"max_guests"`
}

func (s *Server) handleInfo(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(Info{
		Name:          "herdr-share-relay",
		Version:       version.Version,
		Protocol:      version.ProtocolVersion,
		AuthRequired:  len(s.cfg.Tokens) > 0,
		MaxTTLSeconds: int64(s.cfg.MaxTTL / time.Second),
		MaxGuests:     s.cfg.MaxGuests,
	})
}

// Stats reports current load.
func (s *Server) Stats() (shares, guests int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sh := range s.shares {
		guests += len(sh.guests)
	}
	return len(s.shares), guests
}

// Shutdown ends every share and closes every connection.
func (s *Server) Shutdown(ctx context.Context) {
	s.mu.Lock()
	s.closed = true
	var all []*share
	for _, sh := range s.shares {
		all = append(all, sh)
	}
	s.mu.Unlock()
	for _, sh := range all {
		s.end(sh, wire.ReasonShutdown, true, websocket.StatusGoingAway)
	}
	// Let the close frames go out.
	select {
	case <-ctx.Done():
	case <-time.After(300 * time.Millisecond):
	}
}

func (s *Server) clientIP(r *http.Request) string {
	if s.cfg.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			return strings.TrimSpace(strings.Split(xff, ",")[0])
		}
		if xr := r.Header.Get("X-Real-Ip"); xr != "" {
			return strings.TrimSpace(xr)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func randomID(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func shortID(id string) string {
	if len(id) > 6 {
		return id[:6]
	}
	return id
}

func (s *Server) tokenOK(token string) bool {
	if len(s.cfg.Tokens) == 0 {
		return true
	}
	ok := false
	for _, t := range s.cfg.Tokens {
		if subtle.ConstantTimeCompare([]byte(t), []byte(token)) == 1 {
			ok = true
		}
	}
	return ok
}

func (s *Server) capTTL(ttl time.Duration) time.Duration {
	if s.cfg.MaxTTL > 0 && (ttl <= 0 || ttl > s.cfg.MaxTTL) {
		return s.cfg.MaxTTL
	}
	if ttl < 0 {
		return 0
	}
	return ttl
}

func unixOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func sendControl(c *conn, m wire.Control) { c.sendText(m.Marshal()) }

func readControl(ctx context.Context, ws *websocket.Conn) (wire.Control, error) {
	typ, data, err := ws.Read(ctx)
	if err != nil {
		return wire.Control{}, err
	}
	if typ != websocket.MessageText {
		return wire.Control{}, errors.New("expected a text control message")
	}
	return wire.ParseControl(data)
}

func refuse(ws *websocket.Conn, code websocket.StatusCode, m wire.Control) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = ws.Write(ctx, websocket.MessageText, m.Marshal())
	_ = ws.Close(code, m.Code)
}

// ---- host side ----

func (s *Server) handleHost(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	ws.SetReadLimit(hostReadLimit)
	ctx, cancel := context.WithTimeout(r.Context(), helloTimeout)
	first, err := readControl(ctx, ws)
	cancel()
	if err != nil {
		refuse(ws, websocket.StatusPolicyViolation, wire.Control{Type: wire.TypeError, Code: "bad_request", Message: "expected create or resume"})
		return
	}
	if first.Protocol != version.ProtocolVersion {
		refuse(ws, wire.CloseProtocol, wire.Control{Type: wire.TypeError, Code: "protocol", Message: "unsupported protocol version; update herdr-share or the relay"})
		return
	}
	var sh *share
	c := newConn(ws, ip, hostQueue)
	fail := func(code websocket.StatusCode, m wire.Control) {
		c.fail(m.Marshal(), code, m.Code)
		<-c.done
	}
	switch first.Type {
	case wire.TypeCreate:
		if !s.creates.allow(ip) {
			fail(websocket.StatusTryAgainLater, wire.Control{Type: wire.TypeError, Code: "rate_limited", Message: "too many shares created; try again later"})
			return
		}
		if !s.tokenOK(first.Token) {
			s.log.Warn("share refused: bad token", "addr", ip)
			fail(wire.CloseUnauthorized, wire.Control{Type: wire.TypeError, Code: "unauthorized", Message: "this relay requires a valid token"})
			return
		}
		if sh, err = s.create(c, time.Duration(first.TTLSeconds)*time.Second); err != nil {
			fail(websocket.StatusTryAgainLater, wire.Control{Type: wire.TypeError, Code: "capacity", Message: err.Error()})
			return
		}
	case wire.TypeResume:
		if sh, err = s.resume(c, first.ShareID, first.Secret); err != nil {
			fail(wire.CloseNotFound, wire.Control{Type: wire.TypeError, Code: "not_found", Message: err.Error()})
			return
		}
	default:
		fail(websocket.StatusPolicyViolation, wire.Control{Type: wire.TypeError, Code: "bad_request", Message: "expected create or resume"})
		return
	}
	go c.keepalive(pingEvery)
	s.hostLoop(sh, c)
}

func (s *Server) create(c *conn, ttl time.Duration) (*share, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errors.New("relay is shutting down")
	}
	if len(s.shares) >= s.cfg.MaxShares {
		return nil, errors.New("relay is at capacity")
	}
	secret := randomID(32)
	sh := &share{
		id:         randomID(16),
		secretHash: sha256.Sum256([]byte(secret)),
		createdAt:  time.Now(),
		host:       c,
		guests:     map[uint32]*conn{},
	}
	if ttl = s.capTTL(ttl); ttl > 0 {
		sh.expiresAt = sh.createdAt.Add(ttl)
		sh.expiry = time.AfterFunc(ttl, func() { s.expire(sh) })
	}
	s.shares[sh.id] = sh
	sendControl(c, wire.Control{
		Type:      wire.TypeCreated,
		Protocol:  version.ProtocolVersion,
		ShareID:   sh.id,
		Secret:    secret,
		GuestBase: s.cfg.PublicURL,
		ExpiresAt: unixOrZero(sh.expiresAt),
		MaxGuests: s.cfg.MaxGuests,
	})
	expires := "never"
	if !sh.expiresAt.IsZero() {
		expires = sh.expiresAt.UTC().Format(time.RFC3339)
	}
	s.log.Info("share created", "share", shortID(sh.id), "addr", c.addr, "expires_at", expires)
	return sh, nil
}

func (s *Server) resume(c *conn, id, secret string) (*share, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sh := s.shares[id]
	if sh == nil || sh.ended {
		return nil, errors.New("share not found")
	}
	h := sha256.Sum256([]byte(secret))
	if subtle.ConstantTimeCompare(h[:], sh.secretHash[:]) != 1 {
		return nil, errors.New("share not found")
	}
	if old := sh.host; old != nil {
		old.closeNow(wire.CloseReplaced, "replaced")
	}
	if sh.grace != nil {
		sh.grace.Stop()
		sh.grace = nil
	}
	sh.host = c
	sendControl(c, wire.Control{Type: wire.TypeResumed, Protocol: version.ProtocolVersion, ShareID: sh.id, GuestBase: s.cfg.PublicURL, ExpiresAt: unixOrZero(sh.expiresAt), MaxGuests: s.cfg.MaxGuests})
	for gid, g := range sh.guests {
		sendControl(g, wire.Control{Type: wire.TypeHostOnline})
		sendControl(c, wire.Control{Type: wire.TypeGuestJoin, Guest: gid})
	}
	s.log.Info("share resumed", "share", shortID(sh.id), "addr", c.addr, "guests", len(sh.guests))
	return sh, nil
}

func (s *Server) hostLoop(sh *share, c *conn) {
	defer c.cancel()
	for {
		typ, data, err := c.ws.Read(c.ctx)
		if err != nil {
			s.hostGone(sh, c)
			return
		}
		if typ == websocket.MessageBinary {
			gid, payload, err := wire.UnpackGuestFrame(data)
			if err != nil {
				continue
			}
			s.mu.Lock()
			g := sh.guests[gid]
			s.mu.Unlock()
			if g == nil {
				continue
			}
			if !g.sendBinary(payload) {
				s.log.Info("guest too slow; disconnecting", "share", shortID(sh.id), "guest", gid)
				g.closeNow(wire.CloseTooSlow, "too slow")
			}
			continue
		}
		m, err := wire.ParseControl(data)
		if err != nil {
			continue
		}
		switch m.Type {
		case wire.TypeKick:
			s.mu.Lock()
			g := sh.guests[m.Guest]
			s.mu.Unlock()
			if g != nil {
				sendControl(g, wire.Control{Type: wire.TypeEnded, Reason: wire.ReasonKicked})
				g.closeAfter(wire.CloseKicked, "kicked")
			}
		case wire.TypeExtend:
			s.extend(sh, c, time.Duration(m.TTLSeconds)*time.Second)
		case wire.TypeClose:
			reason := m.Reason
			if reason == "" {
				reason = wire.ReasonStopped
			}
			s.end(sh, reason, false, websocket.StatusNormalClosure)
			c.closeAfter(websocket.StatusNormalClosure, "closed")
			<-c.done
			return
		}
	}
}

func (s *Server) hostGone(sh *share, c *conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sh.ended || sh.host != c {
		return
	}
	sh.host = nil
	for _, g := range sh.guests {
		sendControl(g, wire.Control{Type: wire.TypeHostOffline})
	}
	grace := s.cfg.ResumeGrace
	sh.grace = time.AfterFunc(grace, func() {
		s.mu.Lock()
		stillGone := sh.host == nil && !sh.ended
		s.mu.Unlock()
		if stillGone {
			s.log.Info("share ended: bridge did not come back", "share", shortID(sh.id))
			s.end(sh, wire.ReasonHostLost, false, websocket.StatusNormalClosure)
		}
	})
	s.log.Info("bridge disconnected; waiting", "share", shortID(sh.id), "grace", grace)
}

func (s *Server) extend(sh *share, c *conn, ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sh.ended {
		return
	}
	if sh.expiry != nil {
		sh.expiry.Stop()
		sh.expiry = nil
	}
	sh.expiresAt = time.Time{}
	if ttl = s.capTTL(ttl); ttl > 0 {
		sh.expiresAt = time.Now().Add(ttl)
		sh.expiry = time.AfterFunc(ttl, func() { s.expire(sh) })
	}
	sendControl(c, wire.Control{Type: wire.TypeExtended, ExpiresAt: unixOrZero(sh.expiresAt)})
}

func (s *Server) expire(sh *share) {
	s.mu.Lock()
	host := sh.host
	s.mu.Unlock()
	if host != nil {
		sendControl(host, wire.Control{Type: wire.TypeExpired})
	}
	s.log.Info("share expired", "share", shortID(sh.id))
	s.end(sh, wire.ReasonExpired, true, websocket.StatusNormalClosure)
}

// end removes a share and disconnects its guests (and its host if closeHost).
func (s *Server) end(sh *share, reason string, closeHost bool, code websocket.StatusCode) {
	s.mu.Lock()
	if sh.ended {
		s.mu.Unlock()
		return
	}
	sh.ended = true
	delete(s.shares, sh.id)
	if sh.expiry != nil {
		sh.expiry.Stop()
	}
	if sh.grace != nil {
		sh.grace.Stop()
	}
	guests := sh.guests
	sh.guests = map[uint32]*conn{}
	host := sh.host
	s.mu.Unlock()
	for _, g := range guests {
		sendControl(g, wire.Control{Type: wire.TypeEnded, Reason: reason})
		g.closeAfter(wire.CloseEnded, reason)
	}
	if closeHost && host != nil {
		host.closeAfter(code, reason)
	}
	s.log.Info("share ended", "share", shortID(sh.id), "reason", reason)
}

// ---- guest side ----

func (s *Server) acceptGuest(w http.ResponseWriter, r *http.Request) (*websocket.Conn, error) {
	patterns := append([]string{}, s.cfg.AllowedOrigins...)
	if s.cfg.PublicURL != "" {
		if i := strings.Index(s.cfg.PublicURL, "://"); i >= 0 {
			host := s.cfg.PublicURL[i+3:]
			if j := strings.IndexByte(host, '/'); j >= 0 {
				host = host[:j]
			}
			patterns = append(patterns, host)
		}
	}
	if s.cfg.TrustProxy {
		if h := r.Header.Get("X-Forwarded-Host"); h != "" {
			patterns = append(patterns, h)
		}
	}
	return websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: patterns})
}

func (s *Server) handleGuest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ip := s.clientIP(r)
	if !s.joins.allow(ip) {
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}
	ws, err := s.acceptGuest(w, r)
	if err != nil {
		return
	}
	ws.SetReadLimit(guestReadLimit)

	s.mu.Lock()
	sh := s.shares[id]
	if !shareIDPattern.MatchString(id) || sh == nil || sh.ended {
		s.mu.Unlock()
		refuse(ws, wire.CloseNotFound, wire.Control{Type: wire.TypeEnded, Reason: wire.ReasonNotFound})
		return
	}
	if len(sh.guests) >= s.cfg.MaxGuests {
		s.mu.Unlock()
		refuse(ws, wire.CloseFull, wire.Control{Type: wire.TypeEnded, Reason: "full", Message: "this share has its maximum number of viewers"})
		return
	}
	sh.nextGuest++
	gid := sh.nextGuest
	g := newConn(ws, ip, s.cfg.GuestBuffer)
	sh.guests[gid] = g
	host := sh.host
	sendControl(g, wire.Control{Type: wire.TypeJoined, Guest: gid, ExpiresAt: unixOrZero(sh.expiresAt)})
	if host != nil {
		sendControl(host, wire.Control{Type: wire.TypeGuestJoin, Guest: gid})
	} else {
		sendControl(g, wire.Control{Type: wire.TypeHostOffline})
	}
	s.mu.Unlock()
	s.log.Info("guest joined", "share", shortID(id), "guest", gid)

	go g.keepalive(pingEvery)
	defer g.cancel()
	for {
		typ, data, err := ws.Read(g.ctx)
		if err != nil {
			break
		}
		if typ != websocket.MessageBinary {
			continue
		}
		s.mu.Lock()
		host := sh.host
		s.mu.Unlock()
		if host != nil {
			host.sendBinary(wire.PackGuestFrame(gid, data))
		}
	}
	s.mu.Lock()
	_, present := sh.guests[gid]
	delete(sh.guests, gid)
	host = sh.host
	s.mu.Unlock()
	if present && host != nil {
		sendControl(host, wire.Control{Type: wire.TypeGuestLeft, Guest: gid})
	}
	s.log.Info("guest left", "share", shortID(id), "guest", gid)
}
