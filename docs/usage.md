# Using herdr-share

This guide walks through sharing from both sides: **you**, sharing a session from Herdr, and
**them**, opening your link.

## Rules of the game

- You share **one terminal** (an agent session, a shell), never your machine.
- When you share, you choose whether viewers may type: **View only** (default) or **Can type**.
  Anyone who opens the link gets that permission straight away; there is no approval step. You can
  change it at any time, for everyone at once.
- A link works until you stop it, it expires, or the shared session ends. After that it never works
  again.
- Stopping a share disconnects viewers; it never stops your session.

## Setting up (once)

Someone deploys a relay ([deploy.md](deploy.md)) and gives you its address, plus a token if it uses
one. That is all you need: the first time you share, tell the agent (or the dialog, or
`herdr-share config set relay …`) the address; it is remembered.

Optional: `npx -y herdr-share@latest mod install` adds the Share dialog to Herdr (⌘⇧S), and
`herdr-share doctor` checks the relay, herdr-core and the dialog.

## Sharing (you)

### From your coding agent (the simplest way)

No install. In the agent's session in Herdr, say:

> Use `npx skills use jtsang4/herdr-share --skill herdr-share` to share this session. Viewers can
> type, for 24 hours. Relay: https://share.example.com

`npx skills use` hands the agent the herdr-share skill for this request; the agent runs herdr-share
through npx, shares its own terminal (`$HC_TERMINAL_ID`) and replies with the full link, when it
ends and how to stop it. Name the relay the first time only. In the same conversation you can go on:
“let them type”, “view only again”, “who is watching?”, “give it another hour”, “stop sharing”. In a
new conversation, start with the same `npx skills use …` sentence.

To keep the skill around instead, install it once: `npx skills add jtsang4/herdr-share --skill
herdr-share`.

### From Herdr

After `npx -y herdr-share@latest mod install` (once):

1. Focus the terminal you want to share and press **⌘⇧S** (Ctrl+Shift+S on Linux and Windows).
   The **Share session** dialog opens for that terminal. The shortcut is the only thing
   herdr-share adds to Herdr: no buttons, no layout changes. It is listed in Herdr's Keys screen
   (⌘/) as *mod open herdr-share*. Without the key binding, open the dialog with
   `herdr-app do mod.open --name herdr-share`.
2. Choose:
   - **Who can type** — *View only* or *Can type*. Choosing *Can type* shows a reminder that viewers
     will be able to run commands as you.
   - **Link works for** — 30 min, 2 hours, 8 hours, or no limit (a relay may cap this).
3. Click **Start sharing**. The link is copied to your clipboard and a notification confirms it.
   Send it to whoever should watch.

Open the dialog again on a shared terminal to manage the share:

- **Copy link** — copy it again.
- **Who can type** — switch between view only and can type. Viewers see the change immediately.
- **+30 min / 2 hours from now** — extend the share.
- **Watching now** — who is connected (viewers can pass a display name; otherwise “Guest 1”, …).
- **Stop sharing** — viewers are disconnected and the link stops working.
- **Other shared sessions** — every other running share, each with its own Stop.
- **Relay settings** — change the relay address or token.

Closing the dialog changes nothing: the share keeps running in the background.

### From a terminal

`herdr-share` is `npx --prefer-offline -y herdr-share` (or the command, if installed globally):

```sh
herdr-share start t7 --mode view --ttl 2h --copy --label "acme-api · review"
herdr-share list
herdr-share status t7
herdr-share mode t7 control      # or: view
herdr-share extend t7 30m        # 30 minutes from now; 0 = until stopped
herdr-share stop t7              # or: herdr-share stop --all
```

`t7` is the terminal's address from `hc ls` (a terminal on another machine is `machine/t7`; a
named terminal can be given by name). Defaults for `--mode` and `--ttl` come from
`herdr-share config set mode …` and `… ttl …`.

### When a share ends on its own

- **It expires.** Viewers see “The share link expired.”
- **The shared session ends** (the program exits, the pane is closed). The share ends with it.
- **The relay connection drops.** The bridge reconnects by itself; viewers see “The sharer is
  offline…” and continue where they were once it is back. If the relay restarts, its shares are
  gone: start sharing again and send the new link.

## Commands

`herdr-share` below is `npx --prefer-offline -y herdr-share`, or the command itself after a global
install. Every command takes `--json`.

