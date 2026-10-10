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
| EVSECC | `evseCommissioningAndConfiguration` | Station identity (vendor, brand, serial, software revision), operating state |
| EVCS | `evChargingSummary` | Advertisement only: the scenario check, no typed read |
| CEVC | `coordinatedEvCharging` | Strategy, demand, charge plan |
| EVCEM | `measurementOfElectricityDuringEvCharging` | Phases, current, power, charged energy |
| EVSOC | `evStateOfCharge` | Vehicle state of charge |
| VAPD | `visualizationOfAggregatedPhotovoltaicData` | Power, peak power, total yield |
| VABD | `visualizationOfAggregatedBatteryData` | Power, state of charge, energy |
| OPEV | `overloadProtectionByEvChargingCurrentCurtailment` | Per-phase current obligations read/write, constraints, heartbeat, error state |
| OSCEV | `optimizationOfSelfConsumptionDuringEvCharging` | Per-phase current recommendations read/write, constraints, heartbeat, error state |
| OHPCF | `optimizationOfSelfConsumptionByHeatPumpCompressorFlexibility` | Flexibility offer read |

That table is the complete set. A peer may advertise use cases outside it — the device browser
still lists them, because it reports what the peer advertises rather than what we implement, but
there is no typed read or write for them.

OPEV and OSCEV share one wire mechanism (per-phase `LoadControl` current limits towards the
EV entity) with opposite semantics: OPEV writes **obligations** the EV must not exceed
(overload protection of a site fuse), OSCEV writes **recommendations** the EV may follow
(absorbing solar excess). Historical note: these three had no eebus-go client implementation
before the 2026-07-31 upstream revision this tool now pins, and an even earlier version of
this table listed them as available "via Raw RPC", which was never true.

## What the simulator covers

The built-in simulated device answers LPC and MPC. Enabling `ev:` on it adds a vehicle as a
sub-entity of the charging station -- SPINE entity `[1,1]` -- advertising EVCC, EVCEM, EVSOC
and OPEV, with a battery that fills and per-phase currents that follow whatever limit is in
force. That makes every EV scenario except the LPP/MGCP/OHPCF ones runnable without
hardware, including the OPEV write path, which on real hardware needs a vehicle physically
present.

The EV side is built from SPINE server features directly (`internal/simulator/ev.go`):
eebus-go implements these use cases only for the CEM, since the other half is firmware in a
real vehicle.

## Scenario library

101 test cases, grouped as the test runner and the report group them. *Sc.* is the
scenario of the use case the test case checks; *Reference* names the abstract test case of the
EEBUS High-Level Test Specification (LPC, LPP, MPC, MGCP) or the requirement ids of the use case
specification. Every use case also has a `<acronym>-scenarios-advertised` test case that fails
when the device advertises the use case without a scenario the specification makes mandatory
for it.

Verification says how a verdict is reached: `wire` from frames and discovery, `readback` by
writing and reading back over the use case, `needs-device-probe` for criteria that are internal
device state; those are skipped as not verifiable on the wire.

