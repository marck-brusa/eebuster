#!/usr/bin/env bash
# Build release-shaped archives for every supported platform into dist/, with the same layout
# and naming as .github/workflows/release.yml produces -- for building on a machine without
# GitHub access, or for a local pre-release check.
#
#   scripts/build-dist.sh [version]     version defaults to `git describe`, without the v
#   WITH_TRACER=0 scripts/build-dist.sh to skip bundling EEBusTracer (needs network to clone)
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION="${1:-$(git describe --tags --always --dirty | sed 's/^v//')}"
TRACER_REPO="https://github.com/uhl/EEBusTracer.git"
TRACER_COMMIT="e7cdb4a" # v0.7.0 -- keep in sync with .github/workflows/release.yml
WITH_TRACER="${WITH_TRACER:-1}"

targets=("linux amd64" "windows amd64 .exe" "darwin arm64")

rm -rf dist
mkdir -p dist

tracer_src=""
if [ "$WITH_TRACER" = "1" ]; then
  tracer_src="$(mktemp -d)"
  trap 'rm -rf "$tracer_src"' EXIT
  git clone --quiet "$TRACER_REPO" "$tracer_src"
  git -C "$tracer_src" checkout --quiet "$TRACER_COMMIT"
fi

for target in "${targets[@]}"; do
  # shellcheck disable=SC2086
  set -- $target
  goos=$1 goarch=$2 ext=${3:-}
  name="eebus-testbench-${VERSION}-${goos}-${goarch}"
  dir="dist/$name"
  mkdir -p "$dir/scenarios" "$dir/examples"

  CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch go build -mod=vendor -trimpath \
    -ldflags "-s -w -X github.com/marck-brusa/eebuster/internal/httpapi.Version=${VERSION}" \
    -o "$dir/eebus-testbench${ext}" ./cmd/eebus-testbench

  if [ -n "$tracer_src" ]; then
    (cd "$tracer_src" && CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch go build -trimpath \
      -ldflags "-s -w" -o "$OLDPWD/$dir/eebustracer${ext}" ./cmd/eebustracer)
    cp "$tracer_src/LICENSE" "$dir/EEBUSTRACER-LICENSE"
  fi

  cp QUICKSTART.md README.md "$dir/"
  # Shipped as eebus.yaml, ready to edit -- the binary looks for exactly this name.
  cp config/eebus.example.yaml "$dir/eebus.yaml"
  cp scenarios/*.yaml "$dir/scenarios/"
  cp examples/lpc-set-limit.sh examples/lpc-set-limit.ps1 \
     examples/mpc-watch.sh examples/mpc-watch.ps1 \
     examples/README.md "$dir/examples/"

  # zip when available (the GitHub runner has it); otherwise Python, taking care to carry the
  # unix mode bits into the archive -- a plain `python3 -m zipfile` would strip the exec bits
  # and the unpacked binaries would not run.
  if command -v zip >/dev/null; then
    (cd dist && zip -qr "$name.zip" "$name")
  else
    (cd dist && python3 - "$name" <<'PY'
import os, sys, zipfile
name = sys.argv[1]
with zipfile.ZipFile(f"{name}.zip", "w", zipfile.ZIP_DEFLATED) as zf:
    for root, _, files in os.walk(name):
        for f in sorted(files):
            path = os.path.join(root, f)
            info = zipfile.ZipInfo(path)
            info.external_attr = (os.stat(path).st_mode & 0xFFFF) << 16
            info.compress_type = zipfile.ZIP_DEFLATED
            with open(path, "rb") as fh:
                zf.writestr(info, fh.read())
PY
    )
  fi
  rm -rf "$dir"
  echo "built dist/$name.zip"
done

(cd dist && sha256sum ./*.zip > SHA256SUMS.txt && cat SHA256SUMS.txt)
