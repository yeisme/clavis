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

sha256_stdin() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 | awk '{print $1}'
  else
    sha256sum | awk '{print $1}'
  fi
}

MYGO_JSON="$ROOT_DIR/mygo.json"
WORK_DIR="$ROOT_DIR/.package-all-work.$$"
mkdir -p "$WORK_DIR"
cp "$MYGO_JSON" "$WORK_DIR/mygo.json.original"

cleanup() {
  if [[ -f "$WORK_DIR/mygo.json.original" ]]; then
    cp "$WORK_DIR/mygo.json.original" "$MYGO_JSON"
  fi
  rm -rf "$WORK_DIR"
}
trap cleanup EXIT INT TERM

echo "==> Version: ${VERSION}"
echo "==> Running go vet and unit tests..."
go vet ./...
GOOS=windows GOARCH=amd64 go vet ./...
GOOS=linux GOARCH=amd64 go vet ./...
go test -count=1 ./...

# Inject VERSION into mygo.json only for the duration of mygo build so platform
# bundle metadata matches, while injecting Go binary version via MYGO_LDFLAGS
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

SIGN_ARGS=()
if [[ -n "${CLAVIS_SIGN_IDENTITY:-}" ]]; then
  SIGN_ARGS=(-sign "${CLAVIS_SIGN_IDENTITY}")
else
  # Use an explicit ad-hoc signature when no local Developer ID is available.
  SIGN_ARGS=(-sign "-")
fi

echo "==> Building darwin/arm64, windows/amd64, windows/arm64, linux/amd64 (via MYGO_LDFLAGS)..."
MYGO_LDFLAGS="-X main.clavisBuildVersion=${VERSION}" \
  go tool mygo build -platform darwin/arm64,windows/amd64,windows/arm64,linux/amd64 -skip-dmg -skip-installer ${SIGN_ARGS[@]+"${SIGN_ARGS[@]}"}

echo "==> Building clavis-cli per platform..."
build_cli() {
  local goos="$1"
  local goarch="$2"
  local out="$3"
  mkdir -p "$(dirname "$out")"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build -trimpath -ldflags "-s -w" -o "$out" ./cmd/clavis
}

build_cli darwin arm64 "build/darwin-arm64/clavis-cli"
build_cli windows amd64 "build/windows-amd64/clavis-cli.exe"
build_cli windows arm64 "build/windows-arm64/clavis-cli.exe"
build_cli linux amd64 "build/linux-amd64/clavis-cli"

RELEASE_ARTIFACTS=()

echo "==> Verifying and packaging macOS (darwin/arm64)..."
DARWIN_APP="build/darwin-arm64/Clavis.app"
if [[ ! -d "$DARWIN_APP" ]]; then
  echo "ERROR: missing macOS app bundle: $DARWIN_APP" >&2
  exit 1
fi
if [[ "$(uname -s)" == "Darwin" ]]; then
  plutil -lint "${DARWIN_APP}/Contents/Info.plist"
  codesign --verify --deep --strict --verbose=2 "$DARWIN_APP"
  darwin_ver="$("./${DARWIN_APP}/Contents/MacOS/Clavis" --version)"
  echo "macOS binary version: ${darwin_ver}"
  if [[ "$darwin_ver" != *"$VERSION"* ]]; then
    echo "ERROR: macOS --version does not contain ${VERSION}: ${darwin_ver}" >&2
    exit 1
  fi
  "./${DARWIN_APP}/Contents/MacOS/Clavis" --smoke-check
fi

DARWIN_STAGE="${WORK_DIR}/darwin-arm64"
DARWIN_ZIP="build/clavis-${VERSION}-darwin-arm64.zip"
rm -rf "$DARWIN_STAGE" "$DARWIN_ZIP" "${DARWIN_ZIP}.sha256"
mkdir -p "$DARWIN_STAGE"
cp -R "$DARWIN_APP" "${DARWIN_STAGE}/Clavis.app"
cp "build/darwin-arm64/clavis-cli" LICENSE THIRD-PARTY.md "${DARWIN_STAGE}/"
(cd "$DARWIN_STAGE" && zip -q -r "${ROOT_DIR}/${DARWIN_ZIP}" Clavis.app clavis-cli LICENSE THIRD-PARTY.md)
(cd build && sha256_file "$(basename "$DARWIN_ZIP")") > "${DARWIN_ZIP}.sha256"
RELEASE_ARTIFACTS+=("$DARWIN_ZIP")

