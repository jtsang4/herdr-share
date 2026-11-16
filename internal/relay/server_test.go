package relay

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/jtsang4/herdr-share/internal/version"
	"github.com/jtsang4/herdr-share/internal/wire"
)

func newTestServer(t *testing.T, cfg Config) (*Server, *httptest.Server) {
	t.Helper()
	s := New(cfg)
	hs := httptest.NewServer(s)
	t.Cleanup(hs.Close)
	return s, hs
}

func wsURL(hs *httptest.Server, path string) string {
	return "ws" + strings.TrimPrefix(hs.URL, "http") + path
}

func dial(t *testing.T, url string, header http.Header) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: header})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.CloseNow() })
	return c
}

func send(t *testing.T, c *websocket.Conn, m wire.Control) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, m.Marshal()); err != nil {
		t.Fatal(err)
	}
}

func recv(t *testing.T, c *websocket.Conn) (websocket.MessageType, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	typ, b, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return typ, b
}

func recvControl(t *testing.T, c *websocket.Conn, want string) wire.Control {
	t.Helper()
	for {
		typ, b := recv(t, c)
		if typ != websocket.MessageText {
			continue
		}
		m, err := wire.ParseControl(b)
		if err != nil {
			t.Fatal(err)
		}
		if m.Type == want {
			return m
		}
	}
}

func create(t *testing.T, hs *httptest.Server, token string, ttl int64) (*websocket.Conn, wire.Control) {
	t.Helper()
	host := dial(t, wsURL(hs, wire.HostPath), nil)
	send(t, host, wire.Control{Type: wire.TypeCreate, Protocol: version.ProtocolVersion, Token: token, TTLSeconds: ttl})
	_, b := recv(t, host)
	m, err := wire.ParseControl(b)
	if err != nil {
		t.Fatal(err)
	}
	return host, m
}

func guestHeader(hs *httptest.Server) http.Header {
	return http.Header{"Origin": []string{hs.URL}}
}

func TestInfoAndHealth(t *testing.T) {
	_, hs := newTestServer(t, Config{Tokens: []string{"x"}, MaxTTL: time.Hour, MaxGuests: 3})
	resp, err := http.Get(hs.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz %d", resp.StatusCode)
	}
	resp, err = http.Get(hs.URL + wire.InfoPath)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var info Info
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatal(err)
	}
	if info.Name != "herdr-share-relay" || !info.AuthRequired || info.MaxTTLSeconds != 3600 || info.MaxGuests != 3 || info.Protocol != version.ProtocolVersion {
		t.Fatalf("info %+v", info)
	}
}

func TestPagesWithoutClientBuild(t *testing.T) {
	s, hs := newTestServer(t, Config{})
	for _, path := range []string{"/", "/s/abcdefghijklmnopqrstuv"} {
		resp, err := http.Get(hs.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if s.web.ok {
			if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "<!doctype html>") {
				t.Fatalf("%s: %d", path, resp.StatusCode)
			}
			csp := resp.Header.Get("Content-Security-Policy")
			if !strings.Contains(csp, "'sha256-") || strings.Contains(csp, "unsafe-eval") {
				t.Fatalf("%s: csp %q", path, csp)
			}
			want := `"./_app/`
			if path != "/" {
				want = `"../_app/`
			}
			if !strings.Contains(string(body), want) {
				t.Fatalf("%s: asset paths not relative", path)
			}
		} else if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("%s without a client build: %d", path, resp.StatusCode)
		}
	}
	resp, err := http.Get(hs.URL + "/s/short")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("bad id: %d", resp.StatusCode)
	}
	resp, err = http.Get(hs.URL + "/_app/../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatal("asset path escaped")
	}
}

func TestRelayForwardsBetweenHostAndGuests(t *testing.T) {
	s, hs := newTestServer(t, Config{})
	host, created := create(t, hs, "", 600)
	if created.Type != wire.TypeCreated || created.ShareID == "" || created.Secret == "" || created.ExpiresAt == 0 {
		t.Fatalf("created %+v", created)
	}

	guest := dial(t, wsURL(hs, "/api/v1/shares/"+created.ShareID+"/guest"), guestHeader(hs))
	joined := recvControl(t, guest, wire.TypeJoined)
	join := recvControl(t, host, wire.TypeGuestJoin)
	if join.Guest != joined.Guest || join.Guest == 0 {
		t.Fatalf("guest ids %d / %d", join.Guest, joined.Guest)
	}

	ctx := context.Background()
	if err := host.Write(ctx, websocket.MessageBinary, wire.PackGuestFrame(join.Guest, []byte("to guest"))); err != nil {
		t.Fatal(err)
	}
	if typ, b := recv(t, guest); typ != websocket.MessageBinary || string(b) != "to guest" {
		t.Fatalf("guest got %v %q", typ, b)
	}
	if err := guest.Write(ctx, websocket.MessageBinary, []byte("to host")); err != nil {
		t.Fatal(err)
	}
	typ, b := recv(t, host)
	gid, payload, err := wire.UnpackGuestFrame(b)
	if typ != websocket.MessageBinary || err != nil || gid != join.Guest || string(payload) != "to host" {
		t.Fatalf("host got %v %d %q %v", typ, gid, payload, err)
	}

	send(t, host, wire.Control{Type: wire.TypeExtend, TTLSeconds: 7200})
	if ext := recvControl(t, host, wire.TypeExtended); ext.ExpiresAt < time.Now().Add(time.Hour).Unix() {
		t.Fatalf("extended %+v", ext)
	}

	send(t, host, wire.Control{Type: wire.TypeClose, Reason: wire.ReasonStopped})
	if ended := recvControl(t, guest, wire.TypeEnded); ended.Reason != wire.ReasonStopped {
		t.Fatalf("ended %+v", ended)
	}
	if shares, _ := s.Stats(); shares != 0 {
		t.Fatalf("%d shares left", shares)
	}
}

