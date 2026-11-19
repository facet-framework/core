#!/usr/bin/env sh
# @facet: delivery/distribution/release-manifest
# @facet-role: primary

set -eu

die() {
  echo "release-manifest: $*" >&2
  exit 2
}

[ "$#" -eq 7 ] || die "usage: release-manifest.sh <dist> <product> <version> <full-version> <channel> <commit> <build-time>"
DIST=$1
PRODUCT=$2
VERSION=$3
FULL=$4
CHANNEL=$5
COMMIT=$6
BUILD_TIME=$7

command -v jq >/dev/null 2>&1 || die "jq is required"
case "$PRODUCT" in
core) PREFIX=facet ;;
atlas) PREFIX=facet-atlas ;;
*) die "product '$PRODUCT' is not core or atlas" ;;
esac
case "$CHANNEL" in dev | rc | stable) ;; *) die "channel '$CHANNEL' is not dev, rc or stable" ;; esac
case "$COMMIT" in
*[!0-9a-f]* | '') die "commit '$COMMIT' is not a lowercase object name" ;;
esac
[ "${#COMMIT}" -eq 40 ] || [ "${#COMMIT}" -eq 64 ] || die "commit '$COMMIT' is not a full object name"
[ "$FULL" = "$VERSION+g$(printf '%s' "$COMMIT" | cut -c1-12)" ] ||
  die "full version '$FULL' is not $VERSION+g<first 12 of $COMMIT>"
case "$BUILD_TIME" in
[0-9][0-9][0-9][0-9]-[0-1][0-9]-[0-3][0-9]T[0-2][0-9]:[0-5][0-9]:[0-6][0-9]Z) ;;
*) die "build time '$BUILD_TIME' is not RFC3339 UTC (YYYY-MM-DDThh:mm:ssZ)" ;;
esac
[ -f "$DIST/checksums.txt" ] || die "$DIST/checksums.txt does not exist"

WORK=$(mktemp -d "${TMPDIR:-/tmp}/facet-release-manifest.XXXXXX")
trap 'rm -rf "$WORK"' EXIT HUP INT TERM

: >"$WORK/platforms.jsonl"
listed=0
for path in "$DIST/${PREFIX}_${VERSION}_"*; do
  [ -e "$path" ] || continue
  name=${path##*/}
  case "$name" in
  *.tar.gz) ext=tar.gz ;;
  *.zip) ext=zip ;;
  *) die "$name is neither a tar.gz nor a zip" ;;
  esac
  platform=${name#"${PREFIX}_${VERSION}_"}
  platform=${platform%".$ext"}
  case "$platform" in
  linux_amd64 | linux_arm64 | darwin_amd64 | darwin_arm64) want=tar.gz binary=facet ;;
  windows_amd64 | windows_arm64) want=zip binary=facet.exe ;;
  *) die "$name names no known platform" ;;
  esac
  [ "$ext" = "$want" ] || die "$name is a $ext, but $platform archives are $want"

  expected=$(awk -v n="$name" '$2 == n { print $1 }' "$DIST/checksums.txt")
  [ -n "$expected" ] || die "$name is not listed in checksums.txt"
  [ "$(printf '%s\n' "$expected" | wc -l)" -eq 1 ] || die "$name is listed more than once in checksums.txt"
  archive_sum=$(sha256sum "$path" | cut -d' ' -f1)
  [ "$archive_sum" = "$expected" ] || die "$name hashes to $archive_sum, checksums.txt says $expected"

  out="$WORK/$platform"
  mkdir "$out"
  if [ "$ext" = zip ]; then
    unzip -q "$path" "$binary" -d "$out" || die "$name has no member $binary at its root"
  else
    tar -xzf "$path" -C "$out" "$binary" || die "$name has no member $binary at its root"
  fi
  [ -f "$out/$binary" ] || die "$name did not yield $binary"
  binary_sum=$(sha256sum "$out/$binary" | cut -d' ' -f1)
  [ -n "$binary_sum" ] || die "cannot hash $binary from $name"

  jq -n -c --arg platform "$platform" --arg archive "$name" --arg archive_sha256 "$archive_sum" \
    --arg binary "$binary" --arg binary_sha256 "$binary_sum" \
    '{platform: $platform, os: ($platform | split("_")[0]), arch: ($platform | split("_")[1]),
      archive: $archive, archive_sha256: $archive_sha256, binary: $binary, binary_sha256: $binary_sha256}' \
    >>"$WORK/platforms.jsonl"
  listed=$((listed + 1))
done
[ "$listed" -gt 0 ] || die "no ${PREFIX}_${VERSION}_* archive in $DIST"

# Every line in checksums.txt for this product and version names a file that was hashed above.
for name in $(awk '{ print $2 }' "$DIST/checksums.txt"); do
  case "$name" in
  "${PREFIX}_${VERSION}_"*) [ -f "$DIST/$name" ] || die "checksums.txt lists $name, which is not in $DIST" ;;
  esac
done

jq -s --arg product "$PRODUCT" --arg version "$VERSION" --arg full "$FULL" --arg channel "$CHANNEL" \
  --arg commit "$COMMIT" --arg build_time "$BUILD_TIME" \
  '{manifest_version: 1, product: $product, version: $version, full_version: $full, channel: $channel,
    source_commit: $commit, build_time: $build_time, platforms: (sort_by(.platform))}' \
  "$WORK/platforms.jsonl" >"$WORK/release-manifest.json"
mv "$WORK/release-manifest.json" "$DIST/release-manifest.json"
echo "release-manifest: $DIST/release-manifest.json ($listed platforms)"
