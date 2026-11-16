# Releasing

For maintainers: what a release produces, how to cut one, and one-time setup.

## What a release produces

Pushing a `v*` tag runs `.github/workflows/release.yml`:

1. **GoReleaser** (`goreleaser` job)
   - builds `herdr-share` and `herdr-share-relay` for `darwin` / `linux` / `windows` × `amd64` /
     `arm64`, with the web client embedded;
   - publishes on GitHub Releases:
     - archives `herdr-share_<version>_<os>_<arch>.tar.gz` and `herdr-share-relay_…` (`.zip` on
       Windows) for manual downloads and the install script;
     - raw CLI binaries `herdr-share_<version>_<os>_<arch>[.exe]` for the npm launcher;
     - `checksums.txt` (sha256);
   - pushes the multi-arch image `ghcr.io/jtsang4/herdr-share-relay:<version>`; final releases also
     move `<major>.<minor>` and `latest`.
2. **npm** (`npm` job, after GoReleaser succeeds)
   - sets `build/npm/package.json`'s version from the tag;
   - publishes `herdr-share` through npm trusted publishing, with provenance;
   - pre-releases (`v0.2.0-rc.1`) go to the `next` dist-tag and do not move `latest`.

The npm launcher downloads `herdr-share_<version>_<os>_<arch>[.exe]`. If you change the naming in
`.goreleaser.yaml`, change `build/npm/lib/launcher.js` too.

## Cutting a release

1. Make sure `main` is green in CI, and update `CHANGELOG.md`.
2. Tag and push:
   ```sh
   git tag -a v0.1.2 -m "herdr-share 0.1.2"
   git push origin v0.1.2
   ```
3. Check the release page, `docker pull ghcr.io/jtsang4/herdr-share-relay:0.1.2` and
   `npx -y herdr-share@0.1.2 version`.

## One-time setup

### npm trusted publishing

Trusted publishing can only be configured for a package that already exists on npmjs.com, so the
first version is published by hand:

1. Push the tag. The GoReleaser job succeeds; the npm job fails because no trusted publisher is
   configured yet — expected.
2. Publish the same version locally (logged in to npm, with 2FA):
   ```sh
   cd build/npm
   npm version <version> --no-git-tag-version   # e.g. 0.1.1, without the v
   npm publish --access public --provenance=false
   git checkout package.json
   ```
3. On npmjs.com, open `herdr-share` → **Settings → Trusted Publisher** → GitHub Actions:
   organization/user `jtsang4`, repository `herdr-share`, workflow `release.yml`, no environment,
   and allow **npm publish**.
4. A new trusted publisher must be used within **2 days**, or it expires; publish a patch soon after.
5. Once automatic publishing works, require 2FA and disallow tokens in the package settings.

### GHCR image visibility

After the first push, check **Packages → herdr-share-relay → Package settings** on GitHub: the
package must be **public** (anonymous pulls) and linked to `jtsang4/herdr-share`.
