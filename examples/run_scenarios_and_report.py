#!/usr/bin/env python3
"""Trigger the scenario suite over the REST API and print a pass/fail summary -- the same
call the Scenarios tab's "Run all" button and `testbench run-all` make (POST
/api/v1/scenarios/run-all). Standalone httpx example: no facade imports, so this is what
driving the test suite from your own CI script (rather than shelling out to `testbench`)
looks like. Exits non-zero on any failure, same convention as the CLI.

Usage:
    EEBUS_API_URL=http://127.0.0.1:9080 python3 examples/run_scenarios_and_report.py
"""
from __future__ import annotations

import os
import sys

import httpx

BASE_URL = os.environ.get("EEBUS_API_URL", "http://127.0.0.1:8080")


def main() -> None:
    with httpx.Client(base_url=BASE_URL, timeout=120.0) as client:
        r = client.post("/api/v1/scenarios/run-all")
        r.raise_for_status()
        suite = r.json()

    for result in suite["scenarios"]:
        marker = "PASS" if result["status"] == "passed" else "FAIL"
        print(f"[{marker}] {result['name']} ({result['duration_s']}s)")
        if result["status"] == "failed":
            for step in result["steps"]:
                if step["status"] == "failed":
                    print(f"         step {step['step']!r}: {step['detail']}")

    print(f"\n{suite['passed']} passed, {suite['failed']} failed")
    if suite["status"] == "failed":
        raise SystemExit(1)


if __name__ == "__main__":
    main()
