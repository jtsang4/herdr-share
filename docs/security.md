# Security model

## What a link grants

A share link grants exactly what the share's mode says, to **anyone** who has it:

- **View only**: watching the terminal's output, including its recent history at the moment they
  join. Anything printed there — file contents, environment variables, tokens shown by a command —
  is visible.
- **Can type**: everything above, plus typing into the terminal **as you**. In a shell that means
  running any command with your permissions; an agent session can run commands too. Treat a
  can-type link like a password to your account for as long as it lives.

Mitigations you control: view-only by default, short lifetimes, switching back to view only (takes
effect immediately), stopping the share (the link dies for good), and sending links only over
channels you trust. Prefer not to give typing access to agent sessions running without permission
prompts.

## What the relay can and cannot do

The relay **cannot** read the session or forge input:

- The 32-byte key exists only in the link's URL fragment, which browsers do not send to servers.
- Frames are AES-256-GCM with additional data binding each frame to its direction, its connection
  id and a strictly increasing sequence number: a relay cannot read, alter, reorder or replay frames,
  or replay them into another connection.
- The bridge enforces the mode itself: in view-only mode no input reaches herdr-core, whatever
  arrives.

The relay **can** see metadata: when shares exist, how many viewers connect and from which IP
addresses, and the volume and timing of traffic. It can deny service (drop or delay traffic). A
malicious *operator* could also serve a modified viewer page that leaks the key from the fragment —
so viewers trust the relay operator to serve the genuine page. Run your own relay.

## Relay hardening

- Set `HERDR_SHARE_TOKENS` unless only trusted people can reach the relay. `HERDR_SHARE_OPEN_NETWORKS`
  exempts trusted networks; only list networks whose members you trust, and enable
  `HERDR_SHARE_TRUST_PROXY` only behind a proxy you control (otherwise `X-Forwarded-For` is ignored,
  so it cannot be forged to get in).
- Serve it over HTTPS on the internet.
- The page is served with a strict CSP (no inline script except the hashed boot script, no
  third-party origins), `Referrer-Policy: no-referrer`, `X-Frame-Options: DENY`; viewer WebSockets
  check `Origin`.
- Rate limits apply per IP to share creation and viewer connections; viewer counts and lifetimes are
  capped.

## On the sharer's machine

- Share records (`state/shares/*.json`, containing links) and control sockets are private to your
  user (`0600`, folders `0700`). Anyone who can act as your user can already control your terminals.
- The relay token is stored in `config.json` (`0600`) and passed to the background bridge through
  its environment, never on a command line.

## Reporting vulnerabilities

See [SECURITY.md](../SECURITY.md).
