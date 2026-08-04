#!/usr/bin/env bash
# install.sh — idempotently make the `kg` engine available in the stable kgai home
# ($KGAI_HOME, default ~/.kgai). Re-runnable on every SessionStart; it short-circuits
# when already up to date, so the cost is paid once per version.
#
# Strategy:
#   1. If a prebuilt release is configured ($KG_RELEASE_BASE), download kg + libkuzu.
#   2. Otherwise build from source (requires `go` and a C compiler).
#   3. Initialize the store on first run.
#
# Prints a short, AI-readable status line to stdout (SessionStart feeds it to Claude).
set -uo pipefail

# BASH_SOURCE is unset when the script is piped in (`curl … | bash`, the by-hand install),
# and `set -u` would abort on it — fall back to $0 so a standalone run still works.
ROOT="${CLAUDE_PLUGIN_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")/.." 2>/dev/null && pwd)}"
KGAI_HOME="${KGAI_HOME:-$HOME/.kgai}"
BIN="$KGAI_HOME/bin/kg"
LIBDIR="$KGAI_HOME/lib"
# Where `kg` becomes runnable from a plain terminal. The plugin's own bin/ shim is only
# on the PATH Claude Code hands its Bash tool; outside Claude Code nothing resolves.
USER_BIN="${KGAI_USER_BIN:-$HOME/.local/bin}"
KUZU_VERSION="${KUZU_VERSION:-0.11.2}"
PLUGIN_VERSION="$(sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$ROOT/.claude-plugin/plugin.json" 2>/dev/null | head -n1)"
PLUGIN_VERSION="${PLUGIN_VERSION:-dev}"
# Prefer prebuilt binaries from the repo's latest GitHub release (no Go/gcc needed). If a
# platform asset is missing (e.g. before the first release), the download fails and we fall
# back to building from source. Override or set empty to force the source build.
KG_RELEASE_BASE="${KG_RELEASE_BASE-https://github.com/kgaidev/kgai/releases/latest/download}"
mkdir -p "$KGAI_HOME/bin" "$LIBDIR"

status() { echo "kgai: $*"; }

# sha256 of stdin. macOS ships shasum, not sha256sum — every hash goes through here so a
# missing GNU coreutils never silently yields an empty digest.
hash_stdin() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum | cut -d' ' -f1
  elif command -v shasum >/dev/null 2>&1; then shasum -a 256 | cut -d' ' -f1
  elif command -v openssl >/dev/null 2>&1; then openssl dgst -sha256 | awk '{print $NF}'
  fi
}

sha256_of() { hash_stdin < "$1"; }

# verify_asset <file> <asset-name> — checks a downloaded file against the release's
# published <asset-name>.sha256. A missing checksum asset (releases before checksums
# shipped) or a machine without a sha256 tool skips verification with a note; a
# MISMATCH fails hard (corrupted or tampered download).
verify_asset() {
  local file="$1" asset="$2" want have
  want="$(curl -fsSL "$KG_RELEASE_BASE/$asset.sha256" 2>/dev/null | awk '{print $1}')"
  if [ -z "$want" ]; then
    status "no checksum published for $asset — skipping verification"
    return 0
  fi
  have="$(sha256_of "$file")"
  if [ -z "$have" ]; then
    status "no sha256 tool on this machine — skipping verification"
    return 0
  fi
  if [ "$want" != "$have" ]; then
    status "⚠️ checksum MISMATCH for $asset — discarding download"
    return 1
  fi
}

# srcver fingerprints what the engine was installed FROM, so a plugin update reinstalls it.
# The plugin version alone covers released installs (each release ships its own binaries);
# the source hash additionally catches edits in a dev checkout.
#
# The version is always part of it, and the hash falls back to "nohash": an empty
# fingerprint would equal the empty file the previous run wrote, so the "already current"
# check below would match forever and the engine would never update again. That is exactly
# what happened on macOS, where the old sha256sum-only hash produced nothing.
srcver() {
  local h
  h="$( { cat "$ROOT/src/go.mod" 2>/dev/null
          find "$ROOT/src" -name '*.go' -type f 2>/dev/null | LC_ALL=C sort |
            while IFS= read -r f; do cat "$f"; done
        } | hash_stdin )"
  printf '%s|%s|%s\n' "$PLUGIN_VERSION" "$KUZU_VERSION" "${h:-nohash}"
}

