# Security Policy

herdr-share can give other people typing access to a terminal running as you, so security reports
are taken seriously.

## Reporting a vulnerability

Please report vulnerabilities privately through
[GitHub private vulnerability reporting](https://github.com/jtsang4/herdr-share/security/advisories/new).
Do not open a public issue.

Include:

- the affected version or commit
- steps to reproduce, or a proof of concept
- the impact you believe it has

You should receive an initial response within a few days. Once a fix is available, a security
advisory is published together with the release.

## Supported versions

Until 1.0, only the latest release receives security fixes.

## Security model

The threat model is described in [docs/security.md](docs/security.md). In short:

- A share link grants its mode to anyone who has it; a link in *can type* mode lets them run
  commands as you.
- The relay cannot read sessions or forge input (end-to-end encryption, keys only in the link
  fragment), but it sees metadata and serves the viewer page, so run your own.
- Permissions are enforced by the bridge on the sharer's machine, never by the relay or the page.
