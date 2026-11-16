#!/bin/bash
# End-to-end CLI checks against the PTY protocol simulator.
set -u
VK=${VK:-$(cd "$(dirname "$0")/../.." && pwd)/bin/vaultkey}
WORK=${VK_WORK:-${TMPDIR:-/tmp}/vaultkey-it}
PORT=${PORT:-"$WORK/tty"}
STATE="$WORK/state.json"
LOG="$WORK/e2e.log"
PASS=0
FAIL=0

: > "$LOG"

set_state() {
  printf '%s\n' "$1" > "$STATE"
}

run() {
  # run <name> <expected-exit> <stdin-file-or-empty> <args...>
  local name=$1 expected=$2 input=$3
  shift 3
  local output status
  if [ -n "$input" ]; then
    output=$("$VK" --port "$PORT" "$@" < "$input" 2>&1)
  else
    output=$("$VK" --port "$PORT" "$@" </dev/null 2>&1)
  fi
  status=$?
  {
    echo "===== $name (exit $status, expected $expected)"
    echo "$output"
  } >> "$LOG"
  if [ "$status" -eq "$expected" ]; then
    PASS=$((PASS+1))
    echo "PASS  $name (exit $status)"
  else
    FAIL=$((FAIL+1))
    echo "FAIL  $name (exit $status, expected $expected)"
    echo "$output" | sed 's/^/      /'
  fi
  LAST_OUTPUT=$output
}

json_check() {
  # json_check <name> <python-expression over d>
  local name=$1 expression=$2
  if printf '%s' "$LAST_OUTPUT" | python3 -c "import json,sys; d=json.load(sys.stdin); assert $expression" 2>/dev/null; then
    PASS=$((PASS+1)); echo "PASS  $name"
  else
    FAIL=$((FAIL+1)); echo "FAIL  $name"
    echo "$LAST_OUTPUT" | sed 's/^/      /'
  fi
}

set_state '{"locked": false, "authorized": true, "drop": ""}'

run "info" 0 "" info
run "info --json" 0 "" info --json
json_check "info json identity" 'd["device"]["product"]=="VaultKey" and d["device"]["protocol"]==1 and d["port"].endswith("tty")'

run "status" 0 "" status
run "status --json" 0 "" status --json
json_check "status json unlocked+authorized" 'd["status"]["locked"] is False and d["status"]["provisioning"] is True and d["status"]["credentials"]==2'

run "doctor" 0 "" doctor

run "time get" 0 "" time get
run "time get --json" 0 "" time get --json
json_check "time json" 'd["utc"] is True and d["unix"] > 0'

NOW=$(date +%s)
run "time set --unix" 0 "" time set --unix "$NOW" --json
json_check "time set json" 'd["set"]["unix"] == '"$NOW"' and d["verified"]["unix"] == '"$NOW"
run "time set --rfc3339" 0 "" time set --rfc3339 "2031-02-03T04:05:06Z" --json
json_check "time set rfc3339" 'd["set"]["unix"] == 1927857906 and d["set"]["utc"] is True'

run "list" 0 "" list
run "list --json" 0 "" list --json
json_check "list json" 'd["credential_total"]==2 and d["totp_total"]==1 and len(d["credentials"])==2'
run "list --query github --json" 0 "" list --query github --json
json_check "list query" 'd["credential_total"]==1 and d["credentials"][0]["service"]=="GitHub"'

printf 'integration-password\n' > "$WORK/pw.txt"
printf 'caf\xc3\xa9-password\n' > "$WORK/pw2.txt"
run "add-credential --password-stdin" 0 "$WORK/pw.txt" \
  add-credential --service "Integration" --username "tester" --url "https://integration.test" --notes "e2e" --password-stdin --json
json_check "add credential id" 'isinstance(d["entry_id"], int) and d["entry_id"] > 0'
CRED_ID=$(printf '%s' "$LAST_OUTPUT" | python3 -c 'import json,sys; print(json.load(sys.stdin)["entry_id"])')
run "add-credential non-ASCII warning" 0 "$WORK/pw2.txt" \
  add-credential --service "Unicode" --username "tester" --password-stdin
if printf '%s' "$LAST_OUTPUT" | grep -q "non-ASCII"; then
  PASS=$((PASS+1)); echo "PASS  non-ASCII warning shown"
else
  FAIL=$((FAIL+1)); echo "FAIL  non-ASCII warning shown"
fi

printf 'replacement-password\n' > "$WORK/pw.txt"
run "update-credential" 0 "$WORK/pw.txt" \
  update-credential "$CRED_ID" --service "Integration Updated" --username "tester2" --password-stdin --json
