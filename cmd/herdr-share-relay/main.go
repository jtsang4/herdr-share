// Command herdr-share-relay is the relay that connects sharers' bridges with
// guests' browsers. Configure it with flags or HERDR_SHARE_* environment
// variables (see docs/deploy.md).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jtsang4/herdr-share/internal/relay"
	"github.com/jtsang4/herdr-share/internal/version"
)

func env(name, def string) string {
	if v, ok := os.LookupEnv(name); ok && v != "" {
		return v
	}
	return def
}

func envInt(name string, def int) int {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
		fmt.Fprintf(os.Stderr, "ignoring %s=%q: not a number\n", name, v)
	}
	return def
}

func envDuration(name string, def time.Duration) time.Duration {
	if v := os.Getenv(name); v != "" {
		if v == "0" {
			return 0
		}
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
		fmt.Fprintf(os.Stderr, "ignoring %s=%q: not a duration\n", name, v)
	}
	return def
}

func envBool(name string, def bool) bool {
	if v := os.Getenv(name); v != "" {
		b, err := strconv.ParseBool(v)
		if err == nil {
			return b
		}
	}
	return def
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' || r == ' ' }) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func main() {
	os.Exit(run())
}

func run() int {
	var (
		listen      = flag.String("listen", env("HERDR_SHARE_LISTEN", ":8080"), "address to listen on (env HERDR_SHARE_LISTEN)")
		publicURL   = flag.String("public-url", env("HERDR_SHARE_PUBLIC_URL", ""), "base URL guests use, e.g. https://share.example.com (env HERDR_SHARE_PUBLIC_URL)")
		tokens      = flag.String("tokens", env("HERDR_SHARE_TOKENS", ""), "comma-separated tokens required to create shares; empty: anyone can (env HERDR_SHARE_TOKENS)")
		tokensFile  = flag.String("tokens-file", env("HERDR_SHARE_TOKENS_FILE", ""), "file with one token per line (env HERDR_SHARE_TOKENS_FILE)")
		maxShares   = flag.Int("max-shares", envInt("HERDR_SHARE_MAX_SHARES", 1000), "maximum concurrent shares (env HERDR_SHARE_MAX_SHARES)")
		maxGuests   = flag.Int("max-guests", envInt("HERDR_SHARE_MAX_GUESTS", 20), "maximum viewers per share (env HERDR_SHARE_MAX_GUESTS)")
		maxTTL      = flag.Duration("max-ttl", envDuration("HERDR_SHARE_MAX_TTL", 24*time.Hour), "longest a share may last; 0 = unlimited (env HERDR_SHARE_MAX_TTL)")
		grace       = flag.Duration("resume-grace", envDuration("HERDR_SHARE_RESUME_GRACE", 2*time.Minute), "how long a share waits for its bridge to reconnect (env HERDR_SHARE_RESUME_GRACE)")
		trustProxy  = flag.Bool("trust-proxy", envBool("HERDR_SHARE_TRUST_PROXY", false), "trust X-Forwarded-* headers from a reverse proxy (env HERDR_SHARE_TRUST_PROXY)")
		origins     = flag.String("allowed-origins", env("HERDR_SHARE_ALLOWED_ORIGINS", ""), "extra Origin host patterns for guest WebSockets (env HERDR_SHARE_ALLOWED_ORIGINS)")
		logFormat   = flag.String("log-format", env("HERDR_SHARE_LOG_FORMAT", "text"), "text or json (env HERDR_SHARE_LOG_FORMAT)")
		logLevel    = flag.String("log-level", env("HERDR_SHARE_LOG_LEVEL", "info"), "debug, info, warn or error (env HERDR_SHARE_LOG_LEVEL)")
		showVersion = flag.Bool("version", false, "print the version and exit")
		healthcheck = flag.Bool("healthcheck", false, "check the local relay's /healthz and exit (for container health checks)")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println("herdr-share-relay", version.String())
		return 0
	}
	if *healthcheck {
		return runHealthcheck(*listen)
	}

	var level slog.Level
	if err := level.UnmarshalText([]byte(*logLevel)); err != nil {
		level = slog.LevelInfo
	}
	hopts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler = slog.NewTextHandler(os.Stderr, hopts)
	if *logFormat == "json" {
		handler = slog.NewJSONHandler(os.Stderr, hopts)
	}
	log := slog.New(handler)

	toks := splitList(*tokens)
	if *tokensFile != "" {
		b, err := os.ReadFile(*tokensFile)
		if err != nil {
			log.Error("cannot read tokens file", "err", err)
			return 1
		}
		toks = append(toks, splitList(string(b))...)
	}

	srv := relay.New(relay.Config{
		PublicURL:      *publicURL,
		Tokens:         toks,
		MaxShares:      *maxShares,
		MaxGuests:      *maxGuests,
		MaxTTL:         *maxTTL,
		ResumeGrace:    *grace,
		TrustProxy:     *trustProxy,
		AllowedOrigins: splitList(*origins),
		Logger:         log,
	})

	httpSrv := &http.Server{
		Addr:              *listen,
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          slog.NewLogLogger(handler, slog.LevelWarn),
	}
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", *listen)
	if err != nil {
		log.Error("cannot listen", "addr", *listen, "err", err)
		return 1
	}
	log.Info("herdr-share relay listening", "addr", ln.Addr().String(), "version", version.Version,
		"public_url", *publicURL, "auth", len(toks) > 0, "max_ttl", maxTTL.String())
	if len(toks) == 0 {
		log.Warn("no tokens configured: anyone who can reach this relay can create shares (set HERDR_SHARE_TOKENS)")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() { errc <- httpSrv.Serve(ln) }()
	select {
	case err := <-errc:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server stopped", "err", err)
			return 1
		}
	case <-ctx.Done():
		log.Info("shutting down")
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
		_ = httpSrv.Shutdown(sctx)
	}
	return 0
}

func runHealthcheck(listen string) int {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return 1
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort(host, port)+"/healthz", nil)
	if err != nil {
		return 1
	}
	resp, err := http.DefaultClient.Do(req) //nolint:gosec // our own loopback health endpoint
	if err != nil {
		return 1
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
