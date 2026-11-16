# herdr-share

Share a running [Herdr](https://herdr.dev) terminal session — an agent at work, a shell — with
someone else, live, through a relay you run yourself. They open a link in their browser. Whether
they may type is decided when you share and can be changed at any time; stopping the share cuts
them off at once and the link never works again. Your session keeps running either way.

[中文说明](README.zh-CN.md)

- **One link, no accounts.** Viewers need nothing but a browser.
- **You decide who types.** View only by default; switch to “can type” and back while they watch.
- **Self-hosted relay.** One small container (`ghcr.io/jtsang4/herdr-share-relay`), works on private
  networks and air-gapped setups; the relay address is a setting.
- **End-to-end encrypted.** The key lives in the link's `#fragment`, which browsers never send to
  servers. The relay forwards ciphertext it cannot read, alter or replay.
- **Never resizes your view.** Viewers see your terminal at your size; their typing does not
  change it.

| In Herdr: ⌘⇧S opens the Share dialog | In the viewer's browser |
|---|---|
| ![The Share dialog in Herdr](docs/images/share-dialog.png) | ![A viewer typing into a shared terminal](docs/images/viewer.png) |

## How it works

```
 your machine                                   your relay                 viewer
┌────────────────────────────────┐
│ Herdr ── Share dialog (mod)    │
│            │ herdr-share CLI   │
│            ▼                   │  outbound WSS  ┌──────────────┐   WSS   ┌──────────┐
│ bridge (background hc terminal)├───────────────▶│ herdr-share- │◀────────│ browser  │
│            │ terminal.attach   │                │ relay        │         │ (xterm)  │
│            ▼                   │                └──────────────┘         └──────────┘
│ herdr-core: your session t7    │
└────────────────────────────────┘
```

The bridge only makes outbound connections, so your machine needs no open port. Details:
[docs/architecture.md](docs/architecture.md).

## Quick start

**1. Deploy a relay** (once, for you or your team) — see [docs/deploy.md](docs/deploy.md):

```sh
docker run -d --name herdr-share-relay -p 8080:8080 \
  -e HERDR_SHARE_TOKENS=$(openssl rand -hex 16) \
  ghcr.io/jtsang4/herdr-share-relay:latest
```

Put it behind HTTPS for use over the internet (a [compose file with Caddy](deploy/docker-compose.yml) is included).

**2. Add the Share dialog to Herdr** on the machine you share from (macOS, Linux or Windows, with
Node.js 22 or later):

```sh
npx -y herdr-share@latest mod install
```

Nothing is installed globally: npx fetches the package, which downloads the native binary for your
platform from the release and verifies its checksum (it lives inside the package, in npx's cache).
This adds the Share dialog to Herdr and binds **⌘⇧S** (Ctrl+Shift+S on Linux/Windows) to open it — your layout is not changed.
`--no-bind` leaves your `keys.json` alone.

**3. Share.** Focus the terminal in Herdr and press **⌘⇧S**. The first time, enter your relay's
address (and token). Then pick who can type and how long the link works, and click
**Start sharing** — the link is copied. Send it to whoever should watch.

The same from a terminal (`herdr-share` below means `npx --prefer-offline -y herdr-share`, or the
command itself if you installed it globally):

```sh
herdr-share config set relay https://share.example.com
herdr-share config set token <token>
herdr-share start t7 --mode view --ttl 2h --copy   # t7: the terminal (see `hc ls`)
herdr-share mode t7 control                        # let viewers type
herdr-share stop t7                                # the link stops working now
```

**Or ask your coding agent.** Install the skill once and say “share this session with Alice, view
only, for 30 minutes”:

```sh
npx skills add jtsang4/herdr-share --skill herdr-share
```

## Install, update, uninstall

| | With npx (recommended) | Global npm install | Install script / manual |
|---|---|---|---|
| Install | `npx -y herdr-share@latest mod install` | `npm i -g herdr-share && herdr-share mod install` | `curl -fsSL https://raw.githubusercontent.com/jtsang4/herdr-share/main/scripts/install.sh \| sh` |
| Use | `npx --prefer-offline -y herdr-share …` | `herdr-share …` | `herdr-share …` |
| Update | `npx -y herdr-share@latest version` | `npm i -g herdr-share@latest` | run the script again |
| Uninstall | `npx -y herdr-share uninstall` | `herdr-share uninstall && npm uninstall -g herdr-share` | `herdr-share uninstall`, then delete `~/.local/bin/herdr-share` |

- **npx is the default.** Nothing lands in your PATH; the Herdr mod calls
  `npx -y herdr-share@<version>`, served from npx's cache. A global install instead puts the
  `herdr-share` command in npm's normal global bin folder, with its binary inside npm's global
  `node_modules`.
- **No repeated downloads or version checks.** The binary is downloaded once per version and reused.
  `--prefer-offline` makes npx use its cache without asking the registry, so after the first run
  every command works offline, like a global command. Only `@latest` checks for a new version.
- **Updating** (`@latest`, when you want it) fetches the new version once and refreshes the Herdr mod
  automatically. Running shares keep going on the old binary until they end.
- **`herdr-share uninstall`** stops every share, removes the Herdr mod and its key binding, and
  deletes settings and state (`--keep-config` keeps them).
- Windows: use npx or download the zip from [Releases](https://github.com/jtsang4/herdr-share/releases).

The full walkthrough for sharers and viewers is in [docs/usage.md](docs/usage.md).

## Commands

| Command | What it does |
|---|---|
| `herdr-share start <terminal>` | Share a terminal; prints (or `--copy`s) the link. `--mode view\|control`, `--ttl 30m\|2h\|0`, `--label` |
| `herdr-share list` | Running shares |
| `herdr-share status <share>` | Link, mode, viewers, expiry |
| `herdr-share link <share> [--copy]` | Print or copy the link |
| `herdr-share mode <share> view\|control` | Change whether viewers can type |
| `herdr-share extend <share> <lifetime>` | New lifetime counted from now |
| `herdr-share stop <share>\|--all` | End shares |
| `herdr-share config set\|get\|show\|unset` | Relay URL, token, defaults |
| `herdr-share mod install\|uninstall` | The Herdr Share dialog and its key |
| `herdr-share doctor` | Check relay, herdr-core and mod |

`<share>` is the share's id, or the terminal it shares (`t7`, `machine/t7`, or a terminal name).
Every command takes `--json`.

## Requirements

- Sharing: [herdr-core](https://herdr.dev) (`hc`) 0.1.21 or later; the Herdr app for the dialog.
- Relay: Docker, or the `herdr-share-relay` binary from the releases.
- Viewing: a current browser. Plain `http://` works on private networks too.

## Security

Anyone with a link in **can type** mode can run commands in that terminal as you — agents can run
shell commands too. Share such links only with people you trust, keep lifetimes short, and protect
your relay with tokens. Read [docs/security.md](docs/security.md) and [SECURITY.md](SECURITY.md).

## Development

```sh
make web build      # guest client, then ./bin/herdr-share and ./bin/herdr-share-relay
make test lint      # unit tests and linters
make e2e            # isolated end-to-end run: herdr-core session, relay, CLI, real browser
```

See [CONTRIBUTING.md](CONTRIBUTING.md) and [AGENTS.md](AGENTS.md).

## License

[MIT](LICENSE)
