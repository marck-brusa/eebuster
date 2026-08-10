# Supported behavior and limitations

## Purpose

The testbench is an engineering tool for understanding and exercising an EEBUS device under
test. It is intended for development, integration, and regression testing.

It is not a conformance, certification, fuzzing, security-negative, performance, or robustness
suite.

## Required workflows

The product must support these end-to-end workflows:

1. Start one counterparty and optional observers.
2. Discover or configure a peer.
3. Trust a peer, approve or deny incoming pairing, and see connection failures.
4. Explore device type, entity addresses, advertised use cases, versions, and raw discovery.
5. Read live energy, grid, vehicle, photovoltaic, battery, and power-limit state when the peer
   advertises those use cases.
6. Apply and release power limits directly from the dashboard.
7. Run repeatable use-case scenarios from the dashboard, the CLI, or CI.
8. Inspect logs, live events, and mDNS visibility.

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
- provide an interactive time-series view with selectable ranges and sample inspection;
- provide a separate test runner and diagnostics workspace.

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
- `put`
- `assert`
- `expect_event`

Assertions support nested fields and:

- `equals`, `not_equals`
- `greater_than`, `greater_or_equal`
- `less_than`, `less_or_equal`
- `not_null`
- `contains`
- `length_greater_than`

The framework is deliberately small. Add domain-specific behavior through REST endpoints or
raw RPC, then express the expected result in YAML.

## Known limitations

- Untrust disconnects the peer from the current SPINE view, but reconnecting the same SKI may
  require restarting the active counterparty.
- The energy snapshot is best-effort. Unsupported or failed upstream calls appear in
  `errors`; partial data remains available.
- EV count is derived from discovered EV entities and their reported connected state. It is
  not inferred from electrical load.
- A vehicle's state of charge depends on the charging communication path and may be absent.
- Real mDNS requires native Linux host networking. Static addresses are the supported
  fallback elsewhere.

## Safety and confidentiality

- Live controls apply immediately.
- Do not publish internal stack control ports.
- Keep the real peer inventory out of version control.
- Do not regenerate identity keys during routine upgrades.
- Pin upstream source revisions to immutable commit SHAs.
