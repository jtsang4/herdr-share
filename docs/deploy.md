# Deploying the relay

The relay is one stateless process. It pairs each sharer's bridge with the share's viewers and
forwards end-to-end encrypted frames between them; it never sees terminal content. It also serves
the viewer's web page.

## Container

```sh
docker run -d --name herdr-share-relay --restart unless-stopped \
  -p 8080:8080 \
  -e HERDR_SHARE_TOKENS=token-for-alice,token-for-bob \
  ghcr.io/jtsang4/herdr-share-relay:latest
```

Images are published for `linux/amd64` and `linux/arm64` on every release, tagged with the
version (`1.2.3`), the minor series (`1.2`) and `latest`. The image is distroless, runs as a
non-root user, stores nothing and has a built-in health check.

To build the image from source: `docker build -t herdr-share-relay .`

## Binary

Every release also ships `herdr-share-relay` for Linux, macOS and Windows:

```sh
herdr-share-relay -listen :8080 -tokens token-for-alice
```

## Configuration

Flags and environment variables are equivalent.

| Variable | Flag | Default | Meaning |
|---|---|---|---|
| `HERDR_SHARE_LISTEN` | `-listen` | `:8080` | Address to listen on |
| `HERDR_SHARE_TOKENS` | `-tokens` | *(none)* | Comma-separated tokens required to create shares. Empty: anyone who can reach the relay can create shares |
| `HERDR_SHARE_TOKENS_FILE` | `-tokens-file` | | File with one token per line (for secrets mounted as files) |
| `HERDR_SHARE_OPEN_NETWORKS` | `-open-networks` | *(none)* | Comma-separated networks that may create shares **without** a token even when tokens are set, e.g. `10.0.0.0/8,192.168.1.0/24,fd00::/8`; single addresses work too. Invalid entries stop the relay at startup |
| `HERDR_SHARE_PUBLIC_URL` | `-public-url` | | The URL viewers use, when it differs from the URL sharers configure (e.g. bridges reach the relay on an internal address) |
| `HERDR_SHARE_MAX_TTL` | `-max-ttl` | `24h` | Longest a share may last: `24h`, `168h` or `7d`, `0` = unlimited. “No limit” shares are capped to this |
| `HERDR_SHARE_MAX_SHARES` | `-max-shares` | `1000` | Concurrent shares |
| `HERDR_SHARE_MAX_GUESTS` | `-max-guests` | `20` | Viewers per share |
| `HERDR_SHARE_RESUME_GRACE` | `-resume-grace` | `2m` | How long a share waits for its bridge to reconnect |
| `HERDR_SHARE_TRUST_PROXY` | `-trust-proxy` | `false` | Use `X-Forwarded-*` headers (client IPs for rate limits). Enable only behind a proxy you control |
| `HERDR_SHARE_ALLOWED_ORIGINS` | `-allowed-origins` | | Extra `Origin` hosts accepted for viewer WebSockets (the relay's own host always is) |
| `HERDR_SHARE_LOG_FORMAT` | `-log-format` | `text` | `text` or `json` |
| `HERDR_SHARE_LOG_LEVEL` | `-log-level` | `info` | `debug`, `info`, `warn`, `error` |

Endpoints: `/healthz` (liveness), `/api/v1/info` (version, protocol, whether a token is required),
`/` (a short page explaining the relay).

**Use tokens** unless the relay is reachable only by people you trust. Give each person their own
token so you can revoke one by removing it and restarting the relay.

Three ways to run it:

- **Open** (no `HERDR_SHARE_TOKENS`): anyone who can reach the relay can create shares. Fine on a
  private network.
- **Tokens**: every sharer configures a token.
- **Tokens plus open networks**: sharers on the listed networks (office, VPN) need no token; everyone
  else does. `/api/v1/info` answers `auth_required` for the caller, so the CLI and the Herdr dialog
  only ask for a token where one is needed. Behind a reverse proxy, set `HERDR_SHARE_TRUST_PROXY` so
  the relay sees the client's address (`X-Forwarded-For`) rather than the proxy's.

Whatever the mode, sessions stay end-to-end encrypted and viewers only ever need the link.

## HTTPS and reverse proxies

Over the internet, put the relay behind HTTPS. Any proxy that passes WebSockets works:

- [deploy/docker-compose.yml](../deploy/docker-compose.yml) + [deploy/Caddyfile](../deploy/Caddyfile):
  relay and Caddy with automatic certificates.
- [deploy/nginx.conf](../deploy/nginx.conf): nginx, including the `Upgrade` headers and long
  timeouts WebSockets need.

The relay can live at the root of a (sub)domain or under a path prefix that the proxy strips
(e.g. `https://example.com/herdr-share/` → relay `/`). Sharers then configure
`https://example.com/herdr-share` as the relay.

## Private and isolated networks

Nothing calls out to the internet: the viewer page and its fonts are embedded in the relay. Plain
`http://` works (the encryption does not depend on the browser's secure-context APIs), e.g.
`herdr-share config set relay http://10.0.0.5:8080`. Anyone on that network who can see the traffic
still cannot read sessions, but a token keeps strangers from creating shares.

## Operations

- **Restarts end all shares.** Shares live in memory; bridges notice and end their shares, and
  sharers start new ones. Plan upgrades accordingly.
- **Scaling.** One relay handles many shares; traffic is proportional to terminal output × viewers.
  To run several, route each share's traffic to one instance (sticky by the share id in
  `/api/v1/shares/<id>/…` and `/s/<id>`); the bridge's `/api/v1/host` connection must reach the same
  instance — simplest is one relay per team.
- **Logs** record share ids (shortened), connects and disconnects, never keys or content.