project_root() {
  # Must agree with ProjectRoot() in src/internal/store/store.go, or this script looks
  # for the store somewhere the engine never puts it. A LINKED worktree resolves to the
  # main worktree (its common dir is <main>/.git), so all worktrees share one store.
  # A submodule keeps its own root: its common dir is <super>/.git/modules/... and does
  # not end in /.git. --path-format needs git 2.31+; older git just keeps the top level.
  local start top common
  start="${CLAUDE_PROJECT_DIR:-$PWD}"
  top="$(git -C "$start" rev-parse --show-toplevel 2>/dev/null)"
  [ -n "$top" ] || { printf '%s\n' "$start"; return; }
  common="$(git -C "$start" rev-parse --path-format=absolute --git-common-dir 2>/dev/null)"
  case "$common" in
    */.git) printf '%s\n' "${common%/.git}" ;;
    *)      printf '%s\n' "$top" ;;
  esac
}

# ---- make `kg` runnable from any terminal ------------------------------------
# The plugin ships bin/kg, but that shim is only on the PATH Claude Code hands its Bash
# tool. The README and the CLI's own output tell people to run `kg init`, `kg sync`,
# `kg context` in their own terminal, where nothing resolved it — so the engine also gets
# a launcher in the standard per-user bin dir, plus (only when that dir is not on PATH,
# which is the macOS default) one marked line in the shell profile that puts it there.
KGAI_MARK="# added by kgai (Claude Code plugin) — puts the kg CLI on PATH"
PATH_NOTE=""
PATH_RC=""

write_launcher() {
  local dest="$USER_BIN/kg"
  mkdir -p "$USER_BIN" 2>/dev/null || return 1
  # Never clobber a different tool's binary that happens to be called kg.
  if [ -e "$dest" ] && ! grep -q 'kgai launcher' "$dest" 2>/dev/null; then
    return 1
  fi
  # A launcher script, not a symlink: the macOS rpath is @loader_path/../lib, resolved
  # against the path the binary was started from. Through a symlink in ~/.local/bin that
  # would look for the native lib in ~/.local/lib and fail to load.
  cat > "$dest.new" <<'LAUNCHER'
#!/usr/bin/env bash
# kgai launcher — installed by the kgai Claude Code plugin (scripts/install.sh).
# Runs the engine from its stable home ($KGAI_HOME, default ~/.kgai) so `kg` works in any
# terminal, not just inside Claude Code. Safe to delete — the plugin writes it again at
# the next session start.
set -euo pipefail
KGAI_HOME="${KGAI_HOME:-$HOME/.kgai}"
BIN="$KGAI_HOME/bin/kg"
if [ ! -x "$BIN" ]; then
  echo "kg: engine not installed at $BIN. Start a Claude Code session with the kgai plugin enabled, or install by hand: https://github.com/kgaidev/kgai#install-the-cli-by-hand" >&2
  exit 127
fi
export LD_LIBRARY_PATH="$KGAI_HOME/lib:${LD_LIBRARY_PATH:-}"
export DYLD_LIBRARY_PATH="$KGAI_HOME/lib:${DYLD_LIBRARY_PATH:-}"
exec "$BIN" "$@"
LAUNCHER
  [ -f "$dest.new" ] || return 1
  if [ -f "$dest" ] && cmp -s "$dest.new" "$dest"; then rm -f "$dest.new"; return 0; fi
  chmod +x "$dest.new" && mv "$dest.new" "$dest"
}

