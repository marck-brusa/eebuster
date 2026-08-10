#!/usr/bin/env bash
# Regenerates vendor/ and reapplies the one patch eebus-go still needs (static-mode mDNS
# injection -- see patches/eebus-go-service-mdns-hook.patch and docs/ for why it can't be
# avoided: Service.Setup() builds the mDNS manager internally with no exported hook).
#
# `go mod vendor` always wins over existing vendor/ contents, so the patch must be reapplied
# every time, not just once -- run this instead of `go mod vendor` directly.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

go mod vendor
patch -p1 -d vendor/github.com/enbility/eebus-go < patches/eebus-go-service-mdns-hook.patch
