# Examples

Small scripts that drive the REST API directly. They are deliberately dependency-free so they
work on a locked-down machine with nothing installed, and they ship inside the release archive.
Use them as-is or as a starting point for your own tooling.

Both pairs do the same thing; pick the one for your shell.

| Script | Purpose |
| --- | --- |
| `lpc-set-limit.sh` / `lpc-set-limit.ps1` | Apply an LPC consumption limit and read it back |
| `mpc-watch.sh` / `mpc-watch.ps1` | Poll MPC measurements, one line per sample |

```bash
# Linux / macOS -- needs only curl (python3 optional, for tidier output)
./lpc-set-limit.sh <ski> 4200            # 4.2 kW, no expiry
./lpc-set-limit.sh <ski> 4200 PT2H       # 4.2 kW for two hours
./lpc-set-limit.sh <ski> 0 release       # clear the limit
./mpc-watch.sh <ski> 5                   # sample every 5 seconds
```

```powershell
# Windows -- needs only PowerShell 5.1 or later
.\lpc-set-limit.ps1 -Ski <ski> -Watts 4200 -Duration PT2H
.\lpc-set-limit.ps1 -Ski <ski> -Watts 0 -Release
.\mpc-watch.ps1 -Ski <ski> -IntervalSeconds 5
```

All scripts take an optional base URL as the last argument (`-BaseUrl` in PowerShell) and
default to `http://127.0.0.1:8080`. Change it if you moved `api.port` or are driving the tool
from another machine.

Get a peer's SKI from `GET /api/v1/peers`. See [QUICKSTART.md](../QUICKSTART.md) for getting a
device connected in the first place, and `GET /api/v1/openapi.yaml` for the full API — or open
`/docs` (Swagger UI) in a browser; both are embedded and work offline.

## Python examples

`explore_peer.py`, `send_lpc_limit.py` and `run_scenarios_and_report.py` predate the Go
rewrite. They still work against the same REST API but need `httpx` installed, so they are not
included in the release archive.
