# Contributing

Thanks for helping! Bug reports, ideas and pull requests are welcome.

## Setup

You need Go (the version in `go.mod`; the toolchain downloads itself), Node.js 24 or later, and for
end-to-end tests [herdr-core](https://herdr.dev) (`hc`) and Chrome or Playwright's Chromium.

```sh
make web build   # web client, then ./bin/herdr-share and ./bin/herdr-share-relay
make test        # Go unit tests, svelte-check and web unit tests
make lint        # golangci-lint, oxlint and oxfmt
make e2e         # isolated end-to-end run (see below)
make hooks       # optional pre-commit hooks (lefthook)
```

`make help` lists every target.

## Tests

- **Unit tests** run everywhere, including CI on Linux, macOS and Windows.
- **herdr-core tests** (`internal/hcclient`, `test/e2e`) need a core. Always point them at an
  isolated session so they never touch your own terminals:
  ```sh
  export HERDR_SHARE_HC_TEST=1 HC_SESSION=hstest XDG_STATE_HOME=$(mktemp -d)
  hc server start && go test ./internal/hcclient/ ./test/e2e/; hc server stop
  ```
- **`scripts/e2e.sh`** builds a throwaway environment (its own herdr-core session, herdr-share home,
  Herdr app home and relay), then drives the CLI, checks the Herdr mod with `herdr-app check`, and
  plays a viewer in a real browser (`test/browser/guest.mjs`). `--relay docker` runs the relay from
  the container image; `PW_CHANNEL=chrome` uses your installed Chrome with a throwaway profile;
  `--keep` keeps screenshots and logs.

## Conventions

- Go: standard library first, `log/slog`, `context` through blocking calls, errors wrapped with
  `%w`. Must build and lint for Linux, macOS and Windows (`GOOS=windows golangci-lint run ./...`).
- Web: Svelte 5 runes and SvelteKit (adapter-static). Use the project skills in `.agents/skills/`
  and run `npx @sveltejs/mcp svelte-autofixer` on components you change.
- The Herdr mod (`herdr/herdr-share/mods/herdr-share`, also the herdr.dev listing): `herdr-app
  check` on an installed copy must pass (checking the source folder writes type files into it); try it in a Herdr window started with
  its own `HERDR_APP_HOME`, and remember `herdr-share mod install` installs the copy embedded in the
  binary (rebuild first).
- Keep `docs/` in step with behaviour, protocol and configuration changes.
- Commits follow [Conventional Commits](https://www.conventionalcommits.org) (`feat:`, `fix:`,
  `docs:`, `test:`, `ci:`, `chore:` …); the release notes are generated from them.

## Releasing

See [docs/releasing.md](docs/releasing.md).
