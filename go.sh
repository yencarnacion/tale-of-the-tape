#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
mode="${1:-serve}"
if [[ $# -gt 0 ]]; then shift; fi
export GOCACHE="${TMPDIR:-/tmp}/tale-of-the-tape-go-cache"
export GOMODCACHE="${TMPDIR:-/tmp}/tale-of-the-tape-go-modcache"
exec go run -buildvcs=false ./cmd/tale-of-the-tape "$mode" "$@"
