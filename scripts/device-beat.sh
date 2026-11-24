#!/usr/bin/env bash
# device-beat.sh — 跨设备心跳（macOS / Linux 侧）
# 用法：scripts/device-beat.sh [--no-commit] [device-name] [tests-summary] [notes]
set -euo pipefail

NO_COMMIT=0
POSITIONAL=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    --no-commit)
      NO_COMMIT=1
      shift
      ;;
    -h|--help)
      cat <<'USAGE'
Usage: scripts/device-beat.sh [--no-commit] [device-name] [tests-summary] [notes]

Writes docs/device-status/<device-name>.json with valid JSON escaping and
optionally commits the heartbeat file (unless --no-commit is given).
USAGE
      exit 0
      ;;
    --)
      shift
      while [[ $# -gt 0 ]]; do
        POSITIONAL+=("$1")
        shift
      done
      ;;
    *)
      POSITIONAL+=("$1")
      shift
      ;;
  esac
done

DEVICE="${POSITIONAL[0]:-mini}"
TESTS="${POSITIONAL[1]:-}"
NOTES="${POSITIONAL[2]:-}"

if [[ -z "$DEVICE" || "$DEVICE" == *"/"* || "$DEVICE" == *".."* || "$DEVICE" =~ [[:space:]] ]]; then
  printf "error: invalid device name: '%s'\n" "$DEVICE" >&2
  exit 1
fi

REPO="$(git rev-parse --show-toplevel)"
cd "$REPO"

BRANCH="$(git rev-parse --abbrev-ref HEAD)"
HEAD="$(git rev-parse --short HEAD)"
DIRTY="$(git status --porcelain | wc -l | tr -d ' ')"
AHEAD="$(git rev-list --count '@{u}..HEAD' 2>/dev/null || echo -1)"
PLATFORM="$(uname -s | tr '[:upper:]' '[:lower:]')"
TIMESTAMP="$(date -Iseconds 2>/dev/null || date +"%Y-%m-%dT%H:%M:%S%z")"

OUT_DIR="docs/device-status"
OUT_FILE="${OUT_DIR}/${DEVICE}.json"
mkdir -p "$OUT_DIR"

json_escape_shell() {
  local s="$1"
  s="${s//\\/\\\\}"
  s="${s//\"/\\\"}"
  s="${s//$'\n'/\\n}"
  s="${s//$'\r'/\\r}"
  s="${s//$'\t'/\\t}"
  printf '%s' "$s"
}

if command -v python3 >/dev/null 2>&1; then
  python3 - "$OUT_FILE" "$DEVICE" "$PLATFORM" "$BRANCH" "$HEAD" "$DIRTY" "$AHEAD" "$TIMESTAMP" "$TESTS" "$NOTES" <<'PY'
import json
import pathlib
import sys

out_path = pathlib.Path(sys.argv[1])
payload = {
    "device": sys.argv[2],
    "platform": sys.argv[3],
    "project": "clavis",
    "branch": sys.argv[4],
    "head": sys.argv[5],
    "dirty": int(sys.argv[6]),
    "ahead": int(sys.argv[7]),
    "timestamp": sys.argv[8],
    "tests": sys.argv[9],
    "notes": sys.argv[10],
}
out_path.write_text(json.dumps(payload, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
PY
else
  cat > "$OUT_FILE" <<JSON
{
  "device": "$(json_escape_shell "$DEVICE")",
  "platform": "$(json_escape_shell "$PLATFORM")",
  "project": "clavis",
  "branch": "$(json_escape_shell "$BRANCH")",
  "head": "$(json_escape_shell "$HEAD")",
  "dirty": ${DIRTY},
  "ahead": ${AHEAD},
  "timestamp": "$(json_escape_shell "$TIMESTAMP")",
  "tests": "$(json_escape_shell "$TESTS")",
  "notes": "$(json_escape_shell "$NOTES")"
}
JSON
fi

if [[ "$NO_COMMIT" -eq 1 ]]; then
  echo "wrote ${OUT_FILE} (--no-commit: skipped git commit)"
  exit 0
fi

git add "$OUT_FILE"
git commit -m "[shared] chore(beat): device heartbeat ${HEAD}" -- "$OUT_FILE"
echo "wrote ${OUT_FILE} and committed (not pushed)"
