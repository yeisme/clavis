#!/usr/bin/env bash
set -euo pipefail

IDENTITY_NAME="${CLAVIS_SIGN_IDENTITY:-Clavis Local Signing}"
KEYCHAIN_PATH="${HOME}/Library/Keychains/login.keychain-db"

check_identity() {
  security find-identity -v -p codesigning 2>/dev/null | grep -F "\"${IDENTITY_NAME}\""
}

if [[ "${1:-}" == "--check" ]]; then
  if check_identity >/dev/null 2>&1; then
    check_identity
    exit 0
  fi
  echo "No valid code-signing identity named '${IDENTITY_NAME}' found." >&2
  exit 1
fi

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "error: setup-local-signing.sh is only supported on macOS" >&2
  exit 1
fi

if check_identity >/dev/null 2>&1; then
  echo "Valid code-signing identity already present:"
  check_identity
  exit 0
fi

for tool in openssl security; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "error: required tool not found: $tool" >&2
    exit 1
  fi
done

TMP_DIR="$(mktemp -d /tmp/clavis-signing.XXXXXX)"
cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT INT TERM

cat > "$TMP_DIR/openssl.cnf" <<EOF
[ req ]
default_bits       = 2048
distinguished_name = req_distinguished_name
x509_extensions    = codesign_ext
prompt             = no

[ req_distinguished_name ]
CN = ${IDENTITY_NAME}
O  = Clavis Local

[ codesign_ext ]
keyUsage = critical, digitalSignature
extendedKeyUsage = critical, codeSigning
basicConstraints = critical, CA:false
subjectKeyIdentifier = hash
EOF

echo "==> Generating self-signed code-signing certificate: '${IDENTITY_NAME}'"
openssl req -x509 -newkey rsa:2048 -nodes \
  -keyout "$TMP_DIR/key.pem" \
  -out "$TMP_DIR/cert.pem" \
  -days 3650 \
  -config "$TMP_DIR/openssl.cnf" 2>/dev/null

P12_PASS="clavis-local-signing-tmp"
if ! openssl pkcs12 -export \
  -inkey "$TMP_DIR/key.pem" \
  -in "$TMP_DIR/cert.pem" \
  -out "$TMP_DIR/cert.p12" \
  -passout "pass:${P12_PASS}" \
  -legacy 2>/dev/null; then
  openssl pkcs12 -export \
    -inkey "$TMP_DIR/key.pem" \
    -in "$TMP_DIR/cert.pem" \
    -out "$TMP_DIR/cert.p12" \
    -passout "pass:${P12_PASS}"
fi

echo "==> Importing identity into ${KEYCHAIN_PATH}"
security import "$TMP_DIR/cert.p12" \
  -k "$KEYCHAIN_PATH" \
  -P "$P12_PASS" \
  -T /usr/bin/codesign \
  -T /usr/bin/security

echo "==> Trusting certificate for code signing (macOS may prompt for your login password)"
if ! security add-trusted-cert -r trustRoot -p codeSign -k "$KEYCHAIN_PATH" "$TMP_DIR/cert.pem"; then
  cat <<EOF

Could not automatically mark '${IDENTITY_NAME}' as trusted for code signing.
To finish setup manually:
  1. Open 'Keychain Access.app' -> login -> Certificates
  2. Double-click '${IDENTITY_NAME}' -> expand 'Trust'
  3. Set 'Code Signing' to 'Always Trust'
  4. Re-run: ./scripts/setup-local-signing.sh --check
EOF
  exit 1
fi

echo "==> Verifying code-signing identities:"
if ! check_identity; then
  echo "error: '${IDENTITY_NAME}' was imported but is not yet reported as valid by security find-identity" >&2
  exit 1
fi
