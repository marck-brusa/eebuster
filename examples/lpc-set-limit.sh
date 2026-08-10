#!/usr/bin/env bash
# Apply an LPC consumption limit to a device, then read it back.
#
#   ./lpc-set-limit.sh <ski> <watts> [duration] [base-url]
#   ./lpc-set-limit.sh 0123456789abcdef0123456789abcdef01234567 4200
#   ./lpc-set-limit.sh 0123456789abcdef0123456789abcdef01234567 4200 PT2H http://192.168.1.50:8080
#
# duration is ISO-8601 ("PT30S", "PT2H"). Omit it for a limit with no expiry.
# Pass watts as 0 with duration "release" to clear the limit instead:
#   ./lpc-set-limit.sh <ski> 0 release
set -euo pipefail

SKI="${1:?usage: lpc-set-limit.sh <ski> <watts> [duration|release] [base-url]}"
WATTS="${2:?missing watts}"
DURATION="${3:-}"
BASE="${4:-http://127.0.0.1:8080}"

if [ "$DURATION" = "release" ]; then
  # is_active:false is how LPC says "no limit in force" -- it is not the same as 0 W.
  BODY="{\"value_w\":${WATTS},\"is_active\":false}"
elif [ -n "$DURATION" ]; then
  BODY="{\"value_w\":${WATTS},\"is_active\":true,\"duration\":\"${DURATION}\"}"
else
  BODY="{\"value_w\":${WATTS},\"is_active\":true}"
fi

echo "PUT ${BASE}/api/v1/lpc/${SKI}/limit"
echo "  ${BODY}"
curl -fsS -X PUT -H 'Content-Type: application/json' -d "$BODY" \
  "${BASE}/api/v1/lpc/${SKI}/limit"
echo

# A device may accept a write and still report the old value for a moment; give it a beat
# before reading back, or you will misread its own reporting lag as a failed write.
sleep 3
echo "readback:"
curl -fsS "${BASE}/api/v1/lpc/${SKI}/limit"
echo
