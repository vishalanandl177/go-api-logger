#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
race=()
modules=(. storage integrations cmd examples)
for argument in "$@"; do
  case "$argument" in
    --race) race=(-race) ;;
    --portable) modules=(. storage cmd examples) ;;
    *) echo 'Usage: bash scripts/verify.sh [--race] [--portable]' >&2; exit 2 ;;
  esac
done
for module in "${modules[@]}"; do
  (
    cd "$module"
    go test "${race[@]}" -count=1 ./...
    go vet ./...
    go build ./...
  )
done
