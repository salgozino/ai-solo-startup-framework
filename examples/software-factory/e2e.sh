#!/usr/bin/env bash
# End-to-end run of the software factory against real provider CLIs.
#
# Materializes factory.yaml, asks the CEO (through the monitoring UI's HTTP API, the same
# path a human uses) for a small app, waits for the pipeline to finish, and asserts that
# every role took part, every artifact exists, and the delivered tests pass.
#
# Requires: go, pi (with the models in factory.yaml reachable), node, python3, curl, and
# TELEGRAM_BOT_TOKEN / TELEGRAM_OWNER_ID in the environment.
#
# Usage: ./e2e.sh ["<app request>"]
# Env:   E2E_TIMEOUT (seconds, default 3600), COMPANY_UI_ADDR (default 127.0.0.1:18080)
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
repo="$(cd "$here/../.." && pwd)"
cd "$here"

request="${1:-Build a tiny tip calculator web app: the user enters the bill amount, picks a tip percentage (10, 15, 20 or a custom value) and the number of people, and sees the tip, the total and the amount per person.}"
timeout_s="${E2E_TIMEOUT:-3600}"
export COMPANY_UI_ADDR="${COMPANY_UI_ADDR:-127.0.0.1:18080}"
ui="http://$COMPANY_UI_ADDR"

: "${TELEGRAM_BOT_TOKEN:?TELEGRAM_BOT_TOKEN must be set}"
: "${TELEGRAM_OWNER_ID:?TELEGRAM_OWNER_ID must be set}"

run_dir="$here/.runs/$(date +%Y%m%d-%H%M%S)"
mkdir -p "$run_dir"
# Fresh task stores per run: the supervisor stores live under TMPDIR. Keep it short:
# every agent inherits it, and Chrome (QA's browser) puts its singleton socket there;
# a socket path past the 108-byte Unix limit makes Chrome close on start
# ("Target closed").
export TMPDIR="$(mktemp -d /tmp/asf-factory.XXXXXX)"
export FACTORY_A2A_TOKEN="$(head -c 24 /dev/urandom | base64 | tr -d '/+=')"
log="$run_dir/company.log"

echo "e2e: building company binary"
(cd "$repo" && go build -o "$run_dir/company" ./cmd/company)

# Agents inherit the company's working directory, so each run gets its own workspace:
# projects land in $run_dir/workspace/<app-slug>/.
echo "e2e: materializing factory.yaml (log: $log)"
(cd "$run_dir" && exec "$run_dir/company" materialize "$here/factory.yaml") >"$log" 2>&1 &
pid=$!
trap 'kill "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true; rm -rf "$TMPDIR"' EXIT

for _ in $(seq 1 120); do
  curl -fsS "$ui/api/tasks" >/dev/null 2>&1 && break
  kill -0 "$pid" 2>/dev/null || { echo "e2e: company exited during startup"; cat "$log"; exit 1; }
  sleep 1
done
curl -fsS "$ui/api/tasks" >/dev/null || { echo "e2e: UI never came up"; cat "$log"; exit 1; }
grep '^wire: agent' "$log"

echo "e2e: sending request to the CEO"
python3 -I -c 'import json,sys; print(json.dumps({"message": sys.argv[1]}))' "$request" |
  curl -fsS -X POST "$ui/api/send" -H 'Content-Type: application/json' --data-binary @- >/dev/null

deadline=$(( $(date +%s) + timeout_s ))
state=""
while :; do
  curl -fsS "$ui/api/tasks" >"$run_dir/tasks.json"
  state="$(python3 -I "$here/e2e_check.py" status "$run_dir/tasks.json")"
  echo "e2e: $(date +%T) $state"
  case "$state" in
    done:*) break ;;
    escalated:*) echo "e2e: unexpected human escalation — every action in this company is safe"; break ;;
  esac
  kill -0 "$pid" 2>/dev/null || { echo "e2e: company died"; break; }
  [ "$(date +%s)" -lt "$deadline" ] || { echo "e2e: timed out after ${timeout_s}s"; break; }
  sleep 15
done

curl -fsS "$ui/api/tasks" >"$run_dir/tasks.json" || true
python3 -I "$here/e2e_check.py" verify "$run_dir/tasks.json" "$run_dir/workspace" "$log" "$run_dir/chrome-devtools.log"
