package relay

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"regexp"
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

// Behind a proxy that strips a path prefix, a share page's assets and the
// viewer WebSocket resolve under the prefix.
func TestBehindPathPrefix(t *testing.T) {
	s, hs := newTestServer(t, Config{})
	if !s.web.ok {
		t.Skip("web client not built")
	}
	target, _ := url.Parse(hs.URL)
	proxy := httputil.NewSingleHostReverseProxy(target)
	front := httptest.NewServer(http.StripPrefix("/herdr", proxy))
	t.Cleanup(front.Close)

	page := front.URL + "/herdr/s/abcdefghijklmnopqrstuv"
	resp, err := http.Get(page)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	m := regexp.MustCompile(`"(\.\./_app/[^"]+\.js)"`).FindSubmatch(body)
	if m == nil {
		t.Fatalf("no relative script in %s", body)
	}
	base, _ := url.Parse(page)
	asset := base.ResolveReference(&url.URL{Path: string(m[1])})
	if !strings.HasPrefix(asset.Path, "/herdr/_app/") {
		t.Fatalf("asset resolves to %s", asset.Path)
	}
	resp, err = http.Get(asset.String())
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Cache-Control"), "immutable") {
		t.Fatalf("asset %s: %d %q", asset, resp.StatusCode, resp.Header.Get("Cache-Control"))
	}
}

func TestParseNetworks(t *testing.T) {
	nets, err := ParseNetworks([]string{"10.0.0.0/8", " 192.168.1.7 ", "fd00::/8", "::ffff:172.16.0.1", ""})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, n := range nets {
		got = append(got, n.String())
	}
	if want := "10.0.0.0/8 192.168.1.7/32 fd00::/8 172.16.0.1/32"; strings.Join(got, " ") != want {
		t.Fatalf("got %v", got)
	}
	for _, bad := range []string{"10.0.0.0/33", "not-an-ip", "10.0.0/8"} {
		if _, err := ParseNetworks([]string{bad}); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestOpenNetworksMatchClients(t *testing.T) {
	nets, _ := ParseNetworks([]string{"10.0.0.0/8", "192.168.1.0/24", "fd00::/8"})
	s := New(Config{Tokens: []string{"t"}, OpenNetworks: nets})
	for ip, open := range map[string]bool{
		"10.144.144.8":    true,
		"192.168.1.200":   true,
		"192.168.2.1":     false,
		"::ffff:10.1.2.3": true,
		"fd12::1":         true,
		"2001:db8::1":     false,
		"203.0.113.9":     false,
		"garbage":         false,
		"fe80::1%en0":     false,
	} {
		if got := !s.tokenRequired(ip); got != open {
			t.Errorf("%s: open=%v, want %v", ip, got, open)
		}
	}
	if (&Server{cfg: Config{OpenNetworks: nets}}).tokenRequired("203.0.113.9") {
		t.Error("without tokens nobody needs one")
	}
}

func TestOpenNetworksCreateWithoutToken(t *testing.T) {
	loopback, _ := ParseNetworks([]string{"127.0.0.0/8", "::1"})
	_, open := newTestServer(t, Config{Tokens: []string{"secret"}, OpenNetworks: loopback})
	if _, m := create(t, open, "", 60); m.Type != wire.TypeCreated {
		t.Fatalf("from an open network without a token: %+v", m)
	}
	if info := fetchInfo(t, open.URL, nil); info.AuthRequired {
		t.Fatal("info should tell an open-network caller that no token is needed")
	}

	elsewhere, _ := ParseNetworks([]string{"10.0.0.0/8"})
	_, closed := newTestServer(t, Config{Tokens: []string{"secret"}, OpenNetworks: elsewhere})
	if _, m := create(t, closed, "", 60); m.Code != "unauthorized" {
		t.Fatalf("outside the open networks without a token: %+v", m)
	}
	if _, m := create(t, closed, "secret", 60); m.Type != wire.TypeCreated {
		t.Fatalf("outside the open networks with the token: %+v", m)
	}
	if info := fetchInfo(t, closed.URL, nil); !info.AuthRequired {
		t.Fatal("info should ask a caller outside the open networks for a token")
	}
}

func TestOpenNetworksBehindTrustedProxy(t *testing.T) {
	office, _ := ParseNetworks([]string{"10.0.0.0/8"})
	_, hs := newTestServer(t, Config{Tokens: []string{"secret"}, OpenNetworks: office, TrustProxy: true})
	if info := fetchInfo(t, hs.URL, http.Header{"X-Forwarded-For": []string{"10.2.3.4, 172.16.0.1"}}); info.AuthRequired {
		t.Fatal("the forwarded client is in the open network")
	}
	if info := fetchInfo(t, hs.URL, http.Header{"X-Forwarded-For": []string{"203.0.113.5"}}); !info.AuthRequired {
		t.Fatal("the forwarded client is outside the open network")
	}
	_, untrusted := newTestServer(t, Config{Tokens: []string{"secret"}, OpenNetworks: office})
	if info := fetchInfo(t, untrusted.URL, http.Header{"X-Forwarded-For": []string{"10.2.3.4"}}); !info.AuthRequired {
		t.Fatal("a forged X-Forwarded-For must not open the relay without TrustProxy")
	}
}

func fetchInfo(t *testing.T, base string, header http.Header) Info {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, base+wire.InfoPath, nil)
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var info Info
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatal(err)
	}
	return info
}
