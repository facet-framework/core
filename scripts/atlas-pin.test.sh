#!/bin/sh
set -eu

root=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d "${TMPDIR:-/tmp}/atlas-pin-test.XXXXXX")
trap 'rm -rf "$work"' EXIT
trap 'exit 1' HUP INT TERM
go_binary=$(command -v go)
export PIN_TEST_ROOT="$root" PIN_TEST_WORK="$work" PIN_TEST_GO="$go_binary"
export FACET_ATLAS_BASE_URL=https://atlas-release.invalid
export PATH="$work/bin:$PATH"
release="$work/release/v0.1.0-rc.3"
mkdir -p "$work/bin" "$release" "$work/project/scripts" "$work/project/internal/atlaslauncher"
cp "$root/scripts/atlas-pin.sh" "$work/project/scripts/"
pin="$work/project/internal/atlaslauncher/atlas.json"
echo original >"$pin"

cat >"$work/bin/go" <<'SH'
#!/bin/sh
exec "$PIN_TEST_GO" -C "$PIN_TEST_ROOT" "$@"
SH
cat >"$work/bin/curl" <<'SH'
#!/bin/sh
set -eu
fail= output= status=
while [ "$#" -ne 0 ]; do
  case "$1" in
    -fsSL) fail=yes ;;
    -sSL) ;;
    --max-time) shift ;;
    --output) shift; output=$1 ;;
    --write-out) shift; status=yes ;;
    https://atlas-release.invalid/*) path="$PIN_TEST_WORK/release/${1#https://atlas-release.invalid/}" ;;
    *) exit 2 ;;
  esac
  shift
done
if [ -f "$path" ]; then
  cp "$path" "$output"
  if [ "$status" = yes ]; then printf 200; fi
elif [ "$fail" = yes ]; then
  exit 22
else
  if [ "$status" = yes ]; then printf 404; fi
fi
SH
cat >"$work/bin/gh" <<'SH'
#!/bin/sh
set -eu
case "$*" in
  "api repos/facet-framework/atlas/releases/tags/v0.1.0-rc.3")
    cat "$PIN_TEST_WORK/assets.json" ;;
  "attestation verify --repo facet-framework/atlas "*)
    [ "$#" -eq 5 ] && [ -f "$5" ]
    basename "$5" >> "$PIN_TEST_WORK/attestations"
    [ "${PIN_TEST_FAIL_ATTESTATION:-}" != "$(basename "$5")" ] ;;
  *) exit 2 ;;
esac
SH
chmod +x "$work/bin/"*

cat >"$release/release-manifest.json" <<'JSON'
{"manifest_version":1,"product":"atlas","version":"0.1.0-rc.3","core_version":"v0.1.0-rc.4","channel":"rc","platforms":[
  {"platform":"linux_amd64","archive":"atlas-linux.tar.gz","archive_sha256":"ignored"},
  {"platform":"windows_amd64","archive":"atlas-windows.zip","archive_sha256":"ignored"}
]}
JSON
printf linux >"$release/atlas-linux.tar.gz"
printf windows >"$release/atlas-windows.zip"
(cd "$release" && sha256sum atlas-linux.tar.gz atlas-windows.zip >checksums.txt)
echo '{"assets":[{"name":"atlas-linux.tar.gz","size":5},{"name":"atlas-windows.zip","size":7}]}' >"$work/assets.json"

script="$work/project/scripts/atlas-pin.sh"
if sh "$script" 0.1.0-rc.3 >"$work/error" 2>&1; then
  echo "Unsigned release accepted without --unsigned" >&2
  exit 1
fi
[ "$(cat "$pin")" = original ]
echo '{"key_id":"untrusted","algorithm":"ed25519","signature":"AAAA"}' >"$release/release-manifest.json.sig"
if sh "$script" 0.1.0-rc.3 >"$work/error" 2>&1; then
  echo "Unknown signer accepted" >&2
  exit 1
fi
grep -q 'unknown Atlas signing key' "$work/error"
if sh "$script" 0.1.0-rc.3 --unsigned >"$work/error" 2>&1; then
  echo "Signature bypassed with --unsigned" >&2
  exit 1
fi
rm "$release/release-manifest.json.sig"
if PIN_TEST_FAIL_ATTESTATION=atlas-windows.zip sh "$script" 0.1.0-rc.3 --unsigned; then
  echo "Pin rewritten with a failing attestation" >&2
  exit 1
fi
[ "$(cat "$pin")" = original ]
: >"$work/attestations"
sh "$script" 0.1.0-rc.3 --unsigned
printf '%s\n' atlas-linux.tar.gz atlas-windows.zip | diff - "$work/attestations"
digest=$(awk '$2 == "atlas-linux.tar.gz" {print $1}' "$release/checksums.txt")
windows_digest=$(awk '$2 == "atlas-windows.zip" {print $1}' "$release/checksums.txt")
jq -e --arg digest "$digest" --arg windows_digest "$windows_digest" '
  .version == "0.1.0-rc.3" and .core == "0.1.0-rc.4" and
  .platforms["linux/amd64"] == {archive:"atlas-linux.tar.gz",sha256:$digest,size:5} and
  .platforms["windows/amd64"] == {archive:"atlas-windows.zip",sha256:$windows_digest,size:7}
' "$pin" >/dev/null
if sh "$script" 0.1.0-rc.4 --unsigned >"$work/error" 2>&1; then
  echo "Unsigned Atlas rc.4 accepted" >&2
  exit 1
fi
grep -q 'only supported' "$work/error"
echo 'Atlas pin verified local release metadata and archive attestations.'
