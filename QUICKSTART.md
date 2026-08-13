# Quick start

The testbench is a single self-contained executable. It acts as an EEBUS energy manager (CEM)
against a device under test: it discovers devices over mDNS, completes SHIP pairing, applies
LPC/LPP limits, reads MPC/MGCP measurements, and serves a dashboard and REST API on one port.
There is no runtime to install, no container, and no configuration required to start.

## 1. Install

Download the archive for your platform from the
[releases page](https://github.com/marck-brusa/eebuster/releases) and unpack it:

| File | Platform |
| --- | --- |
| `eebus-testbench-<version>-windows-amd64.zip` | Windows 10/11, 64-bit |
| `eebus-testbench-<version>-linux-amd64.zip` | Linux, x86-64 |
| `eebus-testbench-<version>-darwin-arm64.zip` | macOS, Apple Silicon |

Each archive contains the executable, a ready-to-edit `eebus.yaml`, the scenario library, the
sample scripts in `examples/`, and the bundled `eebustracer` protocol analyzer. Verify the
download against `SHA256SUMS.txt` if required.

To build from source instead (Go 1.24 or newer):

```bash
git clone https://github.com/marck-brusa/eebuster.git
cd eebuster
go build -mod=vendor -o eebus-testbench ./cmd/eebus-testbench
```

Always build with `-mod=vendor`. The committed `vendor/` tree carries a required patch to
`eebus-go` that a module-mode build silently discards; use `scripts/vendor.sh` rather than
`go mod vendor` when refreshing dependencies.

## 2. First run

```bash
./eebus-testbench serve
```

The launch directory does not matter: the tool finds `eebus.yaml`, the data directory and
`scenarios/` next to its own executable when the working directory has none, and the first
log lines state exactly which config file and data directory it resolved. With no
configuration file present it starts in real mDNS discovery mode on every usable interface,
with one simulated device enabled. Open <http://127.0.0.1:8080/ui>. You should see the
simulated device connected, reporting 11 kW — and asymmetrically, on two of three phases,
which the dashboard's Phase balance panel flags. The bundled EEBusTracer starts alongside on
<http://127.0.0.1:8090> and is linked from the sidebar.

This first run is worth doing before touching real hardware: it confirms the executable runs,
the dashboard loads, and the API answers, with no network or device variables involved.

Apply a limit to the simulated device and watch its reported power follow:

```bash
curl http://127.0.0.1:8080/api/v1/peers          # copy the simulated device's ski
./examples/lpc-set-limit.sh <ski> 4000           # reported power drops to 4000 W
./examples/lpc-set-limit.sh <ski> 0 release      # returns to 11000 W
```

## 3. Configure for a real device

Copy the shipped `eebus.yaml` (or `config/eebus.example.yaml` in a source checkout) and edit
it. The settings that matter in practice:

```yaml
api:
  port: 8080              # move this if a local policy blocks 8080

network:
  mode: mdns              # real multicast discovery
  interface: "wlan0"      # pin to the interface facing the device; "*" for all
  announce_addresses:     # see "Multi-homed hosts" below -- publish only reachable addresses
    - 192.168.1.50

peers:
  - name: device-under-test
    ski: "<40 hex characters>"
    host: 192.168.1.20
    port: 12345           # the device's SHIP port, not necessarily 4712
    trust: auto           # trusted at startup, so it survives a restart

simulator:
  enabled: false          # off once you are testing real hardware
```

Then simply:

```bash
./eebus-testbench serve
```

(the file next to the executable is found automatically; `-config` overrides it).

Note the SKI the tool prints at startup; the device needs it. It is also shown permanently in the
dashboard sidebar under **This testbench's SKI**, where clicking it copies it. The identity is persisted under
the data directory (by default `data/` next to the executable; `-data-dir` overrides) — keep
it, because deleting it generates a new SKI and invalidates the pairing you are about to set
up. Startup says which happened:

```
identity: loaded from data/identity -- ski <ski>
```

**When upgrading, carry the data directory across** (or point `-data-dir` at the old one).
Unpacking a new release next to the old copy starts with an empty one, which mints a new
identity; the tool warns when that happens.

## 4. Pair with the device

**Pairing is mutual.** Two independent trust decisions must both be made, and forgetting the
second is the most common reason a device is discovered but never connects:

1. **The tool must trust the device.** Either list it under `peers:` with `trust: auto`, or
   click Trust in the dashboard (equivalently `POST /api/v1/peers/{ski}/trust`) — or simply
   pair from the device's side: an incoming pairing is accepted automatically (unless
   `require_approval` is set). All three persist across a restart: every peer that completes a
   connection is stored in the data directory and re-dialled at the next start.
