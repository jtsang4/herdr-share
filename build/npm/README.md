# herdr-share

Share a live [Herdr](https://herdr.dev) terminal session through your own relay. Viewers open a
link in their browser; you decide whether they may type, and can change it or stop at any time.

This package provides the `herdr-share` command. When installed (or on first run) it downloads the
native binary for your platform from
[GitHub Releases](https://github.com/jtsang4/herdr-share/releases), verifies its SHA-256 checksum
and keeps it inside the package — in npx's cache, or in npm's global `node_modules` after a global
install. Nothing else is written outside the package.

```sh
npx -y herdr-share@latest mod install                         # the Share dialog (⌘⇧S) in Herdr
npx --prefer-offline -y herdr-share config set relay https://share.example.com
npx --prefer-offline -y herdr-share start t7 --copy           # share terminal t7
```

`--prefer-offline` uses npx's cache without asking the registry, so after the first run commands
work offline; the binary is downloaded once per version.

No global install is needed; `npm install -g herdr-share` works too.

- **Update:** `npx -y herdr-share@latest version`; the Herdr mod is refreshed automatically.
- **Uninstall:** `npx -y herdr-share uninstall` (stops shares, removes the Herdr mod and settings),
  then `npm uninstall -g herdr-share` if you installed it globally.

See the [project README](https://github.com/jtsang4/herdr-share#readme) for details.
