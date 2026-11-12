#!/usr/bin/env bash
# Imports the release signing certificate into a new keychain for this job, and
# says how to sign with it. CI runs it with a throwaway certificate on every PR,
# so a release can't be the first time it runs on a fresh runner.
#   SIGNING_P12=<base64 .p12> SIGNING_PASSWORD=<password> import-signing-cert.sh <keychain>
# It prints KEYCHAIN=<path> and ACMD_SIGN_IDENTITY=<SHA-1 of the certificate>,
# for $GITHUB_ENV. Neither is secret. scripts/build-app.sh signs with them.
set -euo pipefail

keychain=$1
name="Agent Command Self-Signed"
: "${SIGNING_P12:?set SIGNING_P12 to the base64 .p12}"
: "${SIGNING_PASSWORD:?set SIGNING_PASSWORD}"

key_password="$(openssl rand -base64 24)"
security create-keychain -p "$key_password" "$keychain"
security set-keychain-settings -lut 3600 "$keychain"
security unlock-keychain -p "$key_password" "$keychain"
# codesign looks identities up through the search list, so add this keychain to it.
existing=()
while IFS= read -r k; do
	k="${k#"${k%%[![:space:]]*}"}"
	existing+=("${k//\"/}")
done < <(security list-keychains -d user)
security list-keychains -d user -s "$keychain" "${existing[@]}"

# security reads the format from the file's extension, so it must end in .p12.
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
p12="$tmp/signing.p12"
printf '%s' "$SIGNING_P12" | base64 -d >"$p12"
security import "$p12" -f pkcs12 -k "$keychain" -P "$SIGNING_PASSWORD" -T /usr/bin/codesign >/dev/null
security set-key-partition-list -S apple-tool:,apple:,codesign: -s -k "$key_password" "$keychain" >/dev/null

# By its SHA-1, not its name: a self-signed certificate isn't trusted, and
# finding one by name doesn't work everywhere.
identity="$(security find-identity -p codesigning "$keychain" | awk -v n="\"$name\"" 'index($0, n) {print $2; exit}')"
if [[ -z "$identity" ]]; then
	echo "import-signing-cert: no \"$name\" identity in the certificate" >&2
	security find-identity -p codesigning "$keychain" >&2
	exit 1
fi
echo "KEYCHAIN=$keychain"
echo "ACMD_SIGN_IDENTITY=$identity"
