#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

if [[ -n "${CLAVIS_VERSION:-}" ]]; then
  version="${CLAVIS_VERSION#v}"
  if [[ -z "$version" || "$version" =~ [[:space:]/] ]]; then
    printf "error: invalid CLAVIS_VERSION: '%s'\n" "$CLAVIS_VERSION" >&2
    exit 1
  fi
  printf '%s\n' "$version"
  exit 0
fi

base_version="$(python3 -c 'import json; print(json.load(open("mygo.json")).get("version", "0.1.0"))' 2>/dev/null || printf '0.1.0')"
base_version="${base_version#v}"
if [[ -z "$base_version" || "$base_version" =~ [[:space:]/] ]]; then
  base_version="0.1.0"
fi

# A tag is the preferred release identifier. `--long` lets us distinguish a
# tag-based description (tag-commits-gsha) from a hash-only description when
# the repository has no reachable tag.
if describe_long="$(git describe --tags --dirty --always --long 2>/dev/null)" && [[ "$describe_long" =~ -[0-9]+-g[0-9a-fA-F]+(-dirty)?$ ]]; then
  tag_version="$(git describe --tags --dirty --always)"
  printf '%s\n' "${tag_version#v}"
else
  commit_count="$(git rev-list --count HEAD 2>/dev/null || printf '0')"
  sha="$(git rev-parse --short=12 HEAD 2>/dev/null || printf 'unknown')"
  dirty=""
  if [[ -n "$(git status --porcelain 2>/dev/null)" ]]; then
    dirty=".dirty"
  fi
  printf '%s-dev.%s+%s%s\n' "$base_version" "$commit_count" "$sha" "$dirty"
fi