| Use case | Test case | Sc. | Risk | Verification | Reference | Criterion |
|---|---|---|---|---|---|---|
| Common | `common-manufacturer-data` |  | read-only | readback |  | Device classification: brand or vendor, and a serial number. |
| Common | `common-use-case-versions` |  | read-only | wire |  | Use-case discovery: every advertised use case states an actor and a version. |
| Common | `conformance-window` |  | read-only | wire |  | Wire format of everything sent during discovery and reads (SHIP TS §11, SPINE TS §5). |
| Common | `device-profile-discovery` |  | read-only | wire |  | Discovery: entities, features and advertised use cases. |
| Common | `smoke-pairing` |  | read-only |  |  | SHIP pairing and SPINE setup with the configured device. |
| LPC | `lpc-basic-limit` | 1 | live-control | readback | ATC_COM_PT_CSConnection_007 | LPC scenario 1: a 4.2 kW consumption limit, written and read back. |
| LPC | `lpc-cleanup` (cleanup) |  | live-control | readback |  | LPC: heartbeat restarted and the consumption limit released. |
| LPC | `lpc-duration-roundtrip` | 1 | live-control | readback | ATC_COM_PT_CSTransition6_001 | LPC scenario 1: a limit with a duration. |
| LPC | `lpc-factory-defaults` |  | read-only | needs-device-probe | ATC_COM_PT_CSInit_002 | LPC: default values after a factory reset. |
| LPC | `lpc-failsafe-duration-range` | 2 | live-control | readback | ATC_COM_PT_CSConnection_008 | LPC failsafe: a failsafe duration below the 2 h minimum. |
| LPC | `lpc-failsafe-on-heartbeat-loss` | 3 | disruptive | readback | ATC_COM_PT_CSTransition5_001 | LPC scenario 3: no heartbeat for 130 s, then consumption at or below the failsafe limit. |
| LPC | `lpc-failsafe-persistence` |  | read-only | needs-device-probe | ATC_COM_PT_CSInit_003 | LPC: failsafe limit and duration survive a restart of the device. |
| LPC | `lpc-failsafe-value-set` | 2 | live-control | readback | ATC_COM_PT_CSConnection_008 | LPC scenario 2: every configured failsafe limit and duration, written and read back. |
| LPC | `lpc-failsafe-window` | 2 | read-only | readback |  | LPC failsafe: the announced minimum failsafe duration. |
| LPC | `lpc-failsafe` | 2 | live-control | readback | ATC_COM_PT_CSConnection_008 | LPC failsafe: the failsafe power and duration, written and read back. |
| LPC | `lpc-heartbeat-continuity` | 3 | read-only | wire | ATC_COM_PT_CSHeartbeat_001 | LPC scenario 3: five device heartbeats in a row, no gap longer than 60 s. |
| LPC | `lpc-heartbeat-timeout` | 3 | read-only | wire | ATC_COM_PT_CSHeartbeat_001 | LPC scenario 3: the heartbeat timeout the device announces. |
| LPC | `lpc-limit-above-maximum` | 1 | live-control | readback | ATC_COM_PT_CSConnection_006 | LPC scenario 1: a limit above the device's maximum, accepted as sent or capped. |
| LPC | `lpc-limit-expiry` | 1 | live-control | readback | ATC_COM_PT_CSTransition6_001 | LPC: a 5 s limit that has to end by itself. |
| LPC | `lpc-limit-value-set` | 1 | live-control | readback | ATC_COM_PT_CSConnection_007 | LPC scenario 1: every configured consumption limit, written and read back, without and with a duration. |
| LPC | `lpc-negative-limit` | 1 | live-control | readback | ATC_COM_PT_CSConnection_003 | LPC scenario 1: a negative consumption limit and a negative failsafe limit, both rejected. |
| LPC | `lpc-no-write-before-heartbeat` |  | disruptive | readback | ATC_COM_PT_CSFS_001 | LPC: a limit written while the energy guard is silent is not evaluated; after the heartbeat returns, a deactivation is accepted. |
| LPC | `lpc-nominal-max` | 4 | read-only | readback | ATC_COM_PT_CSUnlCntrl_003 | LPC: the device's declared maximum consumption. |
| LPC | `lpc-read-current` | 1 | read-only | readback |  | LPC: the active consumption limit, read only. |
| LPC | `lpc-release` | 1 | live-control | readback | ATC_COM_PT_CSTransition6_002 | LPC scenario 1: deactivating an active consumption limit. |
| LPC | `lpc-scenarios-advertised` |  | read-only | wire |  | LPC: the advertised scenarios against the specification's mandatory scenarios. |
| LPP | `lpp-basic-limit` | 1 | live-control | readback | ATC_COM_PT_CSConnection_007 | LPP scenario 1: a -1500 W production limit, written and read back. |
| LPP | `lpp-cleanup` (cleanup) |  | live-control | readback |  | LPP: the production limit released. |
| LPP | `lpp-failsafe-window` | 2 | read-only | readback | LPP-TS-013, LPP-TS-038 | LPP scenario 2: the announced failsafe production limit and minimum failsafe duration. |
| LPP | `lpp-failsafe` | 2 | live-control | readback | ATC_COM_PT_CSConnection_008 | LPP scenario 2: the failsafe production limit and duration, written and read back. |
| LPP | `lpp-nominal-max` | 4 | read-only | readback | ATC_COM_PT_CSUnlCntrl_003 | LPP scenario 4: the device's declared maximum production. |
| LPP | `lpp-read-current` | 1 | read-only | readback |  | LPP: the active production limit, read only. |
| LPP | `lpp-scenarios-advertised` |  | read-only | wire |  | LPP: the advertised scenarios against the specification's mandatory scenarios. |
| MPC | `mpc-consumption-sign` | 1 | read-only | readback | ATC_SCE1_PT_MUTotalActivePower_001 | MPC scenario 1: consumption reported positive while charging. |
| MPC | `mpc-live-power` | 1 | read-only | readback | ATC_SCE1_PT_MUTotalActivePower_001 | MPC: total power. |
| MPC | `mpc-notification` | 1 | read-only | wire | ATC_COM_PT_MUNotification_001 | MPC: a total power notification from the device within 120 s. |
| MPC | `mpc-phase-consistency` | 1 | read-only | readback | ATC_SCE1_PT_MUPhaseActivePower_001 | MPC scenario 1: the phase powers against the total. |
| MPC | `mpc-scenario-2-energy` | 2 | read-only | readback | ATC_SCE2_PT_MUTotalConsumedEnergy_001 | MPC scenario 2: consumed energy, which never decreases. |
| MPC | `mpc-scenario-3-current` | 3 | read-only | readback | ATC_SCE3_PT_MUActiveACCurrent_001 | MPC scenario 3: one current per phase, plausible in ampere. |
| MPC | `mpc-scenario-4-voltage` | 4 | read-only | readback | ATC_SCE4_PT_MUACVoltage_001 | MPC scenario 4: one voltage per phase, plausible in volt. |
| MPC | `mpc-scenario-5-frequency` | 5 | read-only | readback | ATC_SCE5_PT_MUFrequency_001 | MPC scenario 5: the grid frequency, between 45 and 65 Hz. |
| MPC | `mpc-scenarios-advertised` |  | read-only | wire |  | MPC: the advertised scenarios against the specification's mandatory scenarios. |
| MGCP | `mgcp-scenario-1-limitation-factor` | 1 | read-only | readback | ATC_SCE1_PT_GCPPowerLimitFactor_001 | MGCP scenario 1: the PV feed-in power limitation factor, 0 to 100 %. |
| MGCP | `mgcp-scenario-2-power` | 2 | read-only | readback | ATC_SCE2_PT_GCPTotalActivePower_001 | MGCP scenario 2: the momentary power at the grid connection point. |
| MGCP | `mgcp-scenario-3-feed-in-energy` | 3 | read-only | readback | ATC_SCE3_PT_GCPTotalFeedInEnergy_001 | MGCP scenario 3: the total feed-in energy. |
| MGCP | `mgcp-scenario-4-consumed-energy` | 4 | read-only | readback | ATC_SCE4_PT_GCPTotalConsumedEnergy_001 | MGCP scenario 4: the total consumed energy, positive and never decreasing. |
| MGCP | `mgcp-scenario-5-current` | 5 | read-only | readback | ATC_SCE5_PT_GCPActiveACCurrent_001 | MGCP scenario 5: one current per phase, plausible in ampere. |
| MGCP | `mgcp-scenario-6-voltage` | 6 | read-only | readback | ATC_SCE6_PT_GCPACVoltage_001 | MGCP scenario 6: one voltage per phase, plausible in volt. |
| MGCP | `mgcp-scenario-7-frequency` | 7 | read-only | readback | ATC_SCE7_PT_GCPFrequency_001 | MGCP scenario 7: the grid frequency, between 45 and 65 Hz. |
| MGCP | `mgcp-scenarios-advertised` |  | read-only | wire |  | MGCP: the advertised scenarios against the specification's mandatory scenarios. |
| EVCC | `evcc-scenario-1-connected` | 1 | read-only | readback | EVCC-001 | EVCC scenario 1: a connected vehicle is reported. |
| EVCC | `evcc-scenario-2-communication-standard` | 2 | read-only | readback | EVCC-002, EVCC-003, EVCC-004, EVCC-005 | EVCC scenario 2: the vehicle's communication standard, one of the three the use case allows. |
| EVCC | `evcc-scenario-3-asymmetric-support` | 3 | read-only | readback |  | EVCC scenario 3: whether the vehicle supports asymmetric charging. |
| EVCC | `evcc-scenario-4-identification` | 4 | read-only | readback | EVCC-007, EVCC-008 | EVCC scenario 4: the vehicle's identification, a MAC address in the specified format. |
| EVCC | `evcc-scenario-5-manufacturer` | 5 | read-only | readback |  | EVCC scenario 5: the vehicle's manufacturer information. |
| EVCC | `evcc-scenario-6-power-limits` | 6 | read-only | readback | EVCC-016, EVCC-017 | EVCC scenario 6: the vehicle's minimum charging power; maximum and standby as observed. |
| EVCC | `evcc-scenario-7-sleep-mode` | 7 | read-only | readback |  | EVCC scenario 7: the vehicle's sleep mode state. |
| EVCC | `evcc-scenario-8-disconnected` | 8 | read-only | needs-device-probe |  | EVCC scenario 8: the vehicle disconnects. |
| EVCC | `evcc-scenarios-advertised` |  | read-only | wire |  | EVCC: the advertised scenarios against the specification's mandatory scenarios. |
| EVSECC | `evsecc-scenario-1-manufacturer` | 1 | read-only | readback | EVSECC-010, EVSECC-011, EVSECC-012, EVSECC-013, EVSECC-014, EVSECC-015 | EVSECC scenario 1: at least one recommended manufacturer field of the station. |
| EVSECC | `evsecc-scenario-2-operating-state` | 2 | read-only | readback | EVSECC-020 | EVSECC scenario 2: the station's operating state, normalOperation or failure. |
| EVSECC | `evsecc-scenarios-advertised` |  | read-only | wire |  | EVSECC: the advertised scenarios against the specification's mandatory scenarios. |
| EVCS | `evcs-scenarios-advertised` |  | read-only | wire |  | EVCS: the advertised scenarios against the specification's mandatory scenarios. |
| CEVC | `cevc-scenarios-advertised` |  | read-only | wire |  | CEVC: the advertised scenarios against the specification's mandatory scenarios. |
| EVCEM | `evcem-scenario-1-current` | 1 | read-only | readback |  | EVCEM scenario 1: the charging current per phase, plausible in ampere. |
| EVCEM | `evcem-scenario-2-power` | 2 | read-only | readback | EVCEM-003, EVCEM-004 | EVCEM scenario 2: the charging power per phase, plausible in watt. |
| EVCEM | `evcem-scenario-3-energy` | 3 | read-only | readback | EVCEM-005 | EVCEM scenario 3: the energy charged in the session, never decreasing. |
| EVCEM | `evcem-scenarios-advertised` |  | read-only | wire |  | EVCEM: the advertised scenarios against the specification's mandatory scenarios. |
| EVSOC | `evsoc-scenario-1-state-of-charge` | 1 | read-only | readback | EVSOC-001 | EVSOC scenario 1: the state of charge, 0 to 100 % with the description the use case prescribes. |
| EVSOC | `evsoc-scenario-2-capacity` | 2 | read-only | readback | EVSOC-002 | EVSOC scenario 2: the vehicle's nominal battery capacity in Wh. |
| EVSOC | `evsoc-scenario-3-health` | 3 | read-only | readback | EVSOC-003 | EVSOC scenario 3: the battery state of health, 0 to 100 %. |
| EVSOC | `evsoc-scenario-4-range` | 4 | read-only | readback | EVSOC-004 | EVSOC scenario 4: the travel range in metres. |
| EVSOC | `evsoc-scenarios-advertised` |  | read-only | wire |  | EVSOC: the advertised scenarios against the specification's mandatory scenarios. |
| VAPD | `pv-production` | 2 | read-only | readback |  | PV: current photovoltaic production. |
| VAPD | `vapd-scenario-1-peak-power` | 1 | read-only | readback |  | VAPD scenario 1: the nominal peak power of the PV system. |
| VAPD | `vapd-scenario-3-yield` | 3 | read-only | readback | VAPD-003a, VAPD-004 | VAPD scenario 3: the cumulated PV yield, negative under the load convention. |
| VAPD | `vapd-scenarios-advertised` |  | read-only | wire |  | VAPD: the advertised scenarios against the specification's mandatory scenarios. |
| VABD | `battery-state` | 1 | read-only | readback |  | Battery use case: aggregated battery power and state data. |
| VABD | `vabd-scenario-2-charge-energy` | 2 | read-only | readback |  | VABD scenario 2: the cumulated charge energy. |
| VABD | `vabd-scenario-3-discharge-energy` | 3 | read-only | readback | VABD-003a, VABD-005 | VABD scenario 3: the cumulated discharge energy, negative under the load convention. |
| VABD | `vabd-scenario-4-state-of-charge` | 4 | read-only | readback |  | VABD scenario 4: the battery state of charge, 0 to 100 %. |
| VABD | `vabd-scenarios-advertised` |  | read-only | wire |  | VABD: the advertised scenarios against the specification's mandatory scenarios. |
| OPEV | `opev-asymmetric` | 1 | live-control | readback | OPEV-002 | OPEV scenario 1: a different current limit per phase (6/10/16 A). |
| OPEV | `opev-cleanup` (cleanup) |  | live-control | readback |  | OPEV: heartbeat restarted and the failure state cleared. |
| OPEV | `opev-constraints` | 1 | read-only | readback |  | OPEV: the vehicle's current range (min/max) and the phases it takes limits on. |
| OPEV | `opev-error-state` | 3 | disruptive | readback | OPEV-007 | OPEV scenario 3: the Energy Guard announces an error; the car stays within the last obligation. |
| OPEV | `opev-guard-subscription` | 2 | read-only | wire | OPEV-005, OPEV-007 | OPEV scenarios 2 and 3: the vehicle subscribed to our heartbeat and operating state. |
| OPEV | `opev-heartbeat-loss` | 2 | disruptive | readback | OPEV-005 | OPEV scenario 2: our heartbeat towards the car stops; the car stays within the last obligation. |
| OPEV | `opev-limit-descriptions` | 1 | read-only | wire | OPEV-002 | OPEV scenario 1: the limit descriptions, phases and permitted ranges the vehicle declares. |
| OPEV | `opev-limit-obeyed` | 1 | live-control | readback | OPEV-001 | OPEV scenario 1: the measured current follows a 6 A obligation and the 0 A pause. |
| OPEV | `opev-limit-roundtrip` | 1 | live-control | readback | OPEV-001, OPEV-003, OPEV-004 | OPEV scenario 1: a 6 A limit on every declared phase, then released. |
| OPEV | `opev-scenarios-advertised` |  | read-only | wire |  | OPEV: the advertised scenarios against the specification's mandatory scenarios. |
| OPEV | `opev-zero-pause` | 1 | live-control | readback | OPEV-001 | OPEV scenario 1: a 0 A limit, the pause signal. |
| OSCEV | `oscev-cleanup` (cleanup) |  | live-control | readback |  | OSCEV: heartbeat restarted and the failure state cleared. |
| OSCEV | `oscev-guard-subscription` | 2 | read-only | wire | OSCEV-005, OSCEV-007 | OSCEV scenarios 2 and 3: the vehicle subscribed to our heartbeat and operating state. |
| OSCEV | `oscev-limit-descriptions` | 1 | read-only | wire | OSCEV-002 | OSCEV scenario 1: the recommendation descriptions, phases and permitted ranges the vehicle declares. |
| OSCEV | `oscev-read` | 1 | read-only | readback |  | OSCEV: the recommended currents for solar self-consumption. |
| OSCEV | `oscev-scenario-1-recommendation` | 1 | live-control | readback | OSCEV-001, OSCEV-003 | OSCEV scenario 1: a 6 A recommendation on every declared phase, then released. |
| OSCEV | `oscev-scenarios-advertised` |  | read-only | wire |  | OSCEV: the advertised scenarios against the specification's mandatory scenarios. |
| OHPCF | `ohpcf-read` | 1 | read-only | readback |  | OHPCF: the heat pump's flexibility offer. |
| OHPCF | `ohpcf-scenarios-advertised` |  | read-only | wire |  | OHPCF: the advertised scenarios against the specification's mandatory scenarios. |