echo "==> Packaging fresh Windows portable zip archives..."
for arch in amd64 arm64; do
  dir="build/windows-${arch}"
  stage="${WORK_DIR}/windows-${arch}"
  zip_path="build/clavis-${VERSION}-windows-${arch}-portable.zip"
  rm -rf "$stage" "$zip_path" "${zip_path}.sha256"
  mkdir -p "$stage"

  if [[ ! -f "${dir}/Clavis.exe" || ! -f "${dir}/clavis-cli.exe" ]]; then
    echo "ERROR: missing Windows binaries in ${dir}" >&2
    exit 1
  fi

  shopt -s nullglob
  for item in "${dir}"/*; do
    base="$(basename "$item")"
    case "$base" in
      *.zip|*.tar.gz|*.sha256|.mygo*)
        continue
        ;;
    esac
    cp -R "$item" "${stage}/${base}"
  done
  shopt -u nullglob
  cp LICENSE THIRD-PARTY.md "${stage}/"

  (cd "$stage" && zip -q -r "${ROOT_DIR}/${zip_path}" .)

  exe_sha="$(sha256_file "${dir}/Clavis.exe" | awk '{print $1}')"
  zip_sha="$(unzip -p "$zip_path" Clavis.exe | sha256_stdin)"
  if [[ "$exe_sha" != "$zip_sha" ]]; then
    echo "ERROR: ${zip_path} Clavis.exe SHA-256 mismatch (${zip_sha} != ${exe_sha})" >&2
    exit 1
  fi
  for required in Clavis.exe clavis-cli.exe LICENSE THIRD-PARTY.md; do
    if ! unzip -Z1 "$zip_path" | grep -Fxq "$required"; then
      echo "ERROR: ${zip_path} is missing required entry: ${required}" >&2
      exit 1
    fi
  done
  (cd build && sha256_file "$(basename "$zip_path")") > "${zip_path}.sha256"
  RELEASE_ARTIFACTS+=("$zip_path")
  echo "Verified ${zip_path} matches ${dir}/Clavis.exe (${exe_sha})"
done

echo "==> Packaging Linux (linux/amd64) tar.gz archive..."
LINUX_DIR="build/linux-amd64"
LINUX_BUNDLE_NAME="clavis-${VERSION}-linux-amd64"
LINUX_STAGE="${WORK_DIR}/linux-amd64/${LINUX_BUNDLE_NAME}"
LINUX_TAR="build/${LINUX_BUNDLE_NAME}.tar.gz"
rm -rf "${WORK_DIR}/linux-amd64" "$LINUX_TAR" "${LINUX_TAR}.sha256"
mkdir -p "$LINUX_STAGE"

if [[ ! -f "${LINUX_DIR}/clavis" || ! -f "${LINUX_DIR}/clavis-cli" ]]; then
  echo "ERROR: missing Linux binaries in ${LINUX_DIR}" >&2
  exit 1
fi

shopt -s nullglob
for item in "${LINUX_DIR}"/*; do
  base="$(basename "$item")"
  case "$base" in
    *.tar.gz|*.zip|*.sha256|.mygo*)
      continue
      ;;
  esac
  cp -R "$item" "${LINUX_STAGE}/${base}"
done
shopt -u nullglob
cp LICENSE THIRD-PARTY.md "${LINUX_STAGE}/"

tar -czf "${ROOT_DIR}/${LINUX_TAR}" -C "${WORK_DIR}/linux-amd64" "$LINUX_BUNDLE_NAME"
for required in "${LINUX_BUNDLE_NAME}/clavis" "${LINUX_BUNDLE_NAME}/clavis-cli" "${LINUX_BUNDLE_NAME}/LICENSE" "${LINUX_BUNDLE_NAME}/THIRD-PARTY.md"; do
  if ! tar -tzf "$LINUX_TAR" | grep -Fxq "$required"; then
    echo "ERROR: ${LINUX_TAR} is missing required entry: ${required}" >&2
    exit 1
  fi
done
(cd build && sha256_file "$(basename "$LINUX_TAR")") > "${LINUX_TAR}.sha256"
RELEASE_ARTIFACTS+=("$LINUX_TAR")

echo "==> Writing combined SHA-256 checksum manifest..."
SUMS_FILE="build/clavis-${VERSION}-SHA256SUMS.txt"
rm -f "$SUMS_FILE"
(
  cd build
  for artifact in "${RELEASE_ARTIFACTS[@]}"; do
    sha256_file "$(basename "$artifact")"
  done | LC_ALL=C sort -k2
) > "$SUMS_FILE"

printf '\n==> Release artifacts (%s)\n' "$VERSION"
for artifact in "${RELEASE_ARTIFACTS[@]}" "$SUMS_FILE"; do
  size="$(wc -c < "$artifact" | tr -d ' ') bytes"
  printf '%-14s %s\n' "$size" "$artifact"
done
echo "==> All artifacts packaged and verified cleanly."
