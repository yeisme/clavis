#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

VERSION="$(${ROOT_DIR}/scripts/version.sh)"
if [[ -z "$VERSION" || "$VERSION" =~ [[:space:]/] ]]; then
  echo "error: invalid package version: ${VERSION@Q}" >&2
  exit 1
fi

MYGO_JSON="$ROOT_DIR/mygo.json"
VERSION_SOURCE="$ROOT_DIR/version.go"
WORK_DIR="$ROOT_DIR/.package-work.$$"
mkdir -p "$WORK_DIR"
cp "$MYGO_JSON" "$WORK_DIR/mygo.json.original"
cp "$VERSION_SOURCE" "$WORK_DIR/version.go.original"

restore_mygo_json() {
  if [[ -f "$WORK_DIR/mygo.json.original" ]]; then
    cp "$WORK_DIR/mygo.json.original" "$MYGO_JSON"
  fi
  if [[ -f "$WORK_DIR/version.go.original" ]]; then
    cp "$WORK_DIR/version.go.original" "$VERSION_SOURCE"
  fi
  rm -rf "$WORK_DIR"
}
trap restore_mygo_json EXIT INT TERM

# mygo v0.9.0 has no version flag. Inject the computed version only for this
# build and restore the tracked manifest on every exit path.
python3 - "$MYGO_JSON" "$VERSION" "$VERSION_SOURCE" <<'PY'
import json
import pathlib
import sys

manifest = pathlib.Path(sys.argv[1])
version = sys.argv[2]
source = pathlib.Path(sys.argv[3])
data = json.loads(manifest.read_text())
data["version"] = version
manifest.write_text(json.dumps(data, indent=2, ensure_ascii=False) + "\n")
text = source.read_text()
old = 'const clavisBuildVersion = "0.1.0-dev"'
new = 'const clavisBuildVersion = ' + json.dumps(version)
if old not in text:
    raise SystemExit(f"version constant not found in {source}")
source.write_text(text.replace(old, new, 1))
PY
gofmt -w "$VERSION_SOURCE"

BUILD_DIR="$ROOT_DIR/build"
APP="$BUILD_DIR/darwin-arm64/Clavis.app"
DMG="$BUILD_DIR/darwin-arm64/clavis-${VERSION}-arm64.dmg"
ZIP="$BUILD_DIR/clavis-${VERSION}-darwin-arm64.zip"
ZIP_STAGE="$WORK_DIR/zip-stage"
rm -rf "$APP" "$DMG" "$ZIP" "$APP.sha256" "$DMG.sha256" "$ZIP.sha256" "$ZIP_STAGE"

echo "==> version: $VERSION"
echo "==> building macOS arm64 app and DMG"
go tool mygo build -platform darwin/arm64

if [[ ! -d "$APP" ]]; then
  echo "error: expected app bundle was not produced: $APP" >&2
  exit 1
fi
if [[ ! -f "$DMG" ]]; then
  echo "error: expected DMG was not produced: $DMG" >&2
  exit 1
fi

version_output="$($APP/Contents/MacOS/Clavis --version)"
echo "==> bundled version: $version_output"
if [[ "$version_output" != *"$VERSION"* ]]; then
  echo "error: bundled --version does not contain $VERSION" >&2
  exit 1
fi

# Produce a portable ZIP alongside MyGo's DMG. Keep the legal notices next to
# the app so recipients can inspect them without network access. The app
# manifest records one SHA-256 per bundle file; the archive and disk image
# have ordinary file sums.
mkdir -p "$ZIP_STAGE"
cp -R "$APP" "$ZIP_STAGE/Clavis.app"
cp "$ROOT_DIR/LICENSE" "$ROOT_DIR/THIRD-PARTY.md" "$ZIP_STAGE/"
(cd "$ZIP_STAGE" && zip -qr "$ZIP" Clavis.app LICENSE THIRD-PARTY.md)

(
  cd "$BUILD_DIR/darwin-arm64"
  find Clavis.app -type f -exec shasum -a 256 {} + | LC_ALL=C sort
) > "$APP.sha256"
(
  cd "$BUILD_DIR"
  shasum -a 256 "$(basename "$DMG")"
) > "$DMG.sha256"
(
  cd "$BUILD_DIR"
  shasum -a 256 "$(basename "$ZIP")"
) > "$ZIP.sha256"

printf '\n==> artifacts (%s)\n' "$VERSION"
for artifact in "$APP" "$APP.sha256" "$DMG" "$DMG.sha256" "$ZIP" "$ZIP.sha256"; do
  if [[ -d "$artifact" ]]; then
    size="$(du -sh "$artifact" | awk '{print $1}')"
  else
    size="$(wc -c < "$artifact" | tr -d ' ') bytes"
  fi
  printf '%-8s %s\n' "$size" "${artifact#$ROOT_DIR/}"
done