Scenarios run through the REST API, the same path the dashboard uses.

## YAML format

```yaml
name: evcc-scenario-1-connected
description: >
  Requires a vehicle entity in the energy snapshot that reports itself connected. The use case
  obliges the EV to inform the CEM when it connects; without a vehicle the test is skipped as
  a precondition, with one the vehicle must show up connected.
covers: "EVCC scenario 1: a connected vehicle is reported."
goal: "Proves the device shows a plugged-in car to the HEMS."
hint: "Needs a vehicle."
category: EVCC
risk: read-only
spec: {use_case: EVCC, scenario: 1, requirements: [EVCC-001], verification: readback}
requires:
  capabilities: [energy_snapshot]
  use_cases: [evCommissioningAndConfiguration]
  vehicle: true
peer: device-under-test
steps:
  - wait_connected: {timeout: 30s}
  - assert:
      get: "/api/v1/energy/{peer.ski}/snapshot"
      length_at_least: {ev.vehicles: 1}
      greater_or_equal: {ev.connected_count: 1}
      equals: {ev.vehicles.0.connected: true}
```

`{peer.ski}` and other dotted references are resolved from the scenario context. A leading
`wait_connected` runs before advertised-use-case requirements are checked.

### Test-run keys

```yaml
spec:                                   # what the test case checks, by identifier
  use_case: LPC                         # acronym or SPINE name; the document is filled in
  scenario: 2
  test_case: ATC_COM_PT_CSConnection_008
  requirements: [LPC-TS-016]
  verification: readback                # wire | readback | needs-device-probe
long_running: true                      # waits on specification timers; runs only when asked
cleanup: true                           # runs last, also after a cancellation
requires:
  scenarios: [2]                        # skips when this optional/recommended scenario is not advertised
  vehicle: true                         # skips without a connected vehicle
  charging: true                        # skips without a charging session
  parameters: [failsafe_w]              # skips when the parameter is neither configured nor derivable
finally:                                # runs after the steps whenever they started
  - put: {path: "/api/v1/lpc/{peer.ski}/limit", template: lpc.limit.release}
```

