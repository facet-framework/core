#!/bin/sh
# @facet: delivery/distribution/atlas-updates
# @facet-role: related
set -eu

die() {
  echo "atlas-pin: $*" >&2
  exit 1
}

[ "$#" -ge 1 ] && [ "$#" -le 2 ] || die "usage: scripts/atlas-pin.sh <version> [--unsigned]"
version=${1#v}
case "$version" in
'' | *[!a-zA-Z0-9.+-]*) die "invalid version" ;;
esac
case "${2-}" in
'') unsigned= ;;
--unsigned)
  unsigned=-unsigned
  case "$version" in
  0.1.0-rc.1 | 0.1.0-rc.2 | 0.1.0-rc.3) ;;
  *) die "--unsigned is only supported for Atlas 0.1.0-rc.1 through rc.3" ;;
  esac
  ;;
*) die "unknown option: $2" ;;
esac

cd "$(dirname "$0")/.."
repo=facet-framework/atlas
base=${FACET_ATLAS_BASE_URL:-https://github.com/facet-framework/atlas/releases/download}
release=${base%/}/v$version
pin=internal/atlaslauncher/atlas.json
work=$(mktemp -d "${TMPDIR:-/tmp}/facet-atlas-pin.XXXXXX")
staged=
trap 'rm -rf "$work"; if [ -n "$staged" ]; then rm -f "$staged"; fi' EXIT
trap 'exit 1' HUP INT TERM

fetch() { curl -fsSL --max-time 300 --output "$work/$1" "$release/$1"; }

fetch release-manifest.json
manifest="$work/release-manifest.json"
sig="$work/release-manifest.json.sig"
if [ -z "$unsigned" ]; then
  fetch release-manifest.json.sig
else
  status=$(curl -sSL --max-time 30 --output "$sig" --write-out '%{http_code}' "$release/release-manifest.json.sig")
  [ "$status" = 404 ] || die "--unsigned requires a missing signature asset (HTTP 404)"
  fetch checksums.txt
  gh api "repos/$repo/releases/tags/v$version" >"$work/release.json"
  jq -er '.platforms | if type == "array" and length > 0 then .[] .archive else error("missing platforms") end |
    if type == "string" and test("^[A-Za-z0-9][A-Za-z0-9._+-]*\\.(tar\\.gz|zip)$") then . else error("invalid archive name") end' \
    "$manifest" >"$work/archives"
  : >"$work/verified.jsonl"
  while IFS= read -r archive; do
    digest=$(awk -v name="$archive" '$2 == name || $2 == "*" name {print tolower($1)}' "$work/checksums.txt")
    size=$(jq -er --arg name "$archive" '[.assets[] | select(.name == $name) | .size] |
      if length == 1 and (.[0] | type == "number" and . > 0 and floor == .) then .[0] else error("invalid asset size") end' "$work/release.json")
    fetch "$archive"
    actual=$(sha256sum "$work/$archive")
    [ "$digest" = "${actual%% *}" ] || die "$archive checksum mismatch"
    [ "$(wc -c <"$work/$archive" | tr -d '[:space:]')" = "$size" ] || die "$archive size mismatch"
    gh attestation verify --repo "$repo" "$work/$archive" >&2
    rm -f "$work/$archive"
    jq -nc --arg archive "$archive" --arg sha256 "$digest" --argjson size "$size" \
      '{key: $archive, value: {sha256: $sha256, size: $size}}' >>"$work/verified.jsonl"
  done <"$work/archives"
  jq --slurpfile verified "$work/verified.jsonl" '($verified | from_entries) as $assets |
    .platforms |= map(. + $assets[.archive])' "$manifest" >"$work/verified.json"
  manifest="$work/verified.json"
fi

staged=$(mktemp "$pin.XXXXXX")
go run ./cmd/atlas-pin $unsigned "$version" "$manifest" "$sig" >"$staged"
chmod 644 "$staged"
mv "$staged" "$pin"
