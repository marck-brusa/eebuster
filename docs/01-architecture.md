# Architecture

## Process model

One process. The executable contains the EEBUS stack, the REST API, the dashboard, the scenario
runner, the wire trace with its conformance checker, and the simulated devices; there is no
supervisor and no container. The single optional child process is the bundled EEBusTracer
(a separate analyzer with its own web UI), which `serve` spawns when its binary sits next to
the executable — never required, and its failure never affects the testbench.

```
Browser / curl / CI
        |
        v
REST API + dashboard        (internal/httpapi, internal/webui)
        |
        v
EEBUS stack                 (internal/eebusgo, on vendored eebus-go)
   |          |          |
   |          |          `-- raw-frame tap -> trace store + conformance (internal/trace, internal/conformance)
   |          `-- simulated devices, each its own SHIP endpoint (internal/simulator)
   v
SHIP :4712 / mDNS _ship._tcp          [optional child: eebustracer web UI on :8090]
```

The stack plays the energy-manager (CEM) side: it registers the client half of the use cases,
so it writes limits and reads measurements. Simulated devices are separate SHIP endpoints in the
same process, each with its own identity and port, which is why the tool can exercise a full
pairing handshake with nothing else on the network.

The API port and the SHIP port are independent. Only SHIP needs to be reachable **inbound** from
the device; the API port is for the operator.

## Network

Discovery is real multicast mDNS (`_ship._tcp`) in both network modes. `static` mode
additionally injects the configured `peers:` entries as synthetic records, for a device that
multicast cannot reach — it still uses ordinary SHIP and SPINE to talk to it.

The host needs a layer-2 path to the device. This rules out network-namespace environments that
NAT their traffic (see QUICKSTART.md's WSL2 note): multicast never reaches the physical segment
and inbound connections do not arrive.

### What gets announced

A peer that discovers us reads our addresses out of the mDNS record and dials them **one at a
time**, waiting out a full TCP connect timeout on each unreachable one. Publishing every local
address is therefore actively harmful on a machine with VPN, container or hypervisor adapters.

`internal/netfilter` drops what no peer can route to — IPv6 link-local (which loses its scope
zone in a DNS record), IPv6 unique-local, IPv4 link-local, and virtual adapter ranges —
and `internal/announce` decides the final set. Both the kept and the dropped addresses are
logged with reasons at startup, because the resulting failure is otherwise only visible in the
*device's* log. `network.announce_addresses` overrides the decision entirely.

Pin `network.interface` on a multi-homed host. Announcing on the wrong link is a common reason a
device never sees the tool at all.

## Identity and trust

The certificate and private key live under the data directory. Its default is `./data` when
that already exists in the working directory (the development layout), and otherwise `data/`
**next to the executable** — so where a run happens to be started from can never silently
change the identity. `-data-dir` overrides both. The SKI is derived from the public key, so
**replacing the certificate changes the SKI and invalidates every existing pairing.** Identity
creation is idempotent and refuses to overwrite existing material, and startup prints the
resolved data directory.

**The data directory is not scratch space — keep it.** An empty one makes the tool generate a
new identity, and unpacking a new release beside the old one is enough to do that by accident.
Startup logs whether the identity was loaded or generated, together with the SKI, and warns
explicitly when it is new. Either carry the directory forward or pass `-data-dir` pointing at
the existing one.

The announced serial carries a fragment of the SKI (`testbench-01-a1b2c3d4`). Trust is keyed on
the SKI, but a device's paired-device list shows the announced brand, model and serial — so two
identities built from one config file would otherwise appear as rows identical in every visible
field, and pairing the wrong one looks like success while nothing connects. The fragment only
relabels an identity; it does not affect pairing.

Trust is mutual and has two independent halves:

- **We trust the device** — declaratively via `peers:` with `trust: auto`, or at runtime through
  `POST /api/v1/peers/{ski}/trust`. Runtime decisions are persisted by `internal/truststore` in
  the data directory and restored at startup, so they survive a restart without a config edit.
  Additionally, **every peer that completes a connection is persisted** — including pairings the
  device initiated itself, which are auto-accepted here and never pass through the trust API.
  Without that, a testbench restart left reconnection entirely to the device's retry policy.
- **The device trusts us** — done with the device's own pairing control. Without it the device
  rejects the connection at the SHIP layer (`close 4452`) and the tool retries indefinitely. We
  advertise `register=true` by default so the device offers us in that control at all; many list
  nothing else. `require_approval: true` keeps the advertisement while holding each incoming
  request for an approve/deny decision.

A device that is re-imaged may present a new certificate under an unchanged SKI. Identity is
anchored on the SKI, so the cached certificate is cleared and re-registered rather than
rejecting the device permanently.

## Configuration

One file, `eebus.yaml`, read at startup; `POST /api/v1/config/reload` re-reads it. Candidates
are tried in the working directory first (`config/eebus.yaml`, `eebus.yaml`), then next to the
executable — the release layout — and startup logs which file was loaded, or an unmissable
warning when none was found. The tool runs with no file at all, defaulting to mDNS on every
interface with one simulated device.

```yaml
identity:      # vendor code, brand, model, serial -> what we announce
api:           # bind address and port for dashboard + REST
network:       # mode, interface, announcement rules
peers:         # devices declared up front
simulator:     # built-in simulated devices
stacks:        # SHIP port
tracer_url:    # link a self-managed EEBusTracer instead of spawning the bundled one
```

The API may change only `stacks.<id>.enabled`. Identity, network and peers stay hand-managed:
they determine trust relationships, and silently rewriting them from a web request would break
pairings from under the operator.

## Data and events

The dashboard polls a best-effort energy snapshot for the selected peer every five seconds.
Partial data is normal and returned with explicit per-method errors rather than failing whole.
Samples are held in memory for up to 12 hours and are lost on restart — this is a test tool, not
a historian.

Events are normalised into three topics:

- `lifecycle` — connection, pairing, discovery and trust state;
- `usecase` — semantic use-case notifications;
- `spine` — a frame failed the conformance checks (the full flow lives in the trace, this is
  the attention signal).

Separately from events, every raw SHIP frame both directions is kept in a bounded in-memory
trace (`internal/trace`, ~2000 frames), captured at the websocket layer before the vendored
stack's JSON repairs and annotated with conformance findings — see the Message trace page and
`GET /api/v1/trace`. With `-frame-log` (or automatically when the bundled EEBusTracer runs)
the frames are additionally appended to a file for offline analysis.

A bounded recent-event buffer is exposed both as a snapshot and as a Server-Sent Events stream.
Do not add response-compression middleware to that stream; it breaks it.

Log output goes to the console and to an in-memory ring buffer that the dashboard's diagnostics
view and `GET /api/v1/stacks/{id}/logs` read from.