A mandatory scenario that is not advertised never skips its test cases: they run, and the
`*-scenarios-advertised` test case reports the missing advertisement.

Device parameters come from the peer's `parameters:` block in `eebus.yaml` and are referenced
as `{params.limits_w.0}`. Absent ones are derived: `nominal_max_w` from the device,
`limits_w` as 100, 50 and 25 % of it, `failsafe_w` as 50 and 25 %, and fixed
`limit_durations` and `failsafe_durations`. The report lists which values were derived.

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
any_not_null         # [a, b]: at least one of the keys has a value
between              # {key: [low, high]}
each_between         # {array: [low, high]} for every element
one_of               # {key: [allowed, values]}
each_not_null        # {array: field or [fields]}: every element carries them
some_not_null        # {array: [field, "alt1|alt2"]}: some element carries each
length_at_least
```

An assertion on a response that is a JSON list addresses it as `items` and `count`.

Dotted key paths descend into arrays with numeric segments: `ev.vehicles.0.power_w`.

### Event

```yaml
- expect_event: {event: eg-lpc-DataUpdateLimit, within: 5s}
```

The event must be published after the previous step began, so a notify caused by the write
before it counts.

### Loop, capture and specification checks

```yaml
- for_each:                             # repeat steps per item; {limit} and {limit_index} are bound
    items: "{params.limits_w}"
    as: limit
    steps:
      - put: {path: "/api/v1/lpc/{peer.ski}/limit", body: {value_w: "{limit}", is_active: true}}