json_check "update credential id" 'd["entry_id"] == '"$CRED_ID"''

run "list --all --json" 0 "" list --all --json
json_check "list all includes updates" 'd["credential_total"]==4 and any(c["service"]=="Integration Updated" for c in d["credentials"])'

printf 'otpauth://totp/Integration:tester?secret=JBSWY3DPEHPK3PXP&digits=6&period=30\n' > "$WORK/uri.txt"
run "add-totp --uri-stdin" 0 "$WORK/uri.txt" add-totp --uri-stdin --json
json_check "add totp id" 'isinstance(d["entry_id"], int) and d["entry_id"] > 0'
TOTP_ID=$(printf '%s' "$LAST_OUTPUT" | python3 -c 'import json,sys; print(json.load(sys.stdin)["entry_id"])')
printf 'https://not-a-totp/\n' > "$WORK/bad-uri.txt"
run "add-totp invalid URI" 2 "$WORK/bad-uri.txt" add-totp --uri-stdin

run "delete totp" 0 "" delete --type totp --id "$TOTP_ID" --yes --json
json_check "delete totp json" 'd["deleted"] is True and d["type"]=="totp"'
run "delete credential" 0 "" delete --type credential --id "$CRED_ID" --yes --json
json_check "delete credential json" 'd["deleted"] is True and d["id"]=='"$CRED_ID"
run "delete unknown credential" 8 "" delete --type credential --id 4294967295 --yes

run "lock --json" 0 "" lock --json
json_check "lock json" 'd["locked"] is True'
run "status after lock" 0 "" status
if printf '%s' "$LAST_OUTPUT" | grep -q "Locked:        yes"; then
  PASS=$((PASS+1)); echo "PASS  status reports locked"
else
  FAIL=$((FAIL+1)); echo "FAIL  status reports locked"
fi
run "list while locked" 4 "" list

set_state '{"locked": false, "authorized": false, "drop": ""}'
run "status unauthorized" 0 "" status
if printf '%s' "$LAST_OUTPUT" | grep -q "CDC auth:      no"; then
  PASS=$((PASS+1)); echo "PASS  status reports unauthorized"
else
  FAIL=$((FAIL+1)); echo "FAIL  status reports unauthorized"
fi
run "add credential unauthorized" 5 "$WORK/pw.txt" \
  add-credential --service "Blocked" --password-stdin
run "list unauthorized" 5 "" list
run "lock unauthorized (allowed while locked-only command)" 0 "" lock

set_state '{"locked": false, "authorized": true, "drop": "LOCK_DEVICE"}'
run "ambiguous lock timeout" 7 "" lock --timeout 1s
if printf '%s' "$LAST_OUTPUT" | grep -q "may have been applied"; then
  PASS=$((PASS+1)); echo "PASS  ambiguous timeout warning"
else
  FAIL=$((FAIL+1)); echo "FAIL  ambiguous timeout warning"
fi
set_state '{"locked": false, "authorized": true, "drop": ""}'

set_state '{"locked": false, "authorized": true, "drop": ""}'
run "unknown command" 2 "" frobnicate
run "no command" 2 "" 
run "bad port" 3 "" --port "$WORK/missing" info
printf 'x\n' > "$WORK/overlong.txt"
run "oversized service" 2 "$WORK/pw.txt" add-credential --service "$(python3 -c 'print("s"*65)')" --password-stdin
run "missing password stdin" 2 /dev/null add-credential --service "NoPassword" --password-stdin
run "delete invalid type" 2 "" delete --type nonsense --id 1 --yes
run "time set out of range" 2 "" time set --unix 100 --json

# watch runs until interrupted; SIGINT should map to exit 130.
"$VK" --port "$PORT" --timeout 2s watch --interval 250ms --json </dev/null > "$WORK/watch.out" 2>"$WORK/watch.err" &
WATCH_PID=$!
sleep 1
kill -INT "$WATCH_PID"
wait "$WATCH_PID"
WATCH_STATUS=$?
{
  echo "===== watch (exit $WATCH_STATUS, expected 130)"
  cat "$WORK/watch.out" "$WORK/watch.err"
} >> "$LOG"
if [ "$WATCH_STATUS" -eq 130 ] && [ "$(wc -l < "$WORK/watch.out")" -ge 1 ]; then
  PASS=$((PASS+1)); echo "PASS  watch (SIGINT -> 130, JSON lines emitted)"
else
  FAIL=$((FAIL+1)); echo "FAIL  watch (exit $WATCH_STATUS)"
  cat "$WORK/watch.out" "$WORK/watch.err" | sed 's/^/      /'
fi

echo
echo "RESULT: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ]
