#!/usr/bin/env bash
# Regenerates vendor/ and reapplies the local patches (docs/10-eebus-go.md says why each one
# can't be avoided):
#   - eebus-go: static-mode mDNS injection (patches/eebus-go-service-mdns-hook.patch).
#     Service.Setup() builds the mDNS manager internally with no exported hook.
#   - ship-go: TLS 1.2 on P-256 only, SHIP 9 (patches/ship-go-tls12-only.patch). Without it
#     crypto/tls offers TLS 1.3 and X25519 to every peer.
#
# `go mod vendor` always wins over existing vendor/ contents, so the patches must be reapplied
# every time, not just once -- run this instead of `go mod vendor` directly.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

go mod vendor
patch -p1 -d vendor/github.com/enbility/eebus-go < patches/eebus-go-service-mdns-hook.patch
patch -p1 -d vendor/github.com/enbility/ship-go < patches/ship-go-tls12-only.patch
