#!/usr/bin/env bash
# Creates the long-lived self-signed certificate that signs every release, so
# macOS keeps treating updates as the same app. Run it once, keep the output
# safe, and never replace it without a reason: a new certificate is a new app
# to macOS.
#   create-signing-cert.sh <output-dir>
# It writes the certificate and key to <output-dir>, and prints what to store as
# the ACMD_SIGNING_P12 and ACMD_SIGNING_PASSWORD repository secrets. It changes
# no keychain.
set -euo pipefail

out=$1
name="Agent Command Self-Signed"
mkdir -p "$out"
umask 077
password="$(openssl rand -base64 24)"

openssl req -x509 -newkey rsa:2048 -nodes -days 7300 \
	-keyout "$out/key.pem" -out "$out/cert.pem" -subj "/CN=$name" \
	-addext "keyUsage=critical,digitalSignature" \
	-addext "extendedKeyUsage=critical,codeSigning" \
	-addext "basicConstraints=critical,CA:false" 2>/dev/null
# 3DES with a SHA-1 MAC: what macOS's keychain imports, and what every OpenSSL
# and LibreSSL can write (OpenSSL 3's own default is a format macOS refuses).
openssl pkcs12 -export -keypbe PBE-SHA1-3DES -certpbe PBE-SHA1-3DES -macalg sha1 \
	-inkey "$out/key.pem" -in "$out/cert.pem" -name "$name" \
	-passout "pass:$password" -out "$out/signing.p12"
base64 -i "$out/signing.p12" >"$out/signing.p12.base64"
printf '%s' "$password" >"$out/signing.password"

echo "Wrote $out/signing.p12.base64 (ACMD_SIGNING_P12) and $out/signing.password (ACMD_SIGNING_PASSWORD)."
echo "Store them as repository secrets, then keep $out somewhere safe, away from the repository."
