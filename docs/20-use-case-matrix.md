# Use cases and tests

## Live capability catalog

The device browser reads the peer's advertised use-case information. The catalog below adds
human-readable labels; it never invents support.

| Acronym | Advertised use-case name | Dashboard access |
|---|---|---|
| LPC | `limitationOfPowerConsumption` | Read/write limit, failsafe, nominal maximum, heartbeat |
| LPP | `limitationOfPowerProduction` | Read/write limit |
| MPC | `monitoringOfPowerConsumption` | Power, energy, phase values, frequency |
| MGCP | `monitoringOfGridConnectionPoint` | Grid power, energy, phase values, frequency |
| EVCC | `evCommissioningAndConfiguration` | Connected state, charge state, sleep mode, standard, identity, manufacturer, limits |
| EVSECC | `evseCommissioningAndConfiguration` | Station identity and operating state |
| CEVC | `coordinatedEvCharging` | Strategy, demand, charge plan |
| EVCEM | `measurementOfElectricityDuringEvCharging` | Phases, current, power, charged energy |
| EVSOC | `evStateOfCharge` | Vehicle state of charge |
| VAPD | `visualizationOfAggregatedPhotovoltaicData` | Power, peak power, total yield |
| VABD | `visualizationOfAggregatedBatteryData` | Power, state of charge, energy |

That table is the complete set. A peer may advertise use cases outside it — the device browser
still lists them, because it reports what the peer advertises rather than what we implement, but
there is no typed read or write for them.

**Not supported (no typed access; the tool has no raw-RPC surface):**

| Acronym | Advertised use-case name |
|---|---|
| OPEV | `overloadProtectionByEvChargingCurrentCurtailment` |
| OSCEV | `optimizationOfSelfConsumptionDuringEvCharging` |
| OHPCF | `optimizationOfSelfConsumptionByHeatPumpCompressorFlexibility` |

`eebus-go` has no implementation of these at the pinned revision — the complete upstream set is
`cem/{cevc,evcc,evcem,evsecc,evsoc,vabd,vapd}`, `cs/lpc`, `eg/{lpc,lpp}`, `ma/{mgcp,mpc}` and
`mu/mpc`. With no registered client use case there is no local feature to send from, so
supporting OPEV's current-limit write (which other tools do offer) means implementing it
against SPINE `LoadControl` directly. An earlier version of this table listed all three as
available "via Raw RPC" — wrong then, and the raw-RPC surface itself was removed later (its
endpoint never existed in the Go rewrite).

## Included scenario examples

| Scenario | Risk | Purpose |
|---|---|---|
| `smoke-pairing` | read-only | Wait for a connected peer |
| `device-profile-discovery` | read-only | Read entities, use cases, and discovery |
| `lpc-read-current` | read-only | Read current consumption limit |
| `lpc-nominal-max` | read-only | Validate positive declared maximum |
| `lpc-basic-limit` | live-control | Apply 4.2 kW and verify readback |
| `lpc-release` | live-control | Release the active limit |
| `lpc-limit-expiry` | live-control | Verify a short limit expires |
| `lpc-failsafe` | live-control | Write/read failsafe and duration |
| `lpc-heartbeat-loss` | disruptive | Stop heartbeat for failsafe observation |
| `lpc-failsafe-window` | read-only | Announced failsafe duration must lie in the 2–24 h window (LPC UC TS) |
| `lpc-duration-roundtrip` | live-control | A written limit duration must be visible on readback |
| `conformance-window` | read-only | Exercise reads, then require the wire trace to be free of conformance errors |
| `mpc-phase-plausibility` | read-only | Per-phase currents/voltages must be plausible for their units (catches mA-as-A scale faults) |
| `mpc-phase-consistency` | read-only | Per-phase powers must add up to the reported total |
| `ev-charged-energy` | read-only | EVCEM scenario 3, when advertised, must deliver a charged-energy value |
| `lpp-read-current` | read-only | Read current production limit |
| `lpp-basic-limit` | live-control | Apply and verify a production limit |
| `mpc-live-power` | read-only | Read total consumption |
| `mpc-electrical-quality` | read-only | Read phase current, voltage, and frequency |
| `mgcp-grid-state` | read-only | Read grid power and frequency |
| `ev-inventory` | read-only | Count connected and charging EV entities |
| `ev-charging-measurements` | read-only | Read charging power and energy |
| `pv-production` | read-only | Read photovoltaic production |
| `battery-state` | read-only | Read battery power and state |