# Appends one marked PATH line to the file the user's login shell reads. Returns 0 only
# when it actually added it, so the status line mentions it exactly once.
ensure_path_entry() {
  case ":${PATH}:" in *":$USER_BIN:"*) return 1 ;; esac
  local rc line
  case "$(basename "${SHELL:-sh}")" in
    zsh)  rc="$HOME/.zshrc";  line="export PATH=\"$USER_BIN:\$PATH\"" ;;
    # A macOS Terminal tab is a LOGIN bash shell, which reads .bash_profile, not .bashrc.
    bash) if [ "$(uname -s)" = "Darwin" ]; then rc="$HOME/.bash_profile"; else rc="$HOME/.bashrc"; fi
          line="export PATH=\"$USER_BIN:\$PATH\"" ;;
    fish) rc="$HOME/.config/fish/config.fish"; line="set -gx PATH $USER_BIN \$PATH" ;;
    *)    rc="$HOME/.profile"; line="export PATH=\"$USER_BIN:\$PATH\"" ;;
  esac
  grep -qF "$USER_BIN" "$rc" 2>/dev/null && return 1
  mkdir -p "$(dirname "$rc")" 2>/dev/null || return 1
  printf '\n%s\n%s\n' "$KGAI_MARK" "$line" >> "$rc" 2>/dev/null || return 1
  PATH_RC="$rc"
  return 0
}

ensure_on_path() {
  PATH_NOTE=""
  if ! write_launcher; then
    PATH_NOTE="note: kept the existing \`kg\` in $USER_BIN (not ours) — the plugin's engine is at $BIN. "
    return 0
  fi
  if ensure_path_entry; then
    PATH_NOTE="\`kg\` is now on your PATH via $USER_BIN (added to $PATH_RC) — open a new terminal to use it there. "
  fi
}

ensure_store() {
  # The store is per-project (<project>/.kgai/store). Create it once per project.
  local proj cfg
  proj="$(project_root)"
  cfg="$proj/.kgai/store/kg.config.json"
  [ -f "$cfg" ] && return 0
  ( cd "$proj" && KGAI_HOME="$KGAI_HOME" LD_LIBRARY_PATH="$LIBDIR:${LD_LIBRARY_PATH:-}" "$BIN" init ) >/dev/null 2>&1 || true
}

report_ready() {
  ensure_on_path
  ensure_store
  echo "$WANT" > "$KGAI_HOME/.srcver"
  # A compact status line, plus a heads-up if there are unresolved conflict branches.
  local extra="$PATH_NOTE"
  local conf
  conf="$(KGAI_HOME="$KGAI_HOME" LD_LIBRARY_PATH="$LIBDIR:${LD_LIBRARY_PATH:-}" "$BIN" conflicts 2>/dev/null | grep -o '"count": *[0-9]*' | grep -o '[0-9]*' || true)"
  if [ -n "$conf" ] && [ "$conf" != "0" ]; then
    extra="${extra}⚠ $conf unresolved decision conflict(s) — run /kgai:kg-conflicts. "
  fi
  # Background auto-sync runs silently; a persistent failure surfaces here, once
  # per session, instead of nagging on every turn. Soft failures (expired SSO,
  # offline) report ok:true with a detail, so check for either.
  local lastsync
  lastsync="$(project_root)/.kgai/store/last-autosync.json"
  if [ -f "$lastsync" ] && grep -qE '"ok": *false|"detail":' "$lastsync" 2>/dev/null; then
    extra="${extra}⚠ background team sync did not sync on its last attempt — tell the user to run \`kg sync\` to see why. "
  fi
  status "engine ready ($1). ${extra}Use /kgai:kg-ask before non-trivial changes; /kgai:kg-decision to record decisions."
}

WANT="$(srcver)"
HAVE="$(cat "$KGAI_HOME/.srcver" 2>/dev/null || true)"

# Already current → fast path. The launcher check runs here too (a few stat calls): the
# engine can be up to date while the user's PATH entry is missing — deleted, new machine,
# or installed before the launcher shipped.
if [ -x "$BIN" ] && [ "$WANT" = "$HAVE" ]; then
  ensure_on_path
  ensure_store
  [ -n "$PATH_NOTE" ] && status "$PATH_NOTE"
  exit 0
fi

