# Supported behavior and limitations

## Purpose

The testbench is an engineering tool for understanding and exercising an EEBUS device under
test. It is intended for development, integration, and regression testing.

It is not a certification, fuzzing, security-negative, performance, or robustness suite. It
does include a wire-level conformance checker (EEBUS JSON encoding and SPINE datagram rules,
with the standard reference on every finding), but passing it is not a certification claim.

## Required workflows

The product must support these end-to-end workflows:

1. Start one counterparty and optional observers.
2. Discover or configure a peer.
3. Trust a peer, approve or deny incoming pairing, and see connection failures.
4. Explore device type, entity addresses, advertised use cases, versions, and raw discovery.
5. Read live energy, grid, vehicle, photovoltaic, battery, and power-limit state when the peer
   advertises those use cases.
6. Apply and release power limits directly from the dashboard, including timed charge
   profiles (sequences of limits with per-step expiry).
7. Run repeatable use-case scenarios from the dashboard, the CLI, or CI.
8. Inspect logs, live events, and mDNS visibility.
9. Inspect every raw SHIP frame both directions, with conformance findings that cite the
   standard, live in the dashboard and via REST — and hand a session to the bundled
   EEBusTracer for offline deep dives.

## Current typed API

The primary adapter provides typed operations for:

- Limitation of Power Consumption (LPC)
  - active limit read/write/release;
  - failsafe value and minimum duration read/write;
  - nominal maximum;
  - heartbeat start/stop/state.
- Limitation of Power Production (LPP)
  - active limit read/write/release.
- Monitoring of Power Consumption (MPC)
  - power, energy, phase current, phase voltage, and frequency.
- Monitoring of Grid Connection Point (MGCP)
  - grid power, energy flow, phase values, frequency, and limitation factor.
- Energy intelligence snapshot
  - EVCC, EVSECC, CEVC, EVCEM, EVSOC;
  - VAPD photovoltaic data;
  - VABD battery data.
- Overload Protection by EV Charging Current Curtailment (OPEV)
  - per-phase current obligations read/write and declared constraints;
  - Energy Guard heartbeat start/stop (scenario 2) and error-state announcement (scenario 3).
- EVSE Commissioning and Configuration (EVSECC)
  - station identity: manufacturer data (vendor, brand, serial, software/hardware revision)
    and operating state.
- Optimization of Self-Consumption During EV Charging (OSCEV)
  - per-phase current recommendations read/write and declared constraints.
- Heat pump compressor flexibility (OHPCF)
  - flexibility offer read (optional power consumption, pausability, run windows).
- Message trace
  - raw frame list with cursor polling, single-frame lookup with the wire payload;
  - conformance summary aggregated by rule, split into errors and warnings.

The REST API remains the single surface for every registered operation, without a dedicated
method.

Typed support does not imply that a peer implements a use case. Support is determined from
the peer's live NodeManagement discovery data. Unsupported scenarios are skipped.

## Dashboard requirements

The desktop dashboard must:

- keep the selected peer visible on every page;
- expose errors instead of silently swallowing failed actions;
- show live values and distinguish absent, inactive, unsupported, and failed data;
- let an engineer drill from peer to entity to use case;
- label actions that change the connected device;
- provide an interactive time-series view with selectable ranges and sample inspection,
  including a second panel for per-phase current, per-phase voltage, or state of charge
  (one unit at a time, with a crosshair shared across panels);
- render per-phase values with asymmetric loading flagged;
- provide a message-trace workspace with per-frame conformance findings, and keep expanded
  rows expanded while the live stream updates;
- provide a charge-profile builder whose live runs carry per-step expiry (a dead-man's
  switch) and which exports the equivalent scenario YAML;
- provide a separate test runner and diagnostics workspace;
- be fully localized (en/de/zh-Hans), with the dictionaries guarded by CI.

The current history is session-local. Persistent time-series storage is not included.

## Scenario framework

Scenario files live in `scenarios/*.yaml`. A scenario declares:

```yaml
name: mpc-live-power
description: Reads total active power.
category: MPC
risk: read-only
requires:
  capabilities: [mpc.read]
  use_cases: [monitoringOfPowerConsumption]
peer: device-under-test
steps: []
```

Supported risk labels are conventions used by the UI: `read-only`, `live-control`, and
`disruptive`.

The implemented step verbs are:

- `wait_connected`
- `sleep`
- `log`
- `call`
- `put`, `post`, `delete` — each with an optional `expect_status:` so a scenario can require
  a refusal
- `assert`
- `expect_event`

Assertions support nested fields (numeric path segments index arrays, e.g.
`ev.vehicles.0.power_w`) and:

- `equals`, `not_equals`
- `greater_than`, `greater_or_equal`
- `less_than`, `less_or_equal`
- `not_null`
- `contains`
- `length_greater_than`
- `each_less_than` — every numeric element of an array under a bound
- `sum_matches` — array elements must add up to another field within a tolerance
- `duration_at_least`, `duration_at_most` — ISO-8601 comparisons

The framework is deliberately small. Add domain-specific behavior through REST endpoints,
then express the expected result in YAML.

## Known limitations

- Untrust disconnects the peer from the current SPINE view, but reconnecting the same SKI may
  require restarting the active counterparty.
- The energy snapshot is best-effort. Unsupported or failed upstream calls appear in
  `errors`; partial data remains available.
- EV count is derived from discovered EV entities and their reported connected state. It is
  not inferred from electrical load.
- A vehicle's state of charge depends on the charging communication path and may be absent.
- Real mDNS requires a layer-2 path to the device: native Windows, macOS, or Linux all work;
  NAT'd namespaces (WSL2, containers) do not. Static `peers:` addresses are the supported
  fallback where multicast cannot reach.

## Safety and confidentiality

- Live controls apply immediately.
- Do not publish internal stack control ports.
- Keep the real peer inventory out of version control.
- Do not regenerate identity keys during routine upgrades.
- Pin upstream source revisions to immutable commit SHAs.
