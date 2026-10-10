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
memory, and supports hover and click inspection. A second panel below it plots per-phase
current, per-phase voltage, or state of charge — one unit at a time, since they share no
axis; hovering either panel inspects the same instant on both. Units the device does not
report are disabled, and the panel is hidden entirely when it reports none of them.

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

Lists the test cases of `scenarios/*.yaml` grouped by use case, in run order, each with what it
checks (`covers`), its goal, its specification reference and its risk. A group header shows
what the selected device advertises for that use case.

- **Select** single test cases or whole use cases with the check boxes, filter by use case,
  and search. The selection is remembered in the browser.
- **Run selected**, **Run read-only**, **Run complete suite**, or **Run use case** on a group
  header. Every run is a recorded test run (see `docs/21-test-report.md`): it captures the
  device, vehicles and conditions, runs the test cases in a fixed order, and runs the cleanup
  test cases of the touched use cases at the end. **Include long-running test cases** adds the
  ones that wait on specification timers. Tester and notes go into the report.
- The **run panel** shows progress, the headline ("20 test cases: 19 passed, 1 failed, 0
  skipped."), **Cancel** (stops after the current step; cleanup still runs), **Open report**
  and the downloads: HTML, JSON, JUnit, CSV of test cases and of steps, Excel.
- **Reports** lists the run files in the reports folder as they are: copy one in and it shows up,
  delete one and it is gone. The panel shows where the folder is -- as a `\\wsl.localhost\...`
  path when the testbench runs in WSL -- and **Open folder** opens it in the file manager.
  Per report: **Open report** in the browser, **Show in folder** with its file selected, and
  **Load results** onto the test rows. Opening the folder works from a browser on the machine
  the testbench runs on, because the window opens there.
- Each test case row shows its result: verdict, skip reason, every step and, for a failed
  step, the compared values.

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
| GET | `/version` | Tool version, stack module versions, Go version, host and platform |
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
| GET | `/peers/{ski}/manufacturer` | Manufacturer data of every entity with a device classification, read from the device |
| POST | `/discover` | Independent mDNS scan |

### Energy and use cases

| Method | Path | Purpose |
|---|---|---|
| GET/PUT | `/lpc/{ski}/limit` | Consumption limit |
| GET/PUT | `/lpc/{ski}/failsafe` | Failsafe value and duration; `?unchecked=true` sends a duration outside 2–24 h as is |
| GET | `/lpc/{ski}/nominal-max` | Declared maximum consumption |
| POST | `/lpc/heartbeat/start` | Start heartbeat |
| POST | `/lpc/heartbeat/stop` | Stop heartbeat |
| GET | `/lpc/{ski}/heartbeat` | Heartbeat state and the device's announced `heartbeat_timeout_s` |
| GET/PUT | `/lpp/{ski}/limit` | Production limit |
| GET/PUT | `/lpp/{ski}/failsafe` | Production failsafe value and duration |
| GET | `/lpp/{ski}/nominal-max` | Declared maximum production |
| GET | `/mpc/{ski}` | Consumption measurements |
| GET | `/opev/{ski}` | Per-phase current obligations and constraints |
| PUT | `/opev/{ski}/limits` | Write per-phase current obligations (overload protection) |
| GET | `/oscev/{ski}` | Per-phase current recommendations and constraints |
| PUT | `/oscev/{ski}/limits` | Write per-phase current recommendations |
| GET | `/ohpcf/{ski}` | Heat pump compressor flexibility offer |
| GET | `/evsecc/{ski}` | Station identity: manufacturer data and operating state |
| GET | `/evsoc/{ski}` | Vehicle state of charge, capacity, state of health and range, each with its measurement description |
| POST | `/opev/heartbeat/start` `/stop` | Energy Guard heartbeat (OPEV scenario 2) |
| PUT | `/opev/operating-state` | Announce/clear the Energy Guard error state (OPEV scenario 3) |
| POST | `/oscev/heartbeat/start` `/stop` | CEM heartbeat for OSCEV |
| PUT | `/oscev/operating-state` | Announce/clear the CEM error state for OSCEV |
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
| POST | `/scenarios/{name}/run` | Run one scenario as a recorded run; answers with its result and `run_id` |
| POST | `/scenarios/run-all` | Run the library (without long-running test cases) as a recorded run; answers with the suite result and `run_id` |
| GET | `/events/recent` | Recent event buffer |
| GET | `/events/stream` | Server-Sent Events |
| DELETE | `/events` | Clear recent events |
| GET | `/templates` | LPC/LPP request templates |
| GET | `/trace` | Captured wire frames with conformance findings (cursor polling via `?after=`; `?raw=1` keeps the payloads) |
| GET | `/trace/{seq}` | One frame in full, including the raw payload |
| GET | `/trace/summary` | Violations aggregated by rule, with standard references |
| DELETE | `/trace` | Clear the trace and conformance sessions |

### Test runs

| Method | Path | Purpose |
|---|---|---|
| POST | `/runs` | Start a run. Body: `peer`, `selection` (`read_only`, `use_cases`, `risks`, `ids`), `include_long_running`, `include_frames`, `tester`, `notes`. `202` with `run_id`; `409 run_in_progress`; `422 nothing_selected` |
| GET | `/runs` | The run files in the reports folder, newest first, the active run id and the folder |
| GET | `/runs/{id}` | The run record; partial while it runs |
| POST | `/runs/{id}/cancel` | Stop after the current step; cleanup still runs |
| GET | `/runs/{id}/report.html` | Self-contained HTML report; `?download=1` offers it as a file |
| GET | `/runs/{id}/report.json` | The run record as a file |
| GET | `/runs/{id}/junit.xml` | JUnit XML with the run's identification as properties |
| GET | `/runs/{id}/report.csv` | CSV; `?table=cases` (default) or `?table=steps` |
| GET | `/runs/{id}/report.xlsx` | Excel workbook: Summary, Device, Test cases, Steps, Conformance |
| POST | `/reports/open` | Open the reports folder in the file manager of the machine the testbench runs on; `?run=<id>` selects that run's report. Loopback clients only: `403 not_local` otherwise |

Runs are written as `<data-dir>/reports/<run-id>.json` after every test case, one run at a
time per process, and a finished run also gets `<run-id>.html` next to it, so the folder can
be browsed without the testbench. The folder is the only store: any run JSON or HTML report
placed there is listed under its file name, and nothing remembers a file that was removed.
`GET /runs` also returns `reports_dir_windows` when the testbench runs in WSL and
`can_open_folder` for a loopback client. Progress is published on `/events/stream` as `run_started`,
`run_progress`, `run_finished` and `run_cancelled` events.

## Examples

Run the LPC test cases and save the report:

```bash
RUN=$(curl -s -X POST http://127.0.0.1:8080/api/v1/runs -H "content-type: application/json" \
  -d '{"selection":{"use_cases":["LPC"]},"tester":"me"}' | jq -r .run_id)
until [ "$(curl -s http://127.0.0.1:8080/api/v1/runs/$RUN | jq -r .status)" != running ]; do sleep 2; done
curl -o report.html "http://127.0.0.1:8080/api/v1/runs/$RUN/report.html"
```

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
