#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
repo_root=$(pwd -P)
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
  module_name="$module"
  if [[ "$module" == '.' ]]; then module_name='root'; fi
  build_dir="$repo_root/.artifacts/build/$module_name"
  case "$build_dir" in
    "$repo_root"/.artifacts/build/*) ;;
    *) echo 'Build output must stay within the repository.' >&2; exit 2 ;;
  esac
  mkdir -p "$build_dir"
  (
    cd "$module"
    go test "${race[@]}" -count=1 ./...
    go vet ./...
    package_names=$(go list -f '{{.Name}}' ./...)
    if [[ $'\n'"$package_names"$'\n' == *$'\nmain\n'* ]]; then
      go build -o "$build_dir/" ./...
    else
      go build ./...
    fi
  )
done
