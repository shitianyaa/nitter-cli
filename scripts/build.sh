#!/usr/bin/env sh
set -eu
cd "$(dirname "$0")/.."
VERSION="${VERSION:-dev}"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
CGO_ENABLED=0 go build -trimpath -buildvcs=false \
  -ldflags "-s -w -X github.com/shitianyaa/twitter-cli/internal/buildinfo.Version=${VERSION} \
  -X github.com/shitianyaa/twitter-cli/internal/buildinfo.Commit=${COMMIT} \
  -X github.com/shitianyaa/twitter-cli/internal/buildinfo.BuildDate=${BUILD_DATE}" \
  -o twitter ./cmd/twitter
