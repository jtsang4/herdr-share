# Agent Guidelines for herdr-share

herdr-share shares one running Herdr (herdr-core) terminal with other people through a self-hosted
relay. Viewers open a link in a browser; the sharer decides, and can change at any time, whether
they may type. Traffic between the sharer's bridge and viewers is end-to-end encrypted.

## Source of truth

- `docs/architecture.md` describes the components, the attach strategy and the wire protocol;
  `docs/security.md` the threat model; `docs/usage.md` the user flows; `docs/deploy.md` the relay's
  configuration. Read the relevant one before changing behaviour, and update it in the same change.
- herdr-core's protocol is documented in its repository (`schema/README.md` in herdrdev/herdr-core)
  and by `hc schema`; the Herdr mod API by `~/.config/herdr-app/AGENTS.md` and its `.d.ts` files.

## Layout

- `cmd/herdr-share`: the sharer's CLI (`internal/cli`); `herdr-share serve` is the bridge process.
- `cmd/herdr-share-relay`: the relay (`internal/relay`).
- `internal/bridge`: one share — attaches, permissions, relay connection, reconnects.
- `internal/hcclient`: herdr-core client over `hc connect` (framed JSON-RPC).
- `internal/e2ee`, `internal/wire`: encryption and message formats (mirrored in `web/src/lib/`).
- `internal/guest`: a Go viewer, used by tests.
- `web/`: the viewer page, a SvelteKit SPA. `web/embed.go` embeds `web/build/app`; `web/build/.gitkeep`
  must stay so Go compiles without a web build.
- `herdr/herdr-share/`: the Herdr mod's herdr.dev listing (`RECIPE.md`, `preview.png`, `keys.json`)
  and its only source, `mods/herdr-share/`, embedded into the CLI by `herdr/embed.go` and installed
  by `herdr-share mod install`. Keep `RECIPE.md` in step with the mod.
- `build/package/Dockerfile`: the release image (GoReleaser, prebuilt binaries); `Dockerfile`: the
  image from source.
- `build/npm`: the `herdr-share` npm launcher. It contains no binary; it downloads release assets
  named `herdr-share_<version>_<os>_<arch>[.exe]`, which must stay in sync with `.goreleaser.yaml`.
- `skills/herdr-share`: the user-facing agent skill (`npx skills add jtsang4/herdr-share --skill
  herdr-share`). Keep it in step with the CLI's commands and flags.
- `docs/releasing.md`: how releases, the image and npm publishing work.

## Commands

```sh
make web build    # web client, then ./bin/herdr-share and ./bin/herdr-share-relay
make test         # go test, svelte-check, vitest, npm launcher tests
make lint         # golangci-lint, oxfmt, oxlint
make e2e          # scripts/e2e.sh: isolated herdr-core, relay, CLI, mod check, real browser
```

- Tests that need herdr-core run only with `HERDR_SHARE_HC_TEST=1`, against an isolated session:
  `HC_SESSION=<name> XDG_STATE_HOME=<tmp> XDG_CONFIG_HOME=<tmp>`. Never run them, or start shares,
  in the developer's own session.
- To try the mod, start a separate Herdr window with its own `HERDR_APP_HOME` (and the same isolated
  hc variables, plus `HERDR_RELEASES_URL=file:///nonexistent`), then `herdr-app shot`. Never automate
  pointer or keys on the developer's display.
- When piping a command into `tail`/`grep`, its exit code is lost; check lint and test results
  explicitly before committing.

## Conventions

### Go

- Prefer the standard library; add a dependency only when it clearly pays for itself, at its latest
  release.
- Must work on macOS, Linux and Windows. Use `path/filepath`; put OS-specific code behind build
  constraints. Lint with `GOOS=windows` and `GOOS=darwin` too.
- Use `log/slog`, pass `context.Context` through blocking calls, wrap errors with `%w`.
- Permissions are enforced in the bridge only. Never trust the relay or the page for them.
- Keys, tokens and links must never be logged or put on command lines.

### Web

- Svelte 5 runes and SvelteKit with `adapter-static`; a client-rendered SPA served by the relay.
- Use the project skills `svelte-code-writer` and `svelte-core-bestpractices`, and run the Svelte
  autofixer on changed components.
- Tooling: `oxlint`, `oxfmt`, `svelte-check`, `vitest`. English by default; Simplified Chinese via
  `src/lib/i18n.ts`.
- Visual direction: minimal and flat; the terminal is the content. Screenshot changes in a browser.

### Dependencies

- Keep dependencies on their latest versions within compatibility limits. TypeScript stays on 6.x
  until SvelteKit and svelte-check support 7.x.
- Dependabot opens weekly update PRs for Go modules, web packages, GitHub Actions and Docker images.

### Skills

- Project skills live in `.agents/skills/`; `.claude/skills/` contains relative symlinks to them.
- Third-party skills are managed with `npx skills` and recorded in `skills-lock.json`.

### General

- Do not hardcode paths, hostnames or credentials from a developer's machine.
- Use Conventional Commits (`feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `ci:`, `chore:`).
