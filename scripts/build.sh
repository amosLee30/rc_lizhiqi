#!/usr/bin/env bash
# Formal build script for the notification gateway service.
#
# Usage:
#   scripts/build.sh                 # build for the host platform -> bin/notify-server
#   GOOS=linux GOARCH=amd64 scripts/build.sh   # cross-compile
#   OUT=bin/gw scripts/build.sh      # custom output path
#
# Set SKIP_CHECKS=1 to skip vet/test (faster, not recommended for releases).
set -euo pipefail
cd "$(dirname "$0")/.."

PKG="./cmd/server"
OUT="${OUT:-bin/notify-server}"

# Build metadata stamped into the binary via -ldflags.
VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo none)"
BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
LDFLAGS="-s -w \
  -X main.Version=${VERSION} \
  -X main.Commit=${COMMIT} \
  -X main.BuildTime=${BUILD_TIME}"

echo "==> go mod download"
go mod download

if [ "${SKIP_CHECKS:-0}" != "1" ]; then
  echo "==> gofmt check"
  unformatted="$(gofmt -l ./cmd ./internal)"
  if [ -n "$unformatted" ]; then
    echo "ERROR: the following files are not gofmt-clean:" >&2
    echo "$unformatted" >&2
    exit 1
  fi
  echo "==> go vet"
  go vet ./...
  echo "==> go test"
  go test ./...
fi

echo "==> building ${OUT} (version=${VERSION} commit=${COMMIT} os=${GOOS:-$(go env GOOS)} arch=${GOARCH:-$(go env GOARCH)})"
mkdir -p "$(dirname "$OUT")"
CGO_ENABLED=0 go build -trimpath -ldflags "$LDFLAGS" -o "$OUT" "$PKG"

echo "==> done: $OUT"
ls -lh "$OUT"
