#!/usr/bin/env python3
"""Start a test run over the REST API, wait for it, print the result and save the reports --
the same calls the Test runner page makes (POST /api/v1/runs, then GET /api/v1/runs/{id}).
Standalone httpx example: what driving the test suite from your own CI script (rather than
shelling out to `eebus-testbench run-all`) looks like. Exits non-zero unless the run passed.

Usage:
    EEBUS_API_URL=http://127.0.0.1:9080 python3 examples/run_scenarios_and_report.py [LPC MPC ...]
"""
from __future__ import annotations

import os
import sys
import time

import httpx

BASE_URL = os.environ.get("EEBUS_API_URL", "http://127.0.0.1:8080")


def main() -> None:
    use_cases = sys.argv[1:]
    with httpx.Client(base_url=BASE_URL, timeout=60.0) as client:
        r = client.post("/api/v1/runs", json={"selection": {"use_cases": use_cases}, "tester": os.environ.get("USER", "")})
        r.raise_for_status()
        run_id = r.json()["run_id"]
        print(f"run {run_id} started")

        run = client.get(f"/api/v1/runs/{run_id}").json()
        while run["status"] == "running":
            time.sleep(2)
            run = client.get(f"/api/v1/runs/{run_id}").json()

        for result in run["test_cases"]:
            marker = {"passed": "PASS", "failed": "FAIL"}.get(result["status"], "SKIP")
            reason = f" -- {result['reason']}" if result.get("reason") else ""
            print(f"[{marker}] {result['id']} ({result['duration_s']}s){reason}")
            if result["status"] == "failed":
                for step in result["steps"]:
                    if step["status"] == "failed":
                        print(f"         step {step['step']!r}: {step.get('detail', '')}")

        for name in ("report.html", "report.xlsx", "junit.xml"):
            data = client.get(f"/api/v1/runs/{run_id}/{name}").content
            path = f"{run_id}-{name}"
            with open(path, "wb") as f:
                f.write(data)
            print(f"saved {path}")

    print(f"\n{run['summary']['headline']}")
    if run["status"] != "passed":
        raise SystemExit(1)


if __name__ == "__main__":
    main()
