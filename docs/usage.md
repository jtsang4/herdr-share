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

1. Someone deploys a relay ([deploy.md](deploy.md)) and gives you its address and, if it uses one,
   a token.
2. Install herdr-share and the Herdr mod:
   ```sh
   curl -fsSL https://raw.githubusercontent.com/jtsang4/herdr-share/main/scripts/install.sh | sh
   ```
3. Tell herdr-share where the relay is. Either open the Share dialog (below) — it asks the first
   time — or:
   ```sh
   herdr-share config set relay https://share.example.com
   herdr-share config set token <token>   # only if the relay requires one
   herdr-share doctor                     # checks relay, herdr-core and the mod
   ```

## Sharing (you)

### From Herdr

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

```sh
herdr-share start t7 --mode view --ttl 2h --copy --label "voltdance · review"
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