# ---- 1. prebuilt release ----------------------------------------------------
if [ -n "${KG_RELEASE_BASE:-}" ]; then
  os="$(uname -s | tr 'A-Z' 'a-z')"; arch="$(uname -m)"
  if [ "$os" = "darwin" ]; then
    # macOS: per-arch binary (arm64 | x86_64) + one universal dylib.
    case "$arch" in x86_64|amd64) arch=x86_64;; aarch64|arm64) arch=arm64;; esac
    lib_asset="libkuzu-darwin-universal.dylib"; lib_file="libkuzu.dylib"
  else
    case "$arch" in x86_64|amd64) arch=x86_64;; aarch64|arm64) arch=aarch64;; esac
    lib_asset="libkuzu-$os-$arch.so"; lib_file="libkuzu.so"
  fi
  if curl -fsSL -o "$KGAI_HOME/bin/kg.new" "$KG_RELEASE_BASE/kg-$os-$arch" 2>/dev/null \
     && curl -fsSL -o "$LIBDIR/$lib_file.new" "$KG_RELEASE_BASE/$lib_asset" 2>/dev/null \
     && verify_asset "$KGAI_HOME/bin/kg.new" "kg-$os-$arch" \
     && verify_asset "$LIBDIR/$lib_file.new" "$lib_asset"; then
    mv "$KGAI_HOME/bin/kg.new" "$BIN"; chmod +x "$BIN"
    mv "$LIBDIR/$lib_file.new" "$LIBDIR/$lib_file"
    report_ready "prebuilt $os-$arch"
    exit 0
  fi
  rm -f "$KGAI_HOME/bin/kg.new" "$LIBDIR/$lib_file.new"
  status "prebuilt download failed, falling back to source build…"
fi

# ---- 2. build from source ---------------------------------------------------
if ! command -v go >/dev/null 2>&1; then
  status "⚠️ ENGINE NOT INSTALLED — kgai will NOT work this session. Prebuilt download failed and the 'go' toolchain is missing for a source build. Fix: install Go (https://go.dev/dl) or check network access to github.com releases, then start a new session."
  exit 0
fi
if ! command -v gcc >/dev/null 2>&1 && ! command -v cc >/dev/null 2>&1; then
  status "⚠️ ENGINE NOT INSTALLED — kgai will NOT work this session. A C compiler (gcc/cc) is required to build the native graph lib. Fix: install Xcode CLT (macOS: xcode-select --install) or gcc, then start a new session."
  exit 0
fi

status "building engine from source (one-time, ~30s)…"
if ! bash "$ROOT/scripts/fetch-libs.sh" >&2; then
  status "⚠️ ENGINE NOT INSTALLED — kgai will NOT work this session. Could not fetch the native graph library (offline? github.com unreachable?). Fix connectivity and start a new session."
  exit 0
fi

case "$(uname -s)/$(uname -m)" in
  Linux/x86_64|Linux/amd64)  libsub="linux-amd64"; rpath='$ORIGIN/../lib' ;;
  Linux/aarch64|Linux/arm64) libsub="linux-arm64"; rpath='$ORIGIN/../lib' ;;
  # dyld has no $ORIGIN — it spells the binary's own directory @loader_path. Building a
  # macOS binary with $ORIGIN yields an engine that cannot load libkuzu.dylib at all.
  Darwin/*)                  libsub="darwin";      rpath='@loader_path/../lib' ;;
  *) status "⚠️ ENGINE NOT INSTALLED — unsupported platform $(uname -s)/$(uname -m). Linux (x86_64/aarch64) and macOS are supported."; exit 0 ;;
esac

if ( cd "$ROOT/src" && CGO_ENABLED=1 go build \
        -ldflags="-X main.version=$PLUGIN_VERSION -extldflags '-Wl,-rpath,$rpath'" \
        -o "$BIN" . ) >&2; then
  cp "$ROOT/third_party/go-kuzu/lib/dynamic/$libsub"/libkuzu.* "$LIBDIR/" 2>/dev/null || true
  report_ready "built from source"
else
  status "⚠️ ENGINE NOT INSTALLED — source build failed (see log above)."
fi
