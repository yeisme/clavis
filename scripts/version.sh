#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

base_version="0.1.0"

# A tag is the preferred release identifier. `--long` lets us distinguish a
# tag-based description (tag-commits-gsha) from a hash-only description when
# the repository has no reachable tag.
if describe_long="$(git describe --tags --dirty --always --long 2>/dev/null)" && [[ "$describe_long" =~ -[0-9]+-g[0-9a-fA-F]+(-dirty)?$ ]]; then
  git describe --tags --dirty --always
else
  commit_count="$(git rev-list --count HEAD 2>/dev/null || printf '0')"
  sha="$(git rev-parse --short=12 HEAD 2>/dev/null || printf 'unknown')"
  printf '%s-dev.%s+%s\n' "$base_version" "$commit_count" "$sha"
fi
