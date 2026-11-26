#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

VERSION="$("${ROOT_DIR}/scripts/version.sh")"
if [[ -z "$VERSION" || "$VERSION" =~ [[:space:]/] ]]; then
  printf "error: invalid package version: '%s'\n" "$VERSION" >&2
  exit 1
fi

sha256_file() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1"
  else
    sha256sum "$1"
  fi
}

MYGO_JSON="$ROOT_DIR/mygo.json"
WORK_DIR="$ROOT_DIR/.package-work.$$"
mkdir -p "$WORK_DIR"
cp "$MYGO_JSON" "$WORK_DIR/mygo.json.original"

restore_mygo_json() {
  if [[ -f "$WORK_DIR/mygo.json.original" ]]; then
    cp "$WORK_DIR/mygo.json.original" "$MYGO_JSON"
  fi
  rm -rf "$WORK_DIR"
}
trap restore_mygo_json EXIT INT TERM

# mygo v0.9.0 reads bundle/DMG version from mygo.json and has no -version flag.
# Inject the computed version only into mygo.json for this build and restore it
# on every exit path. Go binary version is injected cleanly via MYGO_LDFLAGS
# without mutating tracked version.go.
python3 - "$MYGO_JSON" "$VERSION" <<'PY'
import json
import pathlib
import sys

manifest = pathlib.Path(sys.argv[1])
version = sys.argv[2]
data = json.loads(manifest.read_text(encoding="utf-8"))
data["version"] = version
manifest.write_text(json.dumps(data, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
PY

BUILD_DIR="$ROOT_DIR/build"
APP="$BUILD_DIR/darwin-arm64/Clavis.app"
CLI_BIN="$BUILD_DIR/darwin-arm64/clavis-cli"
DMG="$BUILD_DIR/darwin-arm64/clavis-${VERSION}-arm64.dmg"
ZIP="$BUILD_DIR/clavis-${VERSION}-darwin-arm64.zip"
ZIP_STAGE="$WORK_DIR/zip-stage"
rm -rf "$APP" "$CLI_BIN" "$DMG" "$ZIP" "$APP.sha256" "$CLI_BIN.sha256" "$DMG.sha256" "$ZIP.sha256" "$ZIP_STAGE"

SIGN_ARGS=()
if [[ -n "${CLAVIS_SIGN_IDENTITY:-}" ]]; then
  SIGN_ARGS=(-sign "${CLAVIS_SIGN_IDENTITY}")
else
  # A local checkout may have no Developer ID identity. MyGo's explicit
  # ad-hoc marker keeps package verification useful without changing the
  # tracked manifest or pretending a distribution certificate exists.
  SIGN_ARGS=(-sign "-")
fi

echo "==> version: $VERSION"
echo "==> building macOS arm64 app and DMG (via MYGO_LDFLAGS)"
MYGO_LDFLAGS="-X main.clavisBuildVersion=${VERSION}" \
  go tool mygo build -platform darwin/arm64 ${SIGN_ARGS[@]+"${SIGN_ARGS[@]}"}

if [[ ! -d "$APP" ]]; then
  echo "error: expected app bundle was not produced: $APP" >&2
  exit 1
fi
if [[ ! -f "$DMG" ]]; then
  echo "error: expected DMG was not produced: $DMG" >&2
  exit 1
fi

echo "==> building macOS arm64 clavis-cli"
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 \
  go build -trimpath -ldflags "-s -w" -o "$CLI_BIN" ./cmd/clavis

if [[ "$(uname -s)" == "Darwin" ]]; then
  plutil -lint "$APP/Contents/Info.plist" >/dev/null
  version_output="$("$APP/Contents/MacOS/Clavis" --version)"
  echo "==> bundled version: $version_output"
  if [[ "$version_output" != *"$VERSION"* ]]; then
    echo "error: bundled --version does not contain $VERSION" >&2
    exit 1
  fi
fi

# Produce a portable ZIP alongside MyGo's DMG. Keep the CLI helper and legal
# notices next to the app so recipients can inspect them without network access.
# The app manifest records one SHA-256 per bundle file; the archive, CLI, and
# disk image have ordinary file sums.
mkdir -p "$ZIP_STAGE"
cp -R "$APP" "$ZIP_STAGE/Clavis.app"
cp "$CLI_BIN" "$ROOT_DIR/LICENSE" "$ROOT_DIR/THIRD-PARTY.md" "$ZIP_STAGE/"
(cd "$ZIP_STAGE" && zip -qr "$ZIP" Clavis.app clavis-cli LICENSE THIRD-PARTY.md)

(
  cd "$BUILD_DIR/darwin-arm64"
  if command -v shasum >/dev/null 2>&1; then
    find Clavis.app -type f -exec shasum -a 256 {} + | LC_ALL=C sort
  else
    find Clavis.app -type f -exec sha256sum {} + | LC_ALL=C sort
  fi
) > "$APP.sha256"
(
  cd "$(dirname "$CLI_BIN")"
  sha256_file "$(basename "$CLI_BIN")"
) > "$CLI_BIN.sha256"
(
  cd "$(dirname "$DMG")"
  sha256_file "$(basename "$DMG")"
) > "$DMG.sha256"
(
  cd "$BUILD_DIR"
  sha256_file "$(basename "$ZIP")"
) > "$ZIP.sha256"

printf '\n==> artifacts (%s)\n' "$VERSION"
for artifact in "$APP" "$APP.sha256" "$CLI_BIN" "$CLI_BIN.sha256" "$DMG" "$DMG.sha256" "$ZIP" "$ZIP.sha256"; do
  if [[ -d "$artifact" ]]; then
    size="$(du -sh "$artifact" | awk '{print $1}')"
  else
    size="$(wc -c < "$artifact" | tr -d ' ') bytes"
  fi
  printf '%-12s %s\n' "$size" "${artifact#$ROOT_DIR/}"
done
