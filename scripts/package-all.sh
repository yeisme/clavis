#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

VERSION="$(python3 -c 'import json; print(json.load(open("mygo.json"))["version"])')"

echo "==> Running go vet and unit tests..."
go vet ./...
GOOS=windows GOARCH=amd64 go vet ./...
GOOS=linux GOARCH=amd64 go vet ./...
go test -count=1 ./...

echo "==> Building darwin/arm64, windows/amd64, windows/arm64, linux/amd64..."
go tool mygo build -platform darwin/arm64,windows/amd64,windows/arm64,linux/amd64 -skip-dmg

echo "==> Packaging fresh Windows portable zip archives..."
for arch in amd64 arm64; do
  dir="build/windows-${arch}"
  zip_path="build/clavis-${VERSION}-windows-${arch}-portable.zip"
  rm -f "$zip_path"
  (cd "$dir" && zip -q -r "../clavis-${VERSION}-windows-${arch}-portable.zip" Clavis.exe Sqlitecore.dll icon.png)
  exe_sha="$(shasum -a 256 "${dir}/Clavis.exe" | awk '{print $1}')"
  zip_sha="$(unzip -p "$zip_path" Clavis.exe | shasum -a 256 | awk '{print $1}')"
  if [[ "$exe_sha" != "$zip_sha" ]]; then
    echo "ERROR: ${zip_path} Clavis.exe SHA-256 mismatch (${zip_sha} != ${exe_sha})" >&2
    exit 1
  fi
  echo "Verified ${zip_path} matches ${dir}/Clavis.exe (${exe_sha})"
done

echo "==> Verifying macOS bundle..."
plutil -lint build/darwin-arm64/Clavis.app/Contents/Info.plist
codesign --verify --deep --strict --verbose=2 build/darwin-arm64/Clavis.app
./build/darwin-arm64/Clavis.app/Contents/MacOS/Clavis --smoke-check

echo "==> All artifacts packaged and verified cleanly."
