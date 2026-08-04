#!/usr/bin/env bash
# SessionStart + Stop hook — fire-and-forget team sync, no LLM involved.
#
# This script only SPAWNS `kg sync --auto` detached and exits immediately (~10 ms),
# so neither the user nor the model ever waits on the network. The engine makes the
# spawn a no-op unless it is actually useful:
#   - no store here, or no remote configured  → exits silently in a few ms
#   - synced less than the cooldown (60 s) ago → skipped
#   - another sync/write holds the store lock  → skipped (never queues)
# Real attempts record their outcome in <store>/last-autosync.json; a persistently
# failing sync surfaces once per session in the install status line — never louder.
KGAI_HOME="${KGAI_HOME:-$HOME/.kgai}"
BIN="$KGAI_HOME/bin/kg"
[ -x "$BIN" ] || exit 0
export LD_LIBRARY_PATH="$KGAI_HOME/lib:${LD_LIBRARY_PATH:-}"
export DYLD_LIBRARY_PATH="$KGAI_HOME/lib:${DYLD_LIBRARY_PATH:-}"
setsid "$BIN" sync --auto </dev/null >/dev/null 2>&1 &
exit 0