Scenarios run through the REST API, the same path the dashboard uses.

## YAML format

```yaml
name: ev-inventory
description: Counts connected and charging EV entities.
category: EV charging
risk: read-only
requires:
  capabilities: [energy_snapshot]
  use_cases: [evCommissioningAndConfiguration]
peer: device-under-test
steps:
  - wait_connected: {timeout: 30s}
  - assert:
      get: "/api/v1/energy/{peer.ski}/snapshot"
      not_null: [ev.connected_count, ev.charging_count, ev.vehicles]
```

`{peer.ski}` and other dotted references are resolved from the scenario context. A leading
`wait_connected` runs before advertised-use-case requirements are checked.

## Step verbs

### Wait

```yaml
- wait_connected: {timeout: 30s}
- sleep: 2
```

### REST write

```yaml
- put:
    path: "/api/v1/lpc/{peer.ski}/limit"
    body: {value_w: 4200, is_active: true, is_changeable: true, duration: "PT15M"}
- post: {path: "/api/v1/lpc/heartbeat/start"}
- delete: {path: "/api/v1/trace"}
```

Use `template:` instead of `body:` to load an entry from the built-in template library
(`internal/templates/templates.yaml`, served at `GET /api/v1/templates`). An optional
`expect_status: 502` asserts an exact HTTP status instead of the default any-2xx rule — that
is how a scenario states that the device MUST refuse an operation.

### Raw call

```yaml
- call:
    method: eg-lpc/StartHeartbeat
    args: []
```

### Assertion

```yaml
- assert:
    get: "/api/v1/mpc/{peer.ski}"
    not_null: [power_w]
    greater_than: {frequency_hz: 0}
```

Available comparisons:

```text
equals
not_equals
greater_than
greater_or_equal
less_than
less_or_equal
not_null
contains
length_greater_than
each_less_than       # every numeric element of an array stays under the bound
sum_matches          # {array: {total: other_field, tolerance_percent: N}}
duration_at_least    # ISO-8601, e.g. {duration: "PT2H"}
duration_at_most
```

Dotted key paths descend into arrays with numeric segments: `ev.vehicles.0.power_w`.

### Event

```yaml
- expect_event: {event: eg-lpc-DataUpdateLimit, within: 5s}
```

### Note

```yaml
- log: "inspect the device's local state"
```

## Running

Dashboard: open **Test runner**.

CLI, against a running instance:

```bash
./eebus-testbench run scenarios/mpc-live-power.yaml -base-url http://127.0.0.1:8080
./eebus-testbench run-all [scenarios-dir] -base-url http://127.0.0.1:8080 -junit results.xml
```

REST, which is what both of the above and the dashboard use:

```bash
curl -X POST http://127.0.0.1:8080/api/v1/scenarios/mpc-live-power/run
curl -X POST http://127.0.0.1:8080/api/v1/scenarios/run-all
```

`-junit` writes a report so CI shows results as ordinary test cases.

Unsupported capability or peer use-case requirements produce `skipped`. A failed step stops
that scenario, while a suite continues with the remaining scenarios.

`peer:` is resolved through the configured `peers:` list, so a scenario's `peer:` name must match
a `peers[].name` exactly. Every bundled scenario targets `device-under-test`; naming the entry
anything else fails all of them. The error names the configured peers so the mismatch is visible.

The measurement scenarios (`mpc-live-power`, `mpc-electrical-quality`,
`ev-charging-measurements`) assert non-null readings and therefore fail, rather than skip, unless
a charging session is actually running.
