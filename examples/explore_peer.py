#!/usr/bin/env python3
"""Query what a connected peer itself advertises -- SPINE entities and use-case support --
the same two REST calls behind the GUI's Network tab Explore panel and `testbench explore`.
Written as a standalone script (plain httpx, no facade imports) so it also serves as a
minimal example of driving the REST API directly, without the CLI or GUI in the loop.

Usage:
    EEBUS_API_URL=http://127.0.0.1:9080 python3 examples/explore_peer.py <peer-ski>
"""
from __future__ import annotations

import os
import sys

import httpx

BASE_URL = os.environ.get("EEBUS_API_URL", "http://127.0.0.1:8080")


def explore(ski: str) -> dict:
    with httpx.Client(base_url=BASE_URL, timeout=15.0) as client:
        peers = client.get("/api/v1/peers")
        peers.raise_for_status()
        peer = next((p for p in peers.json() if p.get("ski") == ski), None)
        entities = (peer or {}).get("entities", [])

        use_cases = client.get(f"/api/v1/peers/{ski}/usecases")
        use_cases.raise_for_status()

        return {"connected": peer is not None, "entities": entities, "use_cases": use_cases.json()}


def main() -> None:
    if len(sys.argv) != 2:
        print(f"usage: {sys.argv[0]} <peer-ski>", file=sys.stderr)
        raise SystemExit(1)
    ski = sys.argv[1]

    try:
        result = explore(ski)
    except httpx.HTTPStatusError as e:
        # e.response.text is the FastAPI {"detail": "..."} body -- e.g. "no active stack
        # with a live control plane is running" (503) -- far more useful than the bare
        # status code raise_for_status() puts in the exception message.
        print(f"HTTP {e.response.status_code}: {e.response.text}", file=sys.stderr)
        raise SystemExit(1)

    if not result["connected"]:
        print(f"warning: {ski!r} is not in GET /peers (not connected?) -- use cases below may be empty", file=sys.stderr)

    print(f"Entities ({len(result['entities'])}):")
    for e in result["entities"]:
        print(f"  device {e.get('device')} entity {e.get('entity')}")

    names = {
        support.get("useCaseName"): support.get("useCaseAvailable", True)
        for entry in result["use_cases"]
        for support in entry.get("useCaseSupport", [])
        if support.get("useCaseName")
    }
    print(f"\nUse cases advertised ({len(names)}):")
    for name, available in sorted(names.items()):
        print(f"  {name}{'' if available else ' (unavailable)'}")


if __name__ == "__main__":
    main()
