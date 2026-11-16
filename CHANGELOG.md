# Changelog

Release notes for each version are published on the
[GitHub releases page](https://github.com/jtsang4/herdr-share/releases), generated from commit
messages. Notable changes are summarized here.

## 0.1.1 — 2026-11-17

- Install and run with npm: `npx -y herdr-share@latest mod install`. No global install needed;
  `npm i -g herdr-share` puts the command in npm's normal global bin folder. The native binary is
  downloaded once per version, checksum-verified, and kept inside the package.
- `herdr-share uninstall` stops every share and removes the Herdr mod, its key binding and settings.
- Updating refreshes the Herdr mod automatically.
- An agent skill (`npx skills add jtsang4/herdr-share --skill herdr-share`) so coding agents can
  share, change and stop sessions on request.
- A share now ends after 10 minutes without reaching its relay, instead of retrying forever.

## 0.1.0 — 2026-11-17

First release.

- Share a herdr-core terminal from the Herdr Share dialog (⌘⇧S; no other changes to your Herdr window) or the `herdr-share` CLI.
- View-only or can-type shares, switchable live; lifetimes with extend; stop at any time.
- Self-hosted relay as a multi-arch container image and binaries; tokens, lifetime caps, viewer
  limits, rate limits.
- End-to-end encryption between the sharer's bridge and viewers.
- Browser viewer page in English and Simplified Chinese.