func TestGuestOfMissingShare(t *testing.T) {
	_, hs := newTestServer(t, Config{})
	guest := dial(t, wsURL(hs, "/api/v1/shares/abcdefghijklmnopqrstuv/guest"), guestHeader(hs))
	if m := recvControl(t, guest, wire.TypeEnded); m.Reason != wire.ReasonNotFound {
		t.Fatalf("got %+v", m)
	}
}

func TestTokensAndProtocol(t *testing.T) {
	_, hs := newTestServer(t, Config{Tokens: []string{"good"}})
	_, m := create(t, hs, "bad", 60)
	if m.Type != wire.TypeError || m.Code != "unauthorized" {
		t.Fatalf("bad token: %+v", m)
	}
	_, m = create(t, hs, "good", 60)
	if m.Type != wire.TypeCreated {
		t.Fatalf("good token: %+v", m)
	}
	host := dial(t, wsURL(hs, wire.HostPath), nil)
	send(t, host, wire.Control{Type: wire.TypeCreate, Protocol: 99, Token: "good"})
	if m := recvControl(t, host, wire.TypeError); m.Code != "protocol" {
		t.Fatalf("protocol: %+v", m)
	}
}

func TestMaxTTLAndMaxGuests(t *testing.T) {
	_, hs := newTestServer(t, Config{MaxTTL: time.Hour, MaxGuests: 1})
	_, m := create(t, hs, "", 0)
	if left := time.Until(time.Unix(m.ExpiresAt, 0)); left > time.Hour || left < 59*time.Minute {
		t.Fatalf("until-stopped share not capped: %s", left)
	}
	g1 := dial(t, wsURL(hs, "/api/v1/shares/"+m.ShareID+"/guest"), guestHeader(hs))
	recvControl(t, g1, wire.TypeJoined)
	g2 := dial(t, wsURL(hs, "/api/v1/shares/"+m.ShareID+"/guest"), guestHeader(hs))
	if ended := recvControl(t, g2, wire.TypeEnded); ended.Reason != "full" {
		t.Fatalf("second guest: %+v", ended)
	}
}

func TestResumeAfterHostDrops(t *testing.T) {
	_, hs := newTestServer(t, Config{ResumeGrace: 5 * time.Second})
	host, m := create(t, hs, "", 600)
	guest := dial(t, wsURL(hs, "/api/v1/shares/"+m.ShareID+"/guest"), guestHeader(hs))
	recvControl(t, guest, wire.TypeJoined)
	recvControl(t, host, wire.TypeGuestJoin)

	_ = host.CloseNow()
	recvControl(t, guest, wire.TypeHostOffline)

	// A wrong secret cannot take the share over.
	thief := dial(t, wsURL(hs, wire.HostPath), nil)
	send(t, thief, wire.Control{Type: wire.TypeResume, Protocol: version.ProtocolVersion, ShareID: m.ShareID, Secret: "nope"})
	if e := recvControl(t, thief, wire.TypeError); e.Code != "not_found" {
		t.Fatalf("thief: %+v", e)
	}

	again := dial(t, wsURL(hs, wire.HostPath), nil)
	send(t, again, wire.Control{Type: wire.TypeResume, Protocol: version.ProtocolVersion, ShareID: m.ShareID, Secret: m.Secret})
	recvControl(t, again, wire.TypeResumed)
	recvControl(t, guest, wire.TypeHostOnline)
	if j := recvControl(t, again, wire.TypeGuestJoin); j.Guest == 0 {
		t.Fatal("guest not re-announced")
	}
}

func TestShareEndsWhenHostDoesNotReturn(t *testing.T) {
	_, hs := newTestServer(t, Config{ResumeGrace: 200 * time.Millisecond})
	host, m := create(t, hs, "", 600)
	guest := dial(t, wsURL(hs, "/api/v1/shares/"+m.ShareID+"/guest"), guestHeader(hs))
	recvControl(t, guest, wire.TypeJoined)
	_ = host.CloseNow()
	if ended := recvControl(t, guest, wire.TypeEnded); ended.Reason != wire.ReasonHostLost {
		t.Fatalf("ended %+v", ended)
	}
}

func TestGuestOriginIsChecked(t *testing.T) {
	_, hs := newTestServer(t, Config{})
	_, m := create(t, hs, "", 600)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, wsURL(hs, "/api/v1/shares/"+m.ShareID+"/guest"), &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{"https://evil.example"}},
	})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err == nil {
		t.Fatal("a cross-origin page could join")
	}
}

func TestLimiter(t *testing.T) {
	l := newLimiter(2, time.Minute)
	got := []bool{l.allow("a"), l.allow("a"), l.allow("a")}
	if !got[0] || !got[1] || got[2] {
		t.Fatalf("limit not applied: %v", got)
	}
	if !l.allow("b") {
		t.Fatal("keys are independent")
	}
}
