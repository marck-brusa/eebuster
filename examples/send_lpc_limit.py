#!/usr/bin/env python3
"""Write an LPC active consumption limit and read it back -- the same call the GUI's Use
Cases tab and `testbench lpc limit` make (PUT then GET /api/v1/lpc/{ski}/limit). Standalone
httpx example; see docs/03-control-plane.md "The API surface" for the full DTO shape
(snake_case, ISO-8601 durations -- Value/IsActive/etc. are the upstream wire names, not what
this REST layer uses).

Usage:
    EEBUS_API_URL=http://127.0.0.1:9080 python3 examples/send_lpc_limit.py <peer-ski> <watts> [duration]

    python3 examples/send_lpc_limit.py <ski> 4200 PT2H   # 4200 W for 2 hours
    python3 examples/send_lpc_limit.py <ski> 0           # release-style zero limit, no expiry
"""
from __future__ import annotations

import os
import sys

import httpx

BASE_URL = os.environ.get("EEBUS_API_URL", "http://127.0.0.1:8080")


def send_limit(ski: str, watts: float, duration: str | None = None) -> dict:
    body = {"value_w": watts, "is_active": True, "is_changeable": True}
    if duration:
        body["duration"] = duration

    with httpx.Client(base_url=BASE_URL, timeout=15.0) as client:
        write_result = client.put(f"/api/v1/lpc/{ski}/limit", json=body)
        write_result.raise_for_status()

        readback = client.get(f"/api/v1/lpc/{ski}/limit")
        readback.raise_for_status()
        return {"sent": write_result.json(), "readback": readback.json()}


def main() -> None:
    if len(sys.argv) not in (3, 4):
        print(f"usage: {sys.argv[0]} <peer-ski> <watts> [duration, e.g. PT2H]", file=sys.stderr)
        raise SystemExit(1)

    ski, watts = sys.argv[1], float(sys.argv[2])
    duration = sys.argv[3] if len(sys.argv) == 4 else None

    try:
        result = send_limit(ski, watts, duration)
    except httpx.HTTPStatusError as e:
        print(f"HTTP {e.response.status_code}: {e.response.text}", file=sys.stderr)
        raise SystemExit(1)

    print("sent:    ", result["sent"]["sent"])
    print("readback:", result["readback"])


if __name__ == "__main__":
    main()
