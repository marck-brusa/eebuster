# EEBUS Testbench — agent instructions

A single Go binary that acts as an EEBUS energy manager (CEM) against a device under test:
mDNS discovery, SHIP pairing, LPC/LPP limits, MPC/MGCP reads, a web dashboard, a REST API, a
YAML scenario runner, a wire-level message trace with a conformance checker, and built-in
simulated devices. One process, no container; the only child process is the optionally
bundled EEBusTracer, spawned by serve when its binary sits next to the executable.

Read before changing behaviour:

1. `docs/02-requirements.md` — what the tool must do
2. `docs/03-control-plane.md` — REST API and dashboard behaviour
3. `docs/01-architecture.md` — process, network and identity model
4. `docs/10-eebus-go.md` — how the upstream stack is used and patched
5. `docs/20-use-case-matrix.md` — use-case coverage

This is a development and integration tool, not an EEBUS certification or conformance suite.

## Layout

| Path | Contents |
| --- | --- |
| `cmd/eebus-testbench/` | `serve`, `run`, `run-all`, `firewall` subcommands |
| `internal/eebusgo/` | stack and use cases (LPC, LPP, MPC, MGCP, EV, PV, battery) |
| `internal/httpapi/` | REST API — the tool's public contract |
| `internal/webui/` | dashboard, a single hand-written `ui.html` |
| `internal/scenario/` | YAML scenario runner, a pure REST client |
| `internal/conformance/` | wire-frame checks against SHIP TS §11 / SPINE TS §5, spec ref on every finding |
| `internal/trace/` | bounded raw-frame store behind the Message trace page and `/api/v1/trace` |
| `internal/conformance/` | wire-frame checks against SHIP TS §11 / SPINE TS §5, spec ref on every finding |
| `internal/trace/` | bounded raw-frame store behind the Message trace page and `/api/v1/trace` |
| `internal/simulator/` | simulated devices |
| `internal/announce/`, `internal/netfilter/` | which local addresses get published over mDNS |
| `internal/truststore/` | persists runtime trust decisions |

## Non-negotiables

- **Build with `-mod=vendor`.** `vendor/` carries a local patch to `eebus-go`'s
  `service/service.go` (`SetMdnsProvider`) that upstream lacks. A plain `go mod vendor` reverts
  it silently — use `scripts/vendor.sh`, which reapplies it.
- **Do not reimplement SHIP or SPINE.** Adapt the vendored upstream implementations.
- Pin upstream revisions to immutable commit SHAs in `go.mod`.
- Keep identity keys persistent. Identity initialisation must never overwrite existing keys:
  the certificate determines our SKI, and therefore every existing pairing.
- Require an explicit interface (or `*`) in mDNS mode.
- Keep all user configuration in one `eebus.yaml`. The API may only write `stacks.*.enabled`.
- The tool must start and be useful with **no configuration file at all** (real mDNS plus one
  simulated device). Do not add required configuration.
- REST route shapes are the public contract — the dashboard, the sample scripts, the scenario
  runner and users' own tooling all depend on them. Add fields; do not repurpose or remove them.
- Never publish an unroutable local address in an mDNS announcement. A peer dials published
  addresses one at a time and waits out a full TCP timeout on each dead one.

## Protocol accuracy

- Verify method names, signatures, return positions and behaviour against the vendored source
  at the pinned revision before wiring or documenting a call.
- Entity `[0]` is node management, not a use-case entity.
- Resolve typed operations by *advertised* use case, from live discovery — never infer support
  from our own local catalog. If several entities advertise the same use case, require an
  explicit `?entity=` hint.
- Treat the energy snapshot as best-effort: return partial data with explicit per-method errors.
- An empty result must encode as `[]`, not `null`. Clients iterate these.
- Conformance checks must run on the raw websocket frame, BEFORE the vendored stack's JSON
  repairs (`JsonFromEEBUSJson`) -- downstream of them the evidence is gone. The capture path
  relies on ship-go's exact `Trace("Send:"/"Recv:", ski, text)` call shape, pinned by a test.

## Trust and identity

- Pairing is mutual and both halves are required: we trust the device (config `peers:` or the
  trust API) and the device trusts us (its own pairing control). A device that is discovered but
  never connects is usually missing the second half — SHIP reports `close 4452`.
- Trust from the config `peers:` list is declarative; trust through the API is persisted in the
  data directory, and so is every peer that completes a connection (device-initiated pairings
  never pass through the API). Neither should silently override the other.
- A device may present a new certificate under an unchanged SKI after being re-imaged. Identity
  is anchored on the SKI; recover rather than rejecting the device forever.

## Genericity and hygiene

- **This repository is public and the tool is generic.** No customer, vendor or product names,
  no real device addresses, SKIs, serial numbers or credentials in tracked files — including in
  comments and test fixtures. Use documentation-range addresses (RFC 5737) and obviously
  synthetic SKIs.
- `config/eebus.yaml` holds the real device inventory and is gitignored; `eebus.example.yaml` is
  the tracked template.
- Preserve unrelated user changes. Do not commit unless asked.
- Keep documentation operational: what an engineer needs to install, run, extend or debug.

## Tests

```bash
go build -mod=vendor ./...
go vet  -mod=vendor ./cmd/... ./internal/...
go test -mod=vendor ./cmd/... ./internal/...
```

CI additionally cross-compiles for `windows/amd64` and `linux/arm64`, parses the dashboard's
inline JavaScript, and validates every scenario file — none of which the Go compiler checks.

Live behaviour against real hardware cannot be unit tested. When a change touches discovery,
pairing or limit application, say plainly whether it was verified against a device or only
built, and never describe a skipped live test as passed.
