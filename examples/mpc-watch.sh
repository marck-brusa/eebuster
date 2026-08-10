#!/usr/bin/env bash
# Poll a device's MPC measurements and print one line per sample.
#
#   ./mpc-watch.sh <ski> [interval-seconds] [base-url]
#   ./mpc-watch.sh 0123456789abcdef0123456789abcdef01234567 5
#
# Ctrl-C to stop. All-null values mean the device is not currently reporting measurements --
# usually because no charging session is active. That is the device's answer, not an error.
set -euo pipefail

SKI="${1:?usage: mpc-watch.sh <ski> [interval-seconds] [base-url]}"
INTERVAL="${2:-5}"
BASE="${3:-http://127.0.0.1:8080}"

printf '%-10s %-11s %-9s %s\n' "time" "power_w" "energy_Wh" "current_per_phase_mA"
while true; do
  json="$(curl -fsS "${BASE}/api/v1/mpc/${SKI}" 2>/dev/null || echo '{}')"
  # python3 if available for clean field extraction; otherwise dump the raw JSON.
  if command -v python3 >/dev/null 2>&1; then
    printf '%-10s %s\n' "$(date -u +%H:%M:%S)" "$(printf '%s' "$json" | python3 -c '
import json,sys
try: d=json.load(sys.stdin)
except Exception: print("(unparseable)"); raise SystemExit
p=d.get("power_w"); e=d.get("energy_consumed_wh"); c=d.get("current_per_phase_a")
print(f"{str(p):<11} {str(e):<9} {c}")
')"
  else
    printf '%-10s %s\n' "$(date -u +%H:%M:%S)" "$json"
  fi
  sleep "$INTERVAL"
done
