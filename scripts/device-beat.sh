#!/usr/bin/env bash
# device-beat.sh — 跨设备心跳（macOS / Linux 侧）
# 用法：scripts/device-beat.sh [device-name] [tests-summary] [notes]
set -euo pipefail
DEVICE="${1:-mini}"
TESTS="${2:-}"
NOTES="${3:-}"
REPO="$(git rev-parse --show-toplevel)"
cd "$REPO"
BRANCH="$(git rev-parse --abbrev-ref HEAD)"
HEAD="$(git rev-parse --short HEAD)"
DIRTY="$(git status --porcelain | wc -l | tr -d ' ')"
AHEAD="$(git rev-list --count '@{u}..HEAD' 2>/dev/null || echo -1)"
mkdir -p docs/device-status
cat > "docs/device-status/${DEVICE}.json" <<JSON
{
  "device": "${DEVICE}",
  "platform": "$(uname -s | tr '[:upper:]' '[:lower:]')",
  "project": "clavis",
  "branch": "${BRANCH}",
  "head": "${HEAD}",
  "dirty": ${DIRTY},
  "ahead": ${AHEAD},
  "timestamp": "$(date -Iseconds)",
  "tests": "${TESTS}",
  "notes": "${NOTES}"
}
JSON
git add "docs/device-status/${DEVICE}.json"
git commit -m "[shared] chore(beat): device heartbeat ${HEAD}" -- "docs/device-status/${DEVICE}.json"
echo "wrote docs/device-status/${DEVICE}.json and committed (not pushed)"
