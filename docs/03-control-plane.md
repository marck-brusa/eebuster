# Dashboard and API

## Web interfaces

The dashboard (`/ui`) and the REST API share one port, `api.port` in `eebus.yaml`
(default 8080) -- there is no separate host-port mapping to know about. The bundled
EEBusTracer, when running, serves its own UI on its own port (default 8090) and is linked
from the sidebar.

The dashboard is a single static HTML application served by the binary. It has no external
frontend runtime or build step.

The API reference is served by the same binary, fully offline: `/docs` (Swagger UI, with
try-it-out) and `/redoc` (Redoc, reading-oriented) both render the spec at `/openapi.yaml`
(also reachable as `/api/v1/openapi.yaml`). The viewer assets are embedded — see
`internal/openapi/assets/README.md` for provenance.

## Dashboard workspaces

### Dashboard

Shows the selected peer's:

- total consumption;
- connected and charging EV counts;
- photovoltaic production;
- active consumption limit;
- grid, EV, PV, and battery power;
- live device/use-case summary.

The time-series chart stores one sample per snapshot request, retains up to 12 hours in
memory, and supports hover and click inspection.

The LPC control sends immediately. Automatic entity selection uses the entity that advertises
LPC; an engineer can select an explicit SPINE entity when needed.

### Devices & network

Merges:

- configured peers;
- mDNS-visible peers;
- connected peers;
- pending pairing requests.

Actions include Trust, Untrust, Approve, Deny, and an independent mDNS scan.

The device detail shows:

- SKI and SPINE device address;
- device and entity types;
- advertised use cases by entity;
- actor, version, scenario support, and availability;
- typed operations currently exposed by the testbench;
- raw discovery data and partial-read errors.

“Not advertised” means the live peer did not report the use case. It is not a compatibility
claim about the product family.

### Use cases

Provides:

- a live advertised-use-case browser;
- LPC and LPP reads and templates;
- failsafe, nominal maximum, and heartbeat operations;
- MPC and MGCP reads;
- combined EV/PV/battery snapshot reads;

### Message trace

Shows every SHIP frame as it appeared on the wire, in both directions, for every stack in the
process (including simulated devices). Capture happens at the websocket layer *before* the
vendored stack's JSON repairs, so structurally broken messages are shown as the device
actually sent them. Each frame is checked against the EEBUS JSON encoding rules and the SPINE
datagram rules (see `internal/conformance`); findings carry a reference into the standard. A
conformance summary aggregates violations by rule; clicking a frame shows its findings and
the raw wire payload.

### Test runner

Loads metadata from `scenarios/*.yaml`. Tests can be run individually, as a read-only set, or
as the complete suite. Each result includes step status, duration, and failure detail.

### Diagnostics

Shows:

- runtime stack status and controls;
- announced/rejected addresses, interfaces, and firewall guidance (reachability);
- process logs;
- recent and streaming lifecycle/use-case/SPINE events;

## API summary

All endpoints are under `/api/v1`.

### Runtime and configuration

| Method | Path | Purpose |
|---|---|---|
| GET | `/health` | Liveness |
| GET | `/version` | Tool version |
| GET | `/identity` | Local SKI for running adapters |
| GET | `/stacks` | Runtime state and capabilities |
| GET | `/stacks/{id}` | One stack's state |
| POST | `/stacks/{id}/start` | Start a stack |
| POST | `/stacks/{id}/stop` | Stop a stack |
| GET | `/stacks/{id}/logs` | Tail process log |
| GET | `/config` | Redacted configuration |
| POST | `/config/validate` | Validate the file without applying |
| POST | `/config/reload` | Validate and reload the file |
| PUT | `/config/active-stack` | Select one counterparty |
| GET | `/diagnostics/network` | Announced/rejected addresses, interfaces, mDNS health |

### Discovery and trust

| Method | Path | Purpose |
|---|---|---|
| GET | `/peers` | Connected peers |
| GET | `/peers/visible` | Visible but unconnected peers |
| GET | `/peers/pending` | Pending pairing decisions |
| POST | `/peers/{ski}/trust` | Trust and start connection |
| DELETE | `/peers/{ski}/trust` | Disconnect/untrust |
| POST | `/peers/{ski}/deny` | Deny pending pairing |
| GET | `/peers/{ski}/usecases` | Raw advertised use cases |
| GET | `/peers/{ski}/profile` | Enriched and raw device model |
| POST | `/discover` | Independent mDNS scan |

### Energy and use cases

| Method | Path | Purpose |
|---|---|---|
| GET/PUT | `/lpc/{ski}/limit` | Consumption limit |
| GET/PUT | `/lpc/{ski}/failsafe` | Failsafe value and duration |
| GET | `/lpc/{ski}/nominal-max` | Declared maximum consumption |
| POST | `/lpc/heartbeat/start` | Start heartbeat |
| POST | `/lpc/heartbeat/stop` | Stop heartbeat |
| GET | `/lpc/{ski}/heartbeat` | Heartbeat state |
| GET/PUT | `/lpp/{ski}/limit` | Production limit |
| GET/PUT | `/lpp/{ski}/failsafe` | Production failsafe value and duration |
| GET | `/lpp/{ski}/nominal-max` | Declared maximum production |
| GET | `/mpc/{ski}` | Consumption measurements |
| GET | `/mgcp/{ski}` | Grid-connection measurements |
| GET | `/energy/{ski}/snapshot` | Best-effort energy intelligence |
| GET | `/energy/{ski}/history` | Session history |
| DELETE | `/energy/{ski}/history` | Clear session history |

Use `?entity=1` or `?entity=1,2` on typed per-entity operations when explicit selection is
needed.

### Scenarios and events

| Method | Path | Purpose |
|---|---|---|
| GET | `/scenarios` | Scenario IDs |
| GET | `/scenarios/catalog` | Scenario metadata |
| POST | `/scenarios/{name}/run` | Run one scenario |
| POST | `/scenarios/run-all` | Run the complete suite |
| GET | `/events/recent` | Recent event buffer |
| GET | `/events/stream` | Server-Sent Events |
| DELETE | `/events` | Clear recent events |
| GET | `/templates` | LPC/LPP request templates |
| GET | `/trace` | Captured wire frames with conformance findings (cursor polling via `?after=`) |
| GET | `/trace/{seq}` | One frame in full, including the raw payload |
| GET | `/trace/summary` | Violations aggregated by rule, with standard references |
| DELETE | `/trace` | Clear the trace and conformance sessions |

## Examples

Read a device profile:

```bash
curl http://127.0.0.1:8080/api/v1/peers/$SKI/profile
```

Apply a 4.2 kW limit for 15 minutes:

```bash
curl -X PUT "http://127.0.0.1:8080/api/v1/lpc/$SKI/limit" \
  -H "content-type: application/json" \
  -d '{"value_w":4200,"is_active":true,"is_changeable":true,"duration":"PT15M"}'
```

Read the combined snapshot:

```bash
curl http://127.0.0.1:8080/api/v1/energy/$SKI/snapshot
```

Run all scenarios:

```bash
curl -X POST http://127.0.0.1:8080/api/v1/scenarios/run-all
```

## Error behavior

The API reports actionable failures:

- `404` — peer/entity/resource not found;
- `409` — ambiguous entity, port conflict, or counterparty conflict;
- `501` — active stack lacks the capability;
- `502` — upstream RPC error;
- `503` — no live adapter or required binary;
- `504` — discovery timeout.

The dashboard surfaces these responses in a toast or the relevant output panel.