- capture:                              # keep values for later steps as {captured.<name>}
    get: "/api/v1/mpc/{peer.ski}"
    values: {consumed: energy_consumed_wh}
- scenarios_advertised: {use_case: OPEV}  # advertised scenarios against the specification table
- conformance: {max_errors: 0}          # no conformance error in this test case's frames
- wait_for:                             # repeat an assertion once a second until it holds
    get: "/api/v1/lpc/{peer.ski}/heartbeat"
    not_null: [heartbeat_timeout_s]
    timeout: 70s
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
./eebus-testbench run-all scenarios -use-case LPC,MPC -read-only -tester me \
    -html report.html -xlsx report.xlsx -json run.json
./eebus-testbench report run.json -html report.html   # or report report.html -json run.json
```

`run` and `run-all` execute the local scenario files against the instance at `-base-url` as a
recorded run and write the requested formats. `-include-long-running` adds the long-running
test cases, `-ids a,b` selects test cases, `-use-case Common` the use-case independent ones.
The exit code is non-zero unless the run passed.

REST, which is what both of the above and the dashboard use:

```bash
curl -X POST http://127.0.0.1:8080/api/v1/scenarios/mpc-live-power/run
curl -X POST http://127.0.0.1:8080/api/v1/scenarios/run-all
```

`-junit` writes a report so CI shows results as ordinary test cases.

Unmet requirements produce `skipped` with a reason: `use_case_not_advertised`,
`scenario_not_advertised`, `capability_missing`, `phases_not_declared`,
`precondition_not_met`, `not_verifiable_on_wire`, or `not_run` after a cancellation. A
failed step stops that scenario, while a suite continues with the remaining scenarios.

`peer:` is resolved through the configured `peers:` list, so a scenario's `peer:` name must match
a `peers[].name` exactly. Every bundled scenario targets `device-under-test`; naming the entry
anything else fails all of them. The error names the configured peers so the mismatch is visible.

The MPC measurement scenarios assert non-null readings and therefore fail, rather than skip,
when the device publishes no values. The EV measurement scenarios require a charging session
and skip without one.
