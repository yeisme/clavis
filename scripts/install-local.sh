#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

TARGET_APP="${CLAVIS_INSTALL_APP:-/Applications/Clavis.app}"
BACKUP_ROOT="${CLAVIS_BACKUP_DIR:-${HOME}/.local/share/clavis/backups}"
CLI_INSTALL_PATH="${CLAVIS_CLI_INSTALL_PATH:-${HOME}/.local/bin/clavis}"
DEFAULT_IDENTITY="Clavis Local Signing"

ALLOW_ADHOC=0
NO_LAUNCH=0
INSTALL_CLI=0
ROLLBACK=0

usage() {
  cat <<'EOF'
Usage: scripts/install-local.sh [options]

Safely builds, signs, backs up existing /Applications/Clavis.app, swaps in the
new bundle, verifies signature/smoke-check, and relaunches Clavis.

Options:
  --allow-adhoc   Proceed with ad-hoc signing if 'Clavis Local Signing' is absent
                  (warning: ad-hoc re-builds reset macOS TCC permissions)
  --no-launch     Do not launch /Applications/Clavis.app after installing
  --install-cli   Also install clavis-cli to ~/.local/bin/clavis
  --rollback      Restore the most recent backup from ~/.local/share/clavis/backups
  -h, --help      Show this help
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --allow-adhoc) ALLOW_ADHOC=1 ;;
    --no-launch)   NO_LAUNCH=1 ;;
    --install-cli) INSTALL_CLI=1 ;;
    --rollback)    ROLLBACK=1 ;;
    -h|--help)     usage; exit 0 ;;
    *)             echo "error: unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
  shift
done

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "error: install-local.sh is only supported on macOS" >&2
  exit 1
fi

quit_running_clavis() {
  if pgrep -x Clavis >/dev/null 2>&1; then
    echo "==> Quitting running Clavis..."
    osascript -e 'tell application id "com.yeisme.clavis" to quit' >/dev/null 2>&1 || true
    for _ in $(seq 1 20); do
      if ! pgrep -x Clavis >/dev/null 2>&1; then
        return 0
      fi
      sleep 0.25
    done
    pkill -x Clavis >/dev/null 2>&1 || true
    sleep 0.5
  fi
}

prune_old_backups() {
  if [[ ! -d "$BACKUP_ROOT" ]]; then
    return 0
  fi
  local count=0
  while IFS= read -r entry; do
    count=$((count + 1))
    if (( count > 3 )); then
      rm -rf "$entry"
    fi
  done < <(ls -1dt "${BACKUP_ROOT}/Clavis.app."* 2>/dev/null || true)
}

if (( ROLLBACK == 1 )); then
  latest_backup="$(ls -1dt "${BACKUP_ROOT}/Clavis.app."* 2>/dev/null | head -n 1 || true)"
  if [[ -z "$latest_backup" || ! -d "$latest_backup" ]]; then
    echo "error: no backup found under ${BACKUP_ROOT}" >&2
    exit 1
  fi
  echo "==> Rolling back ${TARGET_APP} from ${latest_backup}"
  quit_running_clavis
  stage_dir="${TARGET_APP}.rollback.$$"
  rm -rf "$stage_dir"
  ditto "$latest_backup" "$stage_dir"
  rm -rf "$TARGET_APP"
  mv "$stage_dir" "$TARGET_APP"
  codesign --verify --deep --strict "$TARGET_APP"
  echo "✓ Restored ${TARGET_APP}"
  if (( NO_LAUNCH == 0 )); then
    open -a "$TARGET_APP"
  fi
  exit 0
fi

SIGN_ID="${CLAVIS_SIGN_IDENTITY:-}"
if [[ -z "$SIGN_ID" ]]; then
  if security find-identity -v -p codesigning 2>/dev/null | grep -F "\"${DEFAULT_IDENTITY}\"" >/dev/null 2>&1; then
    SIGN_ID="$DEFAULT_IDENTITY"
  elif (( ALLOW_ADHOC == 1 )); then
    echo "warning: '${DEFAULT_IDENTITY}' not found; using ad-hoc signing (macOS TCC permissions may reset)." >&2
  else
    cat >&2 <<EOF
error: no '${DEFAULT_IDENTITY}' code-signing certificate found in login keychain.
Running ad-hoc builds over ${TARGET_APP} resets macOS Accessibility, Input Monitoring,
and Screen Recording permissions on every upgrade.

Create your local stable certificate once with:
  make setup-signing

Or explicitly override with:
  ./scripts/install-local.sh --allow-adhoc
EOF
    exit 1
  fi
fi

echo "==> Packaging Clavis (signing: ${SIGN_ID:-ad-hoc})"
if [[ -n "$SIGN_ID" ]]; then
  CLAVIS_SIGN_IDENTITY="$SIGN_ID" "${ROOT_DIR}/scripts/package.sh"
else
  "${ROOT_DIR}/scripts/package.sh"
fi

BUILT_APP="${ROOT_DIR}/build/darwin-arm64/Clavis.app"
BUILT_CLI="${ROOT_DIR}/build/darwin-arm64/clavis-cli"

echo "==> Verifying built bundle before replacing ${TARGET_APP}"
plutil -lint "${BUILT_APP}/Contents/Info.plist" >/dev/null
codesign --verify --deep --strict --verbose=2 "$BUILT_APP"
ISOLATED_HOME="$(mktemp -d /tmp/clavis-install-smoke.XXXXXX)"
trap 'rm -rf "$ISOLATED_HOME"' EXIT
HOME="$ISOLATED_HOME" "${BUILT_APP}/Contents/MacOS/Clavis" --smoke-check

quit_running_clavis

BACKUP_PATH=""
if [[ -d "$TARGET_APP" ]]; then
  mkdir -p "$BACKUP_ROOT"
  BACKUP_PATH="${BACKUP_ROOT}/Clavis.app.$(date +%Y%m%d-%H%M%S)"
  echo "==> Backing up existing app to ${BACKUP_PATH}"
  ditto "$TARGET_APP" "$BACKUP_PATH"
  prune_old_backups
fi

STAGE_APP="${TARGET_APP}.new.$$"
rm -rf "$STAGE_APP"
if ! ditto "$BUILT_APP" "$STAGE_APP"; then
  rm -rf "$STAGE_APP"
  echo "error: failed to copy ${BUILT_APP} to ${STAGE_APP}" >&2
  exit 1
fi

rm -rf "$TARGET_APP"
if ! mv "$STAGE_APP" "$TARGET_APP"; then
  echo "error: failed to swap ${TARGET_APP}; attempting rollback..." >&2
  if [[ -n "$BACKUP_PATH" && -d "$BACKUP_PATH" ]]; then
    ditto "$BACKUP_PATH" "$TARGET_APP" || true
  fi
  exit 1
fi

echo "==> Verifying installed ${TARGET_APP}"
codesign --verify --deep --strict "$TARGET_APP"
"${TARGET_APP}/Contents/MacOS/Clavis" --version

if (( INSTALL_CLI == 1 )) && [[ -f "$BUILT_CLI" ]]; then
  mkdir -p "$(dirname "$CLI_INSTALL_PATH")"
  cp "$BUILT_CLI" "$CLI_INSTALL_PATH"
  chmod 0755 "$CLI_INSTALL_PATH"
  echo "✓ Installed clavis CLI to ${CLI_INSTALL_PATH}"
fi

echo "✓ Successfully installed ${TARGET_APP}"
if (( NO_LAUNCH == 0 )); then
  echo "==> Launching ${TARGET_APP}"
  open -a "$TARGET_APP"
fi
