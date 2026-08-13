# EEBUS Testbench

[![CI](https://github.com/marck-brusa/eebuster/actions/workflows/ci.yml/badge.svg)](https://github.com/marck-brusa/eebuster/actions/workflows/ci.yml)

A single-binary engineering tool for testing an EEBUS device. It acts as an energy manager
(CEM) on the network: it discovers devices over mDNS, completes SHIP pairing, applies power
limits, reads measurements, and serves a web dashboard and a REST API from one process.

Written in Go and distributed as one static executable — no runtime, no container, no
dependencies to install. It also includes simulated devices, so it is useful before any
hardware is available.

**This is a development and integration tool, not an EEBUS certification or conformance suite.**

## What it does

- **Discovery and pairing** — real mDNS (`_ship._tcp`) discovery, SHIP trust, incoming-pairing
  approval, and SPINE entity and use-case exploration.
- **Power control** — LPC and LPP limits with optional durations, failsafe values, nominal
  maximum, and heartbeat.
- **Measurements** — MPC and MGCP reads, plus EV, PV, battery and EVSE data when the device
  advertises it.
- **Web dashboard** — live view of connections, limits, measurements, use cases and logs. Shows
  the tool's own SKI permanently, and follows your system light/dark setting or an explicit choice.
- **REST API** — everything the dashboard does is an HTTP call, so it scripts cleanly. An
  OpenAPI description is served at `/api/v1/openapi.yaml`, with browsable references at
  `/docs` (Swagger UI) and `/redoc` — both embedded, so they work without internet access.
- **Scenario runner** — a library of YAML test cases, runnable from the dashboard, the CLI or
  CI, with JUnit XML output.
- **Simulated devices** — LPC-accepting, MPC-reporting devices built into the binary.

## Install

Download the archive for your platform from the
[releases page](https://github.com/marck-brusa/eebuster/releases) and unpack it:

| Archive | Platform |
| --- | --- |
| `…-windows-amd64.zip` | Windows 10/11, 64-bit |
| `…-linux-amd64.zip` | Linux, x86-64 |
| `…-linux-arm64.zip` | Linux, ARM64 |

Each archive contains the executable, a ready-to-edit `eebus.yaml`, the scenario library and
sample scripts. Verify against `SHA256SUMS.txt` if required.

Or build from source (Go 1.24+):

```bash
go build -mod=vendor -o eebus-testbench ./cmd/eebus-testbench
```

> Always build with `-mod=vendor`. The committed `vendor/` tree carries a required patch to
> `eebus-go`; use `scripts/vendor.sh` instead of `go mod vendor` when updating dependencies.

## Run

```bash
./eebus-testbench serve
```

With no configuration file, it starts in mDNS mode on all interfaces with one simulated device
and serves the dashboard at <http://127.0.0.1:8080/ui>. That first run is worth doing before
touching hardware: it verifies the binary, dashboard and API with no network variables in play.

See **[QUICKSTART.md](QUICKSTART.md)** for configuring a real device, pairing it, driving
limits, and the platform notes that matter on Windows and managed corporate laptops.

## API

```bash
curl http://127.0.0.1:8080/api/v1/peers                       # connected devices
curl http://127.0.0.1:8080/api/v1/peers/<ski>/usecases        # advertised use cases
curl -X PUT -H 'Content-Type: application/json' \
     -d '{"value_w":6000,"is_active":true,"duration":"PT2H"}' \
     http://127.0.0.1:8080/api/v1/lpc/<ski>/limit             # apply a limit
curl http://127.0.0.1:8080/api/v1/mpc/<ski>                   # measurements
```

| Area | Endpoints |
| --- | --- |
| Peers and trust | `/peers`, `/peers/visible`, `/peers/pending`, `/peers/{ski}/trust`, `/peers/{ski}/profile`, `/peers/{ski}/usecases` |
| Limits | `/lpc/{ski}/limit`, `/lpc/{ski}/failsafe`, `/lpc/{ski}/nominal-max`, `/lpc/heartbeat/start`, `/lpp/{ski}/…` |
| Measurements | `/mpc/{ski}`, `/mgcp/{ski}`, `/energy/{ski}/snapshot`, `/energy/{ski}/history` |
| Scenarios | `/scenarios`, `/scenarios/{name}/run`, `/scenarios/run-all` |
| Message trace | `/trace`, `/trace/{seq}`, `/trace/summary` — raw wire frames with conformance findings |
| Diagnostics | `/health`, `/version`, `/config`, `/events/stream`, `/diagnostics/network`, `/stacks/{id}/logs` |

Ready-made scripts for the two most common operations are in
[`examples/`](examples/) — `lpc-set-limit` and `mpc-watch`, in both bash and PowerShell, plus
Python references for pulling data in and out of the CEM.

## Deep-dive tracing with EEBusTracer

The dashboard's **Message trace** page shows live frames with conformance findings. For
offline deep dives — message-flow diagrams, latency correlation, lifecycle checklists — the
release archive bundles [EEBusTracer](https://github.com/uhl/EEBusTracer) (MIT), a standalone
protocol analyzer with its own web UI.

`serve` starts it automatically when the `eebustracer` binary sits next to the executable
(as it does in the release archive): its UI runs on <http://127.0.0.1:8090> and is linked
from the dashboard sidebar, opening in a new tab since it is a separate product. Every raw
frame is also appended to `<data-dir>/frames.log`, ready to import in the tracer's UI or via
`./eebustracer import frames.log`. Control it with `-tracer=false`, `-tracer-port`, or
`-frame-log`; setting `tracer_url` in `eebus.yaml` links a self-managed instance instead of
spawning the bundled one. `./eebustracer analyze frames.log` gives a quick use-case audit
from the terminal.

## Layout

```
cmd/eebus-testbench/   serve, scenario and firewall subcommands
internal/eebusgo/      EEBUS stack and use cases (LPC, LPP, MPC, MGCP, EV, PV, battery)
internal/httpapi/      REST API
internal/webui/        dashboard
internal/scenario/     YAML scenario runner
internal/simulator/    built-in simulated devices
internal/announce/     mDNS announcement address selection
internal/netfilter/    filters unroutable addresses out of announcements
internal/truststore/   persists runtime trust decisions
scenarios/             scenario library
examples/              sample scripts
docs/                  architecture, requirements, API and use-case reference
```

## Documentation

| Document | Contents |
| --- | --- |
| [QUICKSTART.md](QUICKSTART.md) | Install, configure, pair, drive limits, platform notes, troubleshooting |
| [RELEASE_NOTES.md](RELEASE_NOTES.md) | Release history and known issues |
| [docs/01-architecture.md](docs/01-architecture.md) | Process, network and identity model |
| [docs/02-requirements.md](docs/02-requirements.md) | What the tool is required to do |
| [docs/03-control-plane.md](docs/03-control-plane.md) | REST API and dashboard behaviour |
| [docs/10-eebus-go.md](docs/10-eebus-go.md) | How the upstream stack is used and patched |
| [docs/20-use-case-matrix.md](docs/20-use-case-matrix.md) | Supported use cases and coverage |

## Contributing

```bash
go build -mod=vendor ./...
go vet -mod=vendor ./cmd/... ./internal/...
go test -mod=vendor ./cmd/... ./internal/...
```

CI runs the same commands, cross-compiles for Windows and ARM64, and validates the dashboard
JavaScript and every scenario file. Please keep `vendor/` updates going through
`scripts/vendor.sh`, and do not commit real device addresses, SKIs or credentials.

## Upstream

Built on [enbility/eebus-go](https://github.com/enbility/eebus-go),
[ship-go](https://github.com/enbility/ship-go) and
[spine-go](https://github.com/enbility/spine-go), pinned to immutable revisions in `go.mod` and
vendored. SHIP and SPINE are not reimplemented here.
