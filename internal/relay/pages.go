package relay

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io/fs"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jtsang4/herdr-share/web"
)

// client is the guest web client, prepared once at startup.
type client struct {
	files fs.FS
	ok    bool
	// The SPA's fallback page, with its asset paths made relative to where
	// it is served, so the relay also works behind a path prefix.
	rootHTML, shareHTML []byte
	// CSP script hashes for each page's inline boot script.
	rootHashes, shareHashes []string
}

var startTime = time.Now()

var inlineScript = regexp.MustCompile(`(?s)<script>(.*?)</script>`)

func scriptHashes(html []byte) []string {
	var out []string
	for _, m := range inlineScript.FindAllSubmatch(html, -1) {
		sum := sha256.Sum256(m[1])
		out = append(out, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")
	}
	return out
}

func loadClient() client {
	files, ok := web.Assets()
	if !ok {
		return client{}
	}
	index, err := fs.ReadFile(files, "index.html")
	if err != nil {
		return client{}
	}
	c := client{files: files, ok: true}
	// "/_app/..." becomes "./_app/..." at "/" and "../_app/..." at "/s/<id>".
	c.rootHTML = bytes.ReplaceAll(index, []byte(`"/_app/`), []byte(`"./_app/`))
	c.shareHTML = bytes.ReplaceAll(index, []byte(`"/_app/`), []byte(`"../_app/`))
	c.rootHashes = scriptHashes(c.rootHTML)
	c.shareHashes = scriptHashes(c.shareHTML)
	return c
}

func securityHeaders(w http.ResponseWriter, r *http.Request, scriptHashes []string) {
	h := w.Header()
	scripts := "'self'"
	if len(scriptHashes) > 0 {
		scripts += " " + strings.Join(scriptHashes, " ")
	}
	host := r.Host
	h.Set("Content-Security-Policy", fmt.Sprintf(
		"default-src 'none'; script-src %s; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; "+
			"connect-src 'self' wss://%s ws://%s; base-uri 'none'; frame-ancestors 'none'; form-action 'none'",
		scripts, host, host))
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
}

func (s *Server) servePage(w http.ResponseWriter, r *http.Request, page []byte, hashes []string) {
	if !s.web.ok {
		http.Error(w, "the guest web client is not built into this relay (run `make web` before building)", http.StatusServiceUnavailable)
		return
	}
	securityHeaders(w, r, hashes)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(page)
}

func (s *Server) handleSharePage(w http.ResponseWriter, r *http.Request) {
	if !shareIDPattern.MatchString(r.PathValue("id")) {
		http.NotFound(w, r)
		return
	}
	s.servePage(w, r, s.web.shareHTML, s.web.shareHashes)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	s.servePage(w, r, s.web.rootHTML, s.web.rootHashes)
}

// assets serves the client's /_app/ files.
func (s *Server) assets() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.web.ok {
			http.NotFound(w, r)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/")
		if !strings.HasPrefix(name, "_app/") || strings.HasSuffix(name, "/") || strings.Contains(name, "..") {
			http.NotFound(w, r)
			return
		}
		b, err := fs.ReadFile(s.web.files, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		securityHeaders(w, r, nil)
		if strings.HasPrefix(name, "_app/immutable/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		http.ServeContent(w, r, name, startTime, bytes.NewReader(b))
	})
}
