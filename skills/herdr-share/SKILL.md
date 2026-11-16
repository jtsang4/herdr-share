---
name: herdr-share
description: Share a live Herdr (herdr-core) terminal session with other people through a self-hosted relay, and manage running shares. Use when the user asks to share their current session, an agent's terminal or a Herdr pane; to send someone a link to watch or take over; to let viewers type or make a share view-only; to extend, list or stop shares; or to set up or check herdr-share (relay URL, token, Herdr mod).
---

# herdr-share

`herdr-share` shares one herdr-core terminal through a relay. Viewers open a link in a browser.
A share is either **view** (watch only) or **control** (viewers can type into the terminal as the
user). Stopping a share disconnects everyone and kills the link for good; the terminal keeps running.

## Running the command

Use `npx --prefer-offline -y herdr-share` (the default; nothing needs installing — the package
downloads and verifies the native binary once, and `--prefer-offline` skips the registry check after
the first run), or `herdr-share` if the user installed it globally. Below,
`herdr-share` stands for whichever applies. Add `--json` to every command you parse.
Never put the relay token on a command line that you echo back to the user, and never print it.

## The usual request, end to end

"Share this session, viewers can type, 24 hours, relay https://share.example.com" means:

1. If the request names a relay, save it: `herdr-share config set relay <url>` (it is remembered, so
   later requests need not repeat it). If it names none and `herdr-share config get relay` is empty,
   ask for the relay address; never invent one.
2. `herdr-share start "$HC_TERMINAL_ID" --mode control --ttl 24h --json`.
3. Reply with the **full** link, the mode, when it ends, and how to stop it ("ask me to stop sharing",
   or `herdr-share stop <id>`). For control mode, say once that anyone with the link can run
   commands as the user — the user asked for it, so do not ask again for confirmation.

If the relay answers that it needs a token, ask the user for it and `config set token` it.

## Which terminal

- **"Share this session / share me"** from inside Herdr: the current terminal is `$HC_TERMINAL_ID`
  (for example `t7`). Use it as is.
- **Another terminal**: list them with `hc ls` (addresses like `t7`, `machine/t7`, or names) and pick
  the one the user means; ask if it is ambiguous.

## Share

```sh
herdr-share start "$HC_TERMINAL_ID" --mode view --ttl 2h --json [--label "<space · tab>"] [--copy]
```

- `--mode view` is the default. Use `--mode control` only when the user explicitly wants viewers to
  type ("可操作", "can type", "let them take over", "control mode").
- `--ttl`: `30m`, `2h`, `8h`, … or `0` for until stopped (the relay may cap it). Default from config.
- The JSON has `link`, `id`, `mode`, `expires_at`, `existing` (already shared: the existing link is
  returned). Give the user the **full** link — the part after `#` is the key; a truncated link does
  not work. `--copy` also puts it on the clipboard.

## Manage

```sh
herdr-share list --json                       # running shares: id, terminal, mode, guests, expires_at
herdr-share status <share|terminal> --json    # one share, with viewer names
herdr-share mode <share|terminal> control     # or: view — applies at once to everyone connected
herdr-share extend <share|terminal> 30m       # new lifetime counted from now; 0 = until stopped
herdr-share link <share|terminal> --copy      # print/copy the link again
herdr-share stop <share|terminal>             # or: herdr-share stop --all
```

`<share>` is a share id from `list`, or the terminal it shares.

## Setup and checks

```sh
herdr-share doctor --json                     # relay, herdr-core and Herdr mod checks
herdr-share config set relay https://share.example.com
herdr-share config set token <token>          # only if the relay requires one; ask the user for it
herdr-share mod install                       # Herdr Share dialog, bound to ⌘⇧S (Ctrl+Shift+S)
```

If `doctor` or `start` says no relay is configured, ask the user for their relay address (and token)
— do not invent one. Deploying a relay is a separate task (container
`ghcr.io/jtsang4/herdr-share-relay`, see https://github.com/jtsang4/herdr-share/blob/main/docs/deploy.md).

## Errors

| Message | Meaning |
|---|---|
| `no relay configured` | `config set relay …` first |
| `this relay requires a token` / `unauthorized` | `config set token …` |
| `no terminal "…"` | wrong address; check `hc ls` |
| `does not look like a herdr-share relay` | wrong relay URL |

## Updating and removing

- Update (only when the user asks): `npx -y herdr-share@latest version` (or
  `npm i -g herdr-share@latest`); the Herdr mod is refreshed automatically.
- Remove: `herdr-share uninstall` (stops shares, removes the mod and its key, and settings;
  `--keep-config` keeps settings), then `npm uninstall -g herdr-share` if it was installed globally.