| Command | What it does |
|---|---|
| `herdr-share start <terminal>` | Share a terminal; prints (or `--copy`s) the link. `--mode view\|control`, `--ttl 30m\|2h\|7d\|0`, `--label` |
| `herdr-share list` | Running shares |
| `herdr-share status <share>` | Link, mode, viewers, expiry |
| `herdr-share link <share> [--copy]` | Print or copy the link |
| `herdr-share mode <share> view\|control` | Change whether viewers can type |
| `herdr-share extend <share> <lifetime>` | New lifetime counted from now |
| `herdr-share stop <share>\|--all` | End shares |
| `herdr-share config set\|get\|show\|unset` | Relay URL, token, defaults |
| `herdr-share mod install\|uninstall` | The Herdr Share dialog and its key |
| `herdr-share doctor` | Check relay, herdr-core and mod |
| `herdr-share uninstall` | Stop shares, remove the mod and settings |

`<share>` is the share's id, or the terminal it shares (`t7`, `machine/t7`, or a terminal name).

## Installing (optional)

You don't need to install anything: npx runs herdr-share from its cache, downloading the native
binary once per version and verifying its checksum; `--prefer-offline` skips the registry check after
the first run. If you prefer a command on your PATH:

| | Global npm install | Install script |
|---|---|---|
| Install | `npm i -g herdr-share` | `curl -fsSL https://raw.githubusercontent.com/jtsang4/herdr-share/main/scripts/install.sh \| sh` (to `~/.local/bin`) |
| Update | `npm i -g herdr-share@latest` | run the script again |
| Remove | `herdr-share uninstall && npm uninstall -g herdr-share` | `herdr-share uninstall`, then delete `~/.local/bin/herdr-share` |

On Windows, use npx or the zip from [Releases](https://github.com/jtsang4/herdr-share/releases).

## Updating and uninstalling

- **Update**: `npx -y herdr-share@latest version` (or `npm i -g herdr-share@latest`). That is the only
  command that checks for a new version; it downloads it once and refreshes the Herdr mod. Running
  shares finish on the old binary.
- **Uninstall**: `herdr-share uninstall` stops every share, removes the Herdr mod and its key binding,
  and deletes settings and state (`--keep-config` keeps them). With npx there is nothing else to
  remove; after a global install, `npm uninstall -g herdr-share`.

## When does a share's process end?

The bridge for a share is a background herdr-core terminal. It exits — and herdr-core removes its
record — when you stop the share, when it expires (enforced on your machine as well as by the
relay), when the shared terminal ends, when the relay no longer knows the share (e.g. it restarted),
after 10 minutes without reaching the relay, or after 2 minutes without herdr-core. Closing the Herdr
window does not stop it. `herdr-share list` shows every share still running.

## Watching (them)

1. Open the link in a browser. It looks like `https://share.example.com/s/<id>#<key>` — the part
   after `#` is the key; a link copied without it does not work.
2. The page connects and shows the live terminal at the sharer's size, including recent output.
3. The top bar shows:
   - the session's title, and the space/tab it belongs to;
   - **End-to-end encrypted** — hover for details;
   - how many people are watching and how much time is left;
   - **View only** or **You can type**.
4. In **You can type** mode, click the terminal and type: keystrokes go straight to the shared
   session (the frame is outlined in amber). In **View only** mode the keyboard does nothing. When
   the sharer changes the mode, a notice says so.
5. If the connection drops, the page reconnects by itself. When the share ends, the page says why;
   the last screen stays visible.

To show a name to the sharer instead of “Guest 1”, add `?name=…` before the `#`:
`https://share.example.com/s/<id>?name=Alice#<key>`.

The page is in English, or Simplified Chinese when the browser prefers Chinese.

## Troubleshooting

| Symptom | What to check |
|---|---|
| “no relay configured” | `herdr-share config set relay https://…` |
| “this relay requires a token” | `herdr-share config set token …` |
| “does not look like a herdr-share relay” | the relay URL, or a proxy in front answering instead |
| Viewer sees “This link does not work” | the link was cut off; it must include everything after `#` |
| ⌘⇧S does nothing | `herdr-share mod install` again; it reports if the key was taken |
| Anything else | `herdr-share doctor`, then the bridge log path from `herdr-share status <share> --json` |
