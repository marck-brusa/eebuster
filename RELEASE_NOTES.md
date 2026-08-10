# Release notes

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
