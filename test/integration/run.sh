#!/bin/bash
# Run the PTY-based integration checks against a freshly started simulator.
# This is test-only scaffolding: it never touches real hardware and is not
# part of the production binary.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
WORK=$(mktemp -d "${TMPDIR:-/tmp}/vaultkey-it.XXXXXX")
SIM_PID=""

cleanup() {
  if [ -n "$SIM_PID" ]; then
    kill "$SIM_PID" 2>/dev/null || true
    wait "$SIM_PID" 2>/dev/null || true
  fi
  rm -rf "$WORK"
}
trap cleanup EXIT INT TERM

if ! command -v python3 >/dev/null 2>&1; then
  echo "python3 is required for the integration checks" >&2
  exit 1
fi

echo "building test binary"
(
  cd "$ROOT"
  CGO_ENABLED=0 go build -trimpath -o "$WORK/vaultkey" ./cmd/vaultkey
)

export VK="$WORK/vaultkey"
export VK_WORK="$WORK"
export PORT="$WORK/tty"
: > "$WORK/sim.log"

echo "starting PTY simulator"
python3 "$ROOT/test/integration/sim.py" &
SIM_PID=$!
for _ in $(seq 1 100); do
  [ -e "$PORT" ] && break
  sleep 0.05
done
if [ ! -e "$PORT" ]; then
  echo "simulator did not create $PORT" >&2
  cat "$WORK/sim.log" >&2 || true
  exit 1
fi

STATUS=0
echo
echo "== CLI checks =="
if ! "$ROOT/test/integration/check_cli.sh"; then
  STATUS=1
fi

# Restart the simulator so prompt checks start from a known clean state.
kill "$SIM_PID" 2>/dev/null || true
wait "$SIM_PID" 2>/dev/null || true
rm -f "$PORT"
printf '%s\n' '{"locked": false, "authorized": true, "drop": ""}' > "$WORK/state.json"
: > "$WORK/sim.log"
python3 "$ROOT/test/integration/sim.py" &
SIM_PID=$!
for _ in $(seq 1 100); do
  [ -e "$PORT" ] && break
  sleep 0.05
done

echo
echo "== hidden-prompt checks =="
if ! python3 "$ROOT/test/integration/check_prompts.py"; then
  STATUS=1
fi

echo
if [ "$STATUS" -eq 0 ]; then
  echo "integration checks passed"
else
  echo "integration checks failed"
fi
exit "$STATUS"
