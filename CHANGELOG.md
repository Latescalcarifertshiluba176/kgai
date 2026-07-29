# Changelog

All notable changes to the kgai plugin are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions match the
git tags (`vX.Y.Z`) and `.claude-plugin/plugin.json`.

## [0.1.12] - 2026-07-29

### Fixed
- **`git worktree` no longer starts an empty graph** (#4). A linked worktree resolved to
  itself as the project, so `git worktree add ../feature-x` produced a second, empty
  store in that directory: `/kgai:kg-ask` returned nothing and decisions recorded there
  were stranded, reachable only through `kg sync`. Worktrees now resolve to the main
  worktree, so every worktree of a project reads and writes one graph — matching the
  design, where the KG is per project and deliberately branch-agnostic. Submodules are
  unaffected: they remain their own project. `scripts/install.sh` resolves the root the
  same way, so it no longer re-initializes the store on every run inside a worktree.

  *Upgrading:* if you already recorded decisions while working inside a worktree, they
  are in `<worktree>/.kgai/store` and the plugin will now look in the main worktree
  instead. Point `KGAI_STORE` at the old path to read it, or `kg sync` both stores
  against one remote to merge them.

## [0.1.11] - 2026-07-28

### Added
- **`kg context --about` reads the decision texts, not just element names.** A question
  phrased in the words of what was decided — "should I hide drafts from the list?" —
  now surfaces the element that decision shaped (superseded dead ends included), even
  when it shares no word with the element's name. Same deterministic lexical scorer as
  `kg search`, no embeddings; naming the element directly remains the strongest signal.
  Costs one scan of the decision texts, paid only by `--about` queries (+56 ms at 10k
  decisions, ~+0.6 s at 100k).
- `warmbench` dev tool — times the individual Cypher reads behind `context`/`search`
  with the graph held open, separating query cost from CLI startup cost.

### Changed
- **`kg context` is ~2× faster at scale.** Head decisions are resolved after ranking,
  for the returned elements only, instead of graph-wide on every read (the head query
  alone: 633 ms → 43 ms at 1,000,000 decisions; cold `kg context` 1.7 s → 0.9 s).
  Output is byte-identical.
- The skill now tells the model it is the semantic layer: matching is word overlap by
  design, so rephrase with the recorded vocabulary before concluding "no record".

### Fixed
- **`kg conflicts` output is deterministically ordered** — competing heads newest-first,
  elements by id. Previously the order was whatever the scan produced, so the same store
  could describe a branch two ways on consecutive reads. (Canonical export was never
  affected.)

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

[0.1.11]: https://github.com/kgaidev/kgai/releases/tag/v0.1.11
[0.1.10]: https://github.com/kgaidev/kgai/releases/tag/v0.1.10