2. **The device must trust the tool.** Use whatever pairing control the device provides —
   typically a Trust or Pair button on its own web interface, or an equivalent call on its REST
   API, given the tool's SKI.

Until both sides trust each other the device rejects the connection at the SHIP layer and the
tool retries indefinitely. The log makes it explicit:

```
websocket: close 4452: Node rejected by application
ship: pairing state for ski <ski> -> remote denied trust
```

That message means step 2 has not been completed. It is not a network fault.

It also appears when step 2 was completed against a **different identity of this tool**. If the
device lists two entries that look identical, compare their SKIs against the one printed at
startup and pair the matching one — a device will report pairing an entry it already trusts as
successful, so the click gives no indication that it changed nothing.

Confirm success:

```bash
curl http://127.0.0.1:8080/api/v1/peers
# "connected": true, "trusted": true
curl http://127.0.0.1:8080/api/v1/peers/<ski>/usecases
```

## 5. Drive limits and read measurements

```bash
./examples/lpc-set-limit.sh <ski> 6000          # 6 kW, no expiry
./examples/lpc-set-limit.sh <ski> 6000 PT2H     # 6 kW for two hours
./examples/lpc-set-limit.sh <ski> 0 release     # clear the limit
./examples/mpc-watch.sh <ski> 5                 # poll measurements every 5 s
```

On Windows, use the PowerShell equivalents:

```powershell
.\examples\lpc-set-limit.ps1 -Ski <ski> -Watts 6000 -Duration PT2H
.\examples\mpc-watch.ps1 -Ski <ski> -IntervalSeconds 5
```

Two behaviours to expect from real devices, neither of which is a fault in the tool:

- **A device may report a limit back a few seconds late,** or briefly report the previous
  value, even though it accepted and applied the new one. Give it a moment before concluding a
  write failed, and confirm against the device's own view where you can.
- **All-null MPC values mean the device is not reporting measurements,** normally because no
  charging session is active. The tool reports exactly what the device sent.

## 6. Run the scenario library

```bash
curl http://127.0.0.1:8080/api/v1/scenarios                    # list
curl -X POST http://127.0.0.1:8080/api/v1/scenarios/lpc-basic-limit/run
curl -X POST http://127.0.0.1:8080/api/v1/scenarios/run-all
```

Scenarios target the peer named `device-under-test` in your configuration. A scenario is
skipped when the device does not advertise the use case it needs, which is normal and distinct
from a failure. Results are also available as JUnit XML for CI.

## 7. Inspect the wire

The dashboard's **Message trace** page shows every SHIP frame in both directions, checked
against the EEBUS encoding rules — structurally broken JSON from a device (the classic
"arrays where objects are expected") is flagged with the exact rule and the standard section
it violates, *before* the stack's own repairs can mask it. The conformance summary aggregates
findings by rule and jumps to the offending frame.

For offline deep dives, every frame is also appended to `data/frames.log`; import it into the
bundled analyzer with `./eebustracer import frames.log` and browse at its UI, or run
`./eebustracer analyze frames.log` for a quick terminal audit.

## Platform notes

### SHIP needs an inbound TCP port

Discovery is multicast UDP and works even when TCP is blocked. Pairing then fails, and the
only record of it is in the *device's* log, not the tool's. The symptom is a device that lists
the tool happily and never connects.

The tool prints the commands for your platform:

```bash
./eebus-testbench firewall
```

On Linux this is typically `sudo ufw allow 4712/tcp`. On Windows it needs a rule for inbound
TCP 4712, which usually requires an administrator.

### Windows and managed corporate laptops

Expect friction, and budget time for it. In practice this environment causes more lost hours
than the EEBUS protocol work itself.

- **Inbound firewall rules may be centrally enforced.** On a managed device the local firewall
  is often policy-controlled, so an administrator prompt is not enough and the rule silently
  fails to apply or is reverted at the next policy refresh. Verify from another machine that
  TCP 4712 is genuinely reachable rather than assuming the rule took effect.
- **Common dashboard ports are frequently blocked.** If `http://<host>:8080/ui` is
  unreachable from another machine while `127.0.0.1:8080` works locally, the port is filtered.
  Move it with `api.port` — the port is entirely your choice.
