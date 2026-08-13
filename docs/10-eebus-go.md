# Using eebus-go

The EEBUS stack is [enbility/eebus-go](https://github.com/enbility/eebus-go) with
[ship-go](https://github.com/enbility/ship-go) and
[spine-go](https://github.com/enbility/spine-go), linked directly into the binary and vendored
at the revisions pinned in `go.mod`. `internal/eebusgo` is the only package that talks to them;
everything else goes through its types.

There is no separate process and no inter-process control plane. An earlier design drove a
patched upstream example over local JSON-RPC, and most of the bugs found in practice were
artefacts of that boundary — argument-arity mismatches, a reflection panic that killed the whole
counterparty on an unknown method name, and duplicated state. Linking the library removes the
class of problem entirely.

## The one local patch

`vendor/github.com/enbility/eebus-go/service/service.go` gains one method:

```go
func (s *Service) SetMdnsProvider(provider shipapi.MdnsProviderInterface) error
```

`Service.Setup()` builds its mDNS manager internally and stores it behind an interface that does
not expose `SetMdnsProvider`, so static-mode provider injection is unreachable from outside the
package. The patch is a passthrough to the concrete manager's own method.

This is the **only** modification in the whole vendor tree. `go mod vendor` reverts it silently,
so use `scripts/vendor.sh`, which re-runs it from
`patches/eebus-go-service-mdns-hook.patch`. When changing a pinned revision, refresh through
that script and re-run the test suite.

## The raw-frame tap

The Message trace and the conformance checker need every SHIP frame exactly as it crossed the
wire. Downstream of ship-go's `parseMessage`, payloads have already been through byte-level
"repairs" (`JsonFromEEBUSJson` rewrites `[{`/`},{`/`}]`/`[]` unconditionally, including inside
string literals), which destroys precisely the evidence a conformance check needs. The one
honest observation point is ship-go's websocket layer, which logs each frame as

```go
logging.Log().Trace("Send:", w.remoteSki, text)   // ws/websocket.go, and "Recv:" likewise
```

`internal/eebusgo.StdLogger.Trace` recognises exactly that three-argument shape and hands the
raw frame to the trace store — before the console level filter, so capture works at any log
verbosity, and **without any vendor patch**. The shape is pinned by
`TestVendoredFrameLogShape`, so a vendored ship-go upgrade that changes the call turns silent
frame loss into a test failure.

## Registered use cases

`internal/eebusgo` registers the client (energy-manager) side of:

- **LPC, LPP** — consumption and production limits, failsafe values, nominal maximum, heartbeat;
- **MPC, MGCP** — power measurements and grid connection point;
- **EV, PV, battery** — vehicle, generation and storage data when advertised.

Registration only makes the call surface available. The peer must still advertise a matching use
case for a call to resolve.

## Entity resolution

Typed calls select an entity by *advertised* use-case name, taken from live discovery — never
from our own registration list. If several entities advertise the same use case, the API returns
`409` with the candidates and the caller must disambiguate with `?entity=`.

Entity `[0]` is node management and is never selected as a use-case entity.

## Discovery modes

`mdns` uses real multicast discovery. `static` composes the same real discovery with synthetic
records built from the configured `peers:` list, for a device multicast cannot reach; it replaces
discovery only, not SHIP or SPINE. Both modes require an explicit interface, or `*`.

## Upstream behaviours worth knowing

- Incoming pairing is denied unless the application either auto-accepts or declares that a user
  can decide, so one of the two is always enabled before discovery starts.
- `SetAutoAccept` controls two unrelated things at once: whether an incoming request is accepted
  without a human decision, *and* the `register` flag in our announced mDNS TXT. Since devices
  commonly list only peers advertising `register=true`, that coupling forces a choice between
  being listed at all and testing the approval dialogue. `internal/eebusgo` therefore drives
  acceptance through the upstream setter and re-asserts the advertisement through our own
  announcer (`announce.Provider.SetAdvertiseRegister`), which owns the TXT record it publishes.
  See `config.Config.RequireApproval`.
- `RegisterRemoteService` is what makes the stack dial a SKI. Being in the static peer list only
  governs whether an *incoming* connection is accepted, so a `trust: auto` peer still needs an
  explicit registration at startup.
- The hub caches a service's SKI together with its certificate fingerprint and rejects a
  mismatch as an `identifier conflict`, with no API to clear it. `UnregisterRemoteService`
  removes the cached entry, which is how a re-issued certificate is recovered from.
- Dial-level failures never reach the SHIP or SPINE layer, so nothing surfaces them through an
  API. `internal/eebusgo/trustwatch.go` scrapes them from the logger, which is indirect but the
  only place the concrete error exists.
- Discovered entries are keyed by mDNS instance name, and the hub iterates that map in Go's
  randomised order. Two devices announcing the *same* SHIP id therefore break address
  resolution: their instance names collide, mDNS renames them in a loop, and records end up
  cross-linked, so one device's SKI is carried by an entry holding another device's hostname and
  addresses. The dial target for that SKI then changes between attempts. Pin it with an IPv4
  `host` on the peer (see below).
- The hostname is dialled before the addresses. A `.local` hostname never resolves in these
  builds: `CGO_ENABLED=0` selects Go's own resolver, which does not consult NSS, so `nss-mdns`
  is bypassed however it is configured. Corporate DNS may also answer the search-domain form
  (`<host>.local.<domain>`) with an unrelated record. Both fail fast, then the addresses are
  tried, so this costs one failed lookup per attempt rather than a hang.

## Pinning a dial address

`ServiceDetails.IPv4()` makes the hub replace a discovered entry's whole address list before
dialling. eebus-go does not expose the setter, but `Service.RemoteServiceFor` returns the live
`*ServiceDetails`, so `internal/eebusgo` sets it directly — no addition to the vendor patch.

An IPv4 literal in a configured peer's `host` becomes that pin, in `mdns` mode as well as
`static`. It applies from the next `Trust()` on, including the re-`Trust()` used to recover from
an identifier conflict, and a config reload refreshes it. This is the supported fix when several
devices on one network share an announced identity; it does not suppress the preceding hostname
attempt.
