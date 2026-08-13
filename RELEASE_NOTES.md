# Release notes

## 1.0.0-rc3

Field-report fixes, mostly around one theme: the launch directory could silently change
everything.

- **Device explorer and use-case browser rendered nothing in rc2.** Two functions shared the
  name `renderProfile` (device detail vs. charge-profile builder), and in JavaScript the later
  declaration silently wins — selecting a peer rendered the wrong one. Renamed, and CI now
  fails on duplicate top-level function declarations, which `node --check` cannot see.
- **The launch directory no longer matters.** `eebus.yaml`, the data directory (and with it
  the identity/SKI!), and `scenarios/` were all resolved against the *current working
  directory* — starting the release binary from anywhere else silently ignored the edited
  config (so `simulator.enabled: false` "didn't work"), minted a **new identity**, and broke
  every existing pairing. Defaults now fall back to the executable's own directory, and
  startup prints the resolved config path and data directory unmissably.
- **Pairings initiated from the device's side now survive a testbench restart.** A device
  that paired *itself* to the testbench (auto-accepted) never passed through the trust API,
  so nothing was persisted and reconnection after a restart depended entirely on the device's
  own retry policy — which gives up. Every completed connection is now persisted to
  `trusted-skis.json` and re-dialled at the next start. Verified with a two-instance restart
  cycle: reconnect within seconds, no re-pairing.
