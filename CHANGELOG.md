# Changelog

All notable changes to the kgai plugin are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions match the
git tags (`vX.Y.Z`) and `.claude-plugin/plugin.json`.

## [0.1.10] - 2026-07-24

### Added
- **`kg status` command** (alias `kg info`) — a fast, at-a-glance snapshot of the
  store: identity, whether a sync remote / cloud token is configured
  (`remote_configured`, `sync_transport`, `cloud_configured`), and live graph counts.
  Distinct from `kg doctor`, which stays the integrity/health check (it verifies hash
  chains); `status` skips that work so it's cheap on large stores.
- **AWS/SSO profile per S3 remote** — the `s3://` remote URL now accepts
  `?profile=NAME&region=REGION`. `profile` pins a named shared-config profile (including
  an SSO profile — run `aws sso login --profile NAME` first) to *this* store instead of a
  global `AWS_PROFILE`; `region` overrides the profile/env region. An empty profile keeps
  the full standard AWS credential chain. No new dependency.

### Changed
- **`kg version` reports the release version.** The plugin version from
  `.claude-plugin/plugin.json` is stamped into the binary at build time (`-ldflags -X
  main.version`) and shown alongside `schema_version`. CI now fails a release if the
  pushed tag does not match `plugin.json`, so binaries can never ship with a stale version.
- Sync documentation converged on verified behavior (S3 supported, git experimental).

[0.1.10]: https://github.com/kgaidev/kgai/releases/tag/v0.1.10
