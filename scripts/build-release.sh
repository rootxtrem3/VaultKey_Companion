#!/usr/bin/env bash
# Build release binaries and SHA-256 checksums. No network access or cgo is
# required. Set VERSION, COMMIT, BUILD_DATE, or OUTPUT to override defaults.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"

VERSION=${VERSION:-0.1.0}
COMMIT=${COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo unknown)}
BUILD_DATE=${BUILD_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}
OUTPUT=${OUTPUT:-"$ROOT/dist"}

LDFLAGS="-s -w -X main.version=$VERSION -X main.commit=$COMMIT -X main.buildDate=$BUILD_DATE"
TARGETS=(
  linux/amd64
  linux/arm64
  darwin/amd64
  darwin/arm64
  windows/amd64
  windows/arm64
)

mkdir -p "$OUTPUT"
echo "building VaultKey companion $VERSION (commit $COMMIT, $BUILD_DATE)"

for target in "${TARGETS[@]}"; do
  os=${target%/*}
  arch=${target#*/}
  suffix=""
  if [ "$os" = "windows" ]; then
    suffix=".exe"
  fi
  name="vaultkey_${VERSION}_${os}_${arch}${suffix}"
  echo "  $name"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    go build -trimpath -ldflags "$LDFLAGS" -o "$OUTPUT/$name" ./cmd/vaultkey
done

(
  cd "$OUTPUT"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum vaultkey_* > SHA256SUMS
  else
    shasum -a 256 vaultkey_* > SHA256SUMS
  fi
)

echo "artifacts in $OUTPUT"
ls -lh "$OUTPUT"
