# Architecture

herdr-share has four parts:

| Part | Where | Code |
|---|---|---|
| **Share dialog** (Herdr mod) | the sharer's Herdr window | `mod/herdr-share/` |
| **Share button** (Herdr mod) | a kept floating window over Herdr | `mod/herdr-share-button/` |
| **CLI and bridge** (`herdr-share`) | the sharer's machine | `cmd/herdr-share`, `internal/{cli,bridge,hcclient,control,store,config}` |
| **Relay** (`herdr-share-relay`) | a server you deploy | `cmd/herdr-share-relay`, `internal/relay` |
| **Viewer page** | the viewer's browser, served by the relay | `web/` (SvelteKit, embedded by `web/embed.go`) |

```
 Herdr window ── Share dialog ──exec──▶ herdr-share CLI ──control socket──▶ bridge
                                                                             │  ▲
                                         hc connect (framed JSON-RPC) ◀──────┘  │ WSS (outbound)
                                         herdr-core: the shared terminal        ▼
                                                                          relay ◀── WSS ── viewer page
```

## Sharer side

- **The mod** is a dialog (gpui-kit JavaScript). It never talks to the network: it runs the CLI
  (`herdr-share … --json`) through Herdr's `exec`, finding the binary through `install.json`, which
  `herdr-share mod install` writes beside it.
- **`herdr-share start`** resolves the terminal, checks the relay (`/api/v1/info`), then starts the
  **bridge** — `herdr-share serve` — as a background herdr-core terminal (`terminal.run`, no
  parent, removed when it exits, labelled `herdr-share=bridge`). It waits for the bridge to write a
  ready file and prints the link. The relay token travels in the bridge's environment, not on its
  command line.
- **The bridge** keeps three things:
  - a herdr-core connection through `hc connect` in framed mode (so remote `machine/t7` terminals
    work exactly as with `hc`), with an event subscription on the shared terminal;
  - an outbound WebSocket to the relay (`/api/v1/host`);
  - a private Unix socket (`state/run/<id>.sock`) for `list/status/mode/extend/stop`, and a record
    in `state/shares/<id>.json` (mode `0600`: it contains the link).

### Attaching without disturbing the sharer

herdr-core sizes a terminal by its *most recently active typing viewer*; `observe` viewers never
type or resize. So:

- **Each viewer gets its own `observe` attach** with `resize_notifications`: a full first paint
  (screen plus history), then live output; grid changes arrive as RESIZE before the repaint and are
  forwarded so the page always shows the sharer's exact grid.
- **Viewers type through one shared, non-observing attach**, opened on first input. Before each
  write it declares the terminal's *current* size, so becoming the active viewer never changes the
  size. It stays open until the share ends (detaching a typing viewer makes the core recompute the
  size). Focus-report sequences from browsers are stripped.
- **The mode is enforced here and only here.** In view mode, input frames are dropped before they
  reach herdr-core, whatever a page or relay sends.

Output is credited back to herdr-core only after it is queued for the relay, so a slow relay link
slows the stream through the core's 256 KiB credit window instead of buffering without bound.

The bridge ends the share when told to stop, at expiry (enforced locally as well as by the relay),
when the terminal finishes or is removed, when the relay no longer knows the share, or if the core
stays unreachable for two minutes. It reconnects to the relay (resume) and to the core (after a core
restart or update) on its own; a kept terminal that is replaced by a fresh shell gets fresh attaches.

## Relay

The relay keeps, in memory, each share's id, a hash of its resume secret, its expiry, the bridge's
connection and its viewers' connections. It:

- creates shares (`create`, optionally requiring a token) and lets a bridge `resume` with the
  secret after a dropped connection, within a grace period; viewers stay connected meanwhile and are
  told the sharer is offline;
- forwards binary frames: bridge → viewer `[guest id][payload]` is unwrapped and sent to that
  viewer; viewer → bridge payloads are wrapped with the viewer's id;
- enforces lifetimes (capped by `MAX_TTL`), viewer limits, per-IP rate limits and per-viewer send
  buffers (a viewer that falls 8 MiB behind is disconnected and reconnects to a fresh screen);
- serves the viewer page with a strict Content-Security-Policy (inline boot script allowed by
  hash), and checks the `Origin` of viewer WebSockets.

## Viewer page

A SvelteKit single-page app (adapter-static) with xterm.js. It reads the share id from the path and
the key from the fragment, connects to `/api/v1/shares/<id>/guest`, and renders the terminal at the
grid it is told, scaling the font to fit. It reconnects with backoff unless the share has ended.
The relay serves the same page for `/` (relay information) and `/s/<id>`; asset paths are rewritten
to be relative, so it also works under a path prefix.

## Protocol

### Relay control plane

WebSocket text messages are JSON objects with a `type` (`internal/wire/wire.go`):

| From → to | Types |
|---|---|
| bridge → relay | `create {protocol, token, ttl_seconds}`, `resume {protocol, share_id, secret}`, `extend {ttl_seconds}`, `kick {guest}`, `close {reason}` |
| relay → bridge | `created {share_id, secret, guest_base, expires_at, max_guests}`, `resumed`, `guest_join {guest}`, `guest_left {guest}`, `extended {expires_at}`, `expired`, `error {code, message}` |
| relay → viewer | `joined {guest}`, `host_offline`, `host_online`, `ended {reason}` |

Close codes: 4000 ended, 4001 unauthorized, 4002 protocol, 4004 not found, 4008 too slow, 4009 full,
4010 kicked, 4011 replaced.

### End-to-end layer

Binary payloads between bridge and viewer start with an envelope byte:

- `I` (init, bridge → viewer): `cid [16]` + a sealed `{"t":"init"}`. A fresh random connection id
  per viewer connection.
- `F` (frame, both ways): a sealed frame.

Sealed frame: `seq u64 BE | nonce [12] | AES-256-GCM(plaintext)`, with additional data
`"herdr-share/1" | direction ('B' bridge→viewer, 'G' viewer→bridge) | cid | seq`. Receivers
require `seq` to increase. Plaintext starts with a kind byte: `1` JSON control, `2` terminal output,
`3` keyboard input.

Control messages (`t`): viewer → bridge `hello {name, client}`; bridge → viewer `welcome {mode,
title, label, terminal, cols, rows, expires_at, viewers}` (resets the screen; output follows),
`resize {cols, rows}`, `mode {mode}`, `title`, `expires`, `viewers`, `end {reason}`.

The bridge opens a viewer's attach only after a `hello` that decrypts with the share key, so
connecting without the key costs the sharer nothing.

The key is 32 random bytes per share, base64url in the link fragment. Test vectors shared by the Go
and TypeScript implementations live in `internal/e2ee/e2ee_test.go` and `web/src/lib/crypto.test.ts`.