- **Endpoint protection may quarantine or delay an unsigned executable.** The release binaries
  are not code-signed, so SmartScreen will warn on first run and some agents will hold the
  file for scanning. If the tool refuses to start or vanishes after download, check the
  security console before debugging the tool.
- **Corporate DNS and proxies interfere with `.local` name resolution.** The tool falls back
  to the IP addresses from the mDNS record, so this costs a failed lookup per connection
  attempt rather than breaking pairing outright.

If a laptop cannot be made to work within a reasonable time, a small native Linux machine on
the same network is the pragmatic escape hatch and removes every item above at once.

### Multi-homed hosts

VPN clients, Docker Desktop, Hyper-V and WSL each add virtual adapters. By default a peer
receives every one of your local addresses in the mDNS record and dials them **one at a time**,
waiting out a full TCP timeout on each unreachable one. A host with eight addresses of which
one is reachable can therefore take over a minute per connection attempt.

The tool filters obviously unroutable addresses automatically. When in doubt, be explicit:

```yaml
network:
  interface: "eth0"
  announce_addresses: ["192.168.1.50"]
```

or `--announce-address 192.168.1.50`. Pin the interface too; announcing on the wrong link is a
common reason a device never sees the tool at all.

### WSL2 is not recommended for real hardware

WSL2 runs in a NAT'd network namespace with no layer-2 path to the LAN. Consequently:

- multicast mDNS does not reach the physical network, so real discovery does not work;
- inbound connections to the SHIP port do not arrive without `netsh interface portproxy`
  forwarding plus a matching Windows firewall rule;
- the WSL adapter's address is published to peers, where it is unreachable and simply burns a
  connection timeout.

Mirrored networking mode in recent Windows builds improves this, but it is not universally
available or permitted by policy. **Use the native Windows executable, or a native Linux
host.** WSL2 is fine for building and for the simulator; it is not a supported configuration
for testing against real devices.

### Running only one device of a kind

Some products report an identical SPINE device address across units. When two such devices are
connected at once, the second one's entities are shadowed and all of its LPC/MPC reads fail
with "no entity supporting this use case" even though its use cases are listed. Keep a single
device of that kind connected — set the others to `trust: manual`.

## Troubleshooting

| Symptom | Cause |
| --- | --- |
| `close 4452: Node rejected by application` | The device does not trust the tool. Complete step 4.2 — and check it was done against the SKI printed at startup, not an older identity. |
| Device visible, never connects | Inbound TCP on the SHIP port is blocked. Run `./eebus-testbench firewall`. |
| `trying to connect to <ski> at <another device's address>` | Two devices announce the same SHIP id, so discovery cross-linked their records. Pin the address: give the device a `peers:` entry with its IP as `host`. |
| A device's scan still lists the testbench although it is not running | A clean shutdown (Ctrl+C) sends mDNS goodbyes; a hard kill (closed console window) cannot, so the record lingers in peers' caches until its TTL expires, and some device stacks additionally cache scan results. Harmless as long as the identity is stable: restart the testbench with the same data directory and the stale record points at the current instance again. |
| Pairing "breaks" after restarting the testbench | Almost always a changed identity: the SKI comes from the data directory, and if that moves, every pairing is void. The startup log prints the resolved data directory and warns loudly when a NEW identity was generated. Defaults now anchor next to the executable, so the launch directory cannot silently change the identity. |
| `identifier conflict on fingerprint` | The device presented a new certificate under an existing SKI, typically after re-imaging. Cleared and re-registered automatically; no restart needed. |
| `lookup <host>.local ... server misbehaving` | Expected. `.local` names never resolve in these builds; the IP addresses from the mDNS record are used instead. |
| MPC all null | The device is not reporting measurements, usually no active session. |
| `no entity supporting this use case` | Either the use case is not advertised, or a second device with the same SPINE device address is connected. |
| Dashboard unreachable from another host | Port filtered locally; change `api.port`. |
| Nothing discovered at all | Check `discovery` in `GET /api/v1/diagnostics/network`. `ever_seen: false` means no multicast is reaching this host — most often the host and the device are on different wireless access points, which blocks multicast while leaving unicast working. Put both on the same AP, or add a `peers:` entry with the device's IP, which needs no discovery. Also check `network.interface` and the WSL2 note above. |

Increase detail with `-log-level trace`, which dumps every SPINE datagram as JSON. Recent log
lines are also available at `GET /api/v1/stacks/{id}/logs` and in the dashboard.