- **The device browser and use-case workbench now show real data.** The profile endpoint
  carried only flat use-case name strings — a Go-rewrite gap — while the dashboard rendered
  fields (availability, actor, version, scenarios, catalog labels) that only the old Python
  facade ever served, so every capability chip claimed NOT ADVERTISED and the browser cards
  were empty. The profile now includes `use_case_details`, enriched per entity from live
  NodeManagement discovery plus the documented catalog; entity addresses also render again
  (the JS still expected the facade's `{entity: [...]}` wrapper). Verified against a live
  charging system: six use cases with actors, versions, and scenario lists.
- **Expanded trace and event rows stay expanded.** Both tables rebuild on every live
  update, which collapsed an opened frame within seconds unless the stream was paused.
  Expansion is now tracked per frame (by its sequence number) and re-applied on each render.
- Clean shutdowns send mDNS goodbyes (verified); a hard kill cannot, so a stale scan entry on
  a device is possible but — with the identity now stable — harmless. Documented in
  QUICKSTART's troubleshooting table.

## 1.0.0-rc2

The debugging release: the testbench can now show what is actually on the wire, judge it
against the standard, and drive shaped load profiles — and it speaks three languages while
doing it.

### Message trace and conformance checking

- **New "Message trace" dashboard page and `/api/v1/trace` endpoints.** Every SHIP frame is
  captured at the websocket layer, *before* the stack's own JSON repairs — the vendored stack
  silently rewrites malformed payloads (including `[]` → `{}`), which used to destroy exactly
  the evidence needed when a device sends structurally broken messages.
- **Built-in conformance checker** (see `internal/conformance`): every frame is checked
  against the EEBUS JSON encoding rules (SHIP TS 1.0.1 §11.4/§11.5) and the SPINE datagram
  rules (SPINE TS ProtocolSpecification 1.3.0 §5). Detected fault classes include objects
  carrying several elements, empty objects, empty array instances such as
  `"permittedValueSet":[[]]` ("arrays where objects are expected"), missing mandatory header
  elements, invalid `cmdClassifier`, replies without `msgCounterReference`, trailing NUL
  bytes, and msgCounter reuse. A per-peer session correlates the device's own declared
  measurement units with later values, catching thousand-fold scale faults (16 A sent as
  16000 A). Every finding carries its reference into the standard.
- **Conformance summary with jump-to-evidence**, and non-conformant frames surfacing as
  `spine`-level events.

### New test scenarios

`conformance-window`, `mpc-phase-plausibility`, `mpc-phase-consistency`,
`lpc-failsafe-window`, `lpc-duration-roundtrip`, `ev-charged-energy` — each aimed at a fault
class seen from real devices, with the standard reference in its description. The runner
gained `post`/`delete` verbs, `expect_status`, `each_less_than`, `sum_matches`,
`duration_at_least/at_most`, and array indexing in assertion paths.

### Dashboard

- **Complete localization** in English, German, and simplified Chinese — every string, not
  just the headings — with CI guarding the dictionaries against drift.
- **Phase balance panel**: per-phase power, current, and voltage per source with asymmetric
  loading flagged (a two-phase charger on a three-phase connection is now visible at a
  glance). The simulated device publishes a deliberately asymmetric profile to demonstrate.
- **Charge profile builder**: timed sequences of LPC limits with presets and a preview, run
  live (each step carries its own duration as a dead-man's switch) or exported as scenario
  YAML.
- The API reference (`/docs`, `/redoc`) is now fully embedded and works offline; the spec is
  also served under the documented `/api/v1/openapi.yaml` path.

### EEBusTracer bundled and auto-started

The release archive now includes [EEBusTracer](https://github.com/uhl/EEBusTracer) (MIT,
pinned v0.7.0) for offline deep dives. `serve` spawns it automatically on
`http://127.0.0.1:8090` when the binary sits next to the executable, links it from the
dashboard sidebar (new tab — it is a separate product), and appends every raw frame to
`<data-dir>/frames.log` for importing in its UI. `-tracer=false` / `-tracer-port` /
`-frame-log` control it; an explicit `tracer_url` in `eebus.yaml` links a self-managed
instance instead.

### Validated against real hardware

Verified end to end against a live charging system over Wi-Fi: mutual SHIP pairing, LPC
reads and writes, and the complete scenario suite. The new scenarios caught real device
behaviour from the outside on the first run — a limit write silently not applied on
readback, failsafe reads answering "data not available" because the device announces keys
whose out-of-range values its stack drops, and missing limit-update notifications after
state-only writes — each reported with its reference into the standard.

### Housekeeping

- Release targets are now linux/amd64, windows/amd64, and **darwin/arm64** (Apple Silicon);
  linux/arm64 was dropped. `scripts/build-dist.sh` builds the same archives locally.
- Removed two dead UI areas whose endpoints did not exist in the Go rewrite (RAW JSON-RPC
  card, Network namespace panel), and the last stale Python-facade prose.

## 1.0.0

First released version. The tool is a single static executable that acts as an EEBUS energy
manager against a device under test, with a web dashboard, a REST API, a YAML scenario runner
and built-in simulated devices.

Validated against real hardware over Wi-Fi: discovery, mutual SHIP pairing, LPC limit writes
honoured down to the device's own commanded charging current, MPC telemetry during a live
charging session, and a sustained run with **zero SHIP disconnects**.

### Fixed in this release

- **Runtime trust is now persisted.** Trusting a device through the API or dashboard was
  in-memory only, so a restart left the tool idle next to a device it had just paired with. The
  only durable route was a `peers:` entry with a literal SKI, which does not survive the device
  being re-imaged. Decisions are now stored in `trusted-skis.json` in the data directory and
  restored at startup.
- **Recovery from a rotated device certificate.** If a device presented a new certificate under
  an unchanged SKI — what happens when it is re-imaged or re-commissioned, since the SKI derives
  from the key pair — the cached certificate no longer matched and *every* subsequent connection
  was rejected with `identifier conflict`, permanently, until the process was restarted. The
  cached entry is now cleared and re-registered automatically, rate-limited per device.
- **Scenarios could run against the wrong device.** A scenario's `peer:` name was compared
  against the mDNS instance name reported by `/api/v1/peers`, which never equals the configured
  name, so the lookup always missed and silently fell back to the first peer in the list. With a
  simulator enabled that is a different device: `lpc-failsafe` asserted against the simulator
  while reporting results for the real one. The name is now resolved through the configured
  `peers:` list, and a scenario that cannot identify its target fails loudly.
- **`GET /peers/{ski}/profile` was missing `use_cases`.** Only the nested `raw_use_cases` form
  was returned, so `device-profile-discovery` asserted on a field that did not exist. Both
  shapes are now present.
- **A dial address can now be pinned, so discovery cannot redirect it to another device.**
  Discovered records are keyed by mDNS instance name and iterated in randomised order, so two
  devices announcing the *same* SHIP id break address resolution: their names collide, mDNS
  renames them repeatedly, and records end up cross-linked — one device's SKI carried by an entry
  holding another device's hostname and addresses. The dial target then changed between attempts,
  and every wrong guess burned a full TCP timeout, which was enough to exhaust the trust window.
  An IPv4 literal in a peer's `host` now fixes the address for that SKI in `mdns` mode as well as
  `static`. Found against real hardware where several units shipped the same placeholder serial.
- **A newly generated identity is now announced loudly.** The SKI comes from the key pair in the
  data directory, so starting with an empty one — which unpacking a release beside the old copy
  does — silently minted a new identity. Devices paired with the previous one then refused every
  connection with `close 4452` while their pairing UI still reported success, because the stale
  entry was still listed and still trusted. Startup now logs whether the identity was loaded or
  generated, with the SKI, and warns explicitly when it is new.
- **The shipped `eebus.yaml` was never read.** The binary defaulted to `config/eebus.yaml` while
  the release archive unpacks `eebus.yaml` beside the executable, so a bare
  `./eebus-testbench serve` matched neither and started on zero-config defaults — silently
  discarding everything in the file the archive tells you to edit, configured peers included.
  Both layouts are now resolved, `config/eebus.yaml` first.
- **A discovery blackout is now reported instead of looking like an empty network.** When no peer
  has ever been seen over mDNS, `GET /api/v1/diagnostics/network` says so in `discovery` and in
  `warnings`, the dashboard shows it, and it is logged once after 30 s. Receiving nothing and
  there being nothing to receive were previously indistinguishable — the common cause is
  multicast not being forwarded between wireless access points, which leaves unicast (and so
  SHIP itself) working perfectly while neither side ever discovers the other.
- **The announced serial now carries a SKI fragment** (`testbench-01-a1b2c3d4`). Two identities
  built from one config file were previously indistinguishable in a device's paired-device list —
  same brand, model, serial and SHIP id, differing only in the SKI, which such lists rarely show.
  Pairing the wrong row looked like success and connected nothing. Trust is keyed on the SKI, so
  this only relabels an identity and does not invalidate an existing pairing.

### Changed

- **This testbench now advertises itself as available for pairing by default,** and advertising is
  no longer tied to accepting silently. `auto_accept` defaults to **true**, which sets `register=true`
  in our mDNS record; devices commonly list only peers advertising that, so the previous default
  left the testbench absent from the device's pairing list — showing the built-in simulated device
  while hiding the energy manager, which reads as a broken tool. The new `require_approval: true`
  (or `-require-approval`) keeps the advertisement and still stops each incoming request for an
  approve/deny decision. Upstream drives both from one flag, so these were previously
  mutually exclusive.
- **EV data adds sleep mode and the full manufacturer nameplate.** `in_sleep_mode` distinguishes a
  sleeping vehicle — which reports connected while every measurement reads null — from a fault,
  and `manufacturer` now returns device name, code, serial, brand, vendor and revisions as
  separate fields instead of one string.
- **The dashboard shows its own SKI permanently,** in the sidebar, with click-to-copy. It is the
  one value a device needs in order to pair, and it previously appeared only in the startup log
  and via `GET /api/v1/identity`.
- **Light, dark, or follow the system.** The dashboard was dark only. Appearance is now a sidebar
  setting with three states; "Match system" is the default and keeps tracking the OS if it
  changes while the page is open. The choice persists per browser.
- Distribution is a per-platform zip containing the executable, a ready-to-edit `eebus.yaml`,
  the scenario library and sample scripts. Linux (amd64, arm64) and Windows (amd64).
- Docker packaging and the previous Python service have been removed; the tool is one binary.
  Python examples are retained in `examples/` as REST reference code.
- `GET /api/v1/version` now reports the version stamped at release build time instead of a
  hardcoded string. A locally built binary reports `dev`.

## Known issues

### In this tool

- **Only one device of a model family can be driven at a time** when several report the same
  SPINE device address. SPINE keys remote devices by that address, so the second device's
  entities are shadowed and all of its LPC/MPC reads fail with *"no entity supporting this use
  case"* even though its use cases are listed. Workaround: keep one such device connected and
  set the others to `trust: manual`.
- **Measurement scenarios fail rather than skip when there is nothing to measure.**
  `mpc-live-power`, `mpc-electrical-quality` and `ev-charging-measurements` assert non-null
  readings, so with no vehicle connected — or against a device that reports only total power and
  no per-phase detail — they report `failed` when `skipped` is the honest result
  (`expected a non-null value`, `expected length > 0`). The requirement should be capability- and
  session-gated the way the other scenarios are. Run them during an active charging session.
- **Test coverage is uneven.** `eebusgo`, `identity`, `netfilter`, `scenario`, `staticmdns` and
  `truststore` have unit tests; `config`, `httpapi`, `simulator` and `announce` do not.
- **Release binaries are unsigned.** Windows SmartScreen will warn on first run and some
  endpoint-protection agents will hold the download for scanning.
- **No OPEV, OSCEV or OHPCF**, not even through raw RPC — `eebus-go` does not implement them at
  the pinned revision, so there is no local feature to send from. This matters mainly for OPEV's
  EV current-limit write, which some other tools offer; it would have to be built directly on
  SPINE `LoadControl`. See `docs/20-use-case-matrix.md`.
- **Discovery needs multicast to reach this host.** The tool reports a blackout but cannot repair
  one. On a multi-AP wireless network, put the host and the device on the same access point, or
  pin the device with a `peers:` entry, which does not use discovery at all.
- **`.local` hostnames never resolve, on any host.** The release binaries are built with
  `CGO_ENABLED=0` so that they run anywhere without runtime dependencies, and that selects Go's
  own resolver, which does not consult NSS — so `nss-mdns`/`libnss-mdns` is bypassed however it is
  configured, and installing it changes nothing. (An earlier version of these notes claimed it
  helped; it does not.) A hostname is dialled before the addresses, so each attempt spends one
  failed lookup before falling back to the IP addresses from the mDNS record, which do work.
  Networks whose DNS answers the search-domain form (`<host>.local.<domain>`) with an unrelated
  record hit the same path. Cost is a fast failure per attempt, not a hang.

### Device behaviour worth knowing (not tool defects)

These were observed during validation. The tool reports what the device sends; each of these
is the device's own behaviour.

- **A limit may be reported back late.** A device can accept and apply a limit while still
  reporting the previous value for several seconds, so a read immediately after a write can look
  like a failed write. Confirm against the device's own view where possible.
- **All-null MPC values mean the device is not reporting measurements**, normally because no
  charging session is active — not a parsing failure.
- **A large limit step-down may abort power transfer instead of throttling.** One device dropped
  from 11 kW to standby when the limit was cut by roughly 60 % in a single step, and did not
  resume when the limit was raised again; the session had to be restarted. Step limits down
  gradually if the device is sensitive to this.
- **A device may enforce a limit correctly while reporting `is_active: false`,** or continue
  enforcing a limit whose duration has expired. Treat the device's own enforced value as
  authoritative over its EEBUS report when the two disagree.
