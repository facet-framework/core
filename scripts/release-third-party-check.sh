#!/usr/bin/env sh
# @facet: delivery/distribution/core-release-packaging
# @facet-role: related
#
# Fails unless THIRD_PARTY.json lists exactly the Go modules the facet command
# compiles on the six release platforms, the Go toolchain of this build, and
# every notice file with its SHA-256.

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
# shellcheck source=release-layout.sh
. "$SCRIPT_DIR/release-layout.sh"
PLATFORMS="linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64"

die() {
  echo "release-third-party-check: $*" >&2
  exit 2
}

NOTICES=${1:-$ROOT}
INVENTORY="$NOTICES/THIRD_PARTY.json"
command -v go >/dev/null 2>&1 || die "go is required"
command -v jq >/dev/null 2>&1 || die "jq is required"
release_layout "$ROOT" || exit 2
[ -s "$INVENTORY" ] || die "$INVENTORY is missing or empty"
jq -e '.dependencies | type == "array"' "$INVENTORY" >/dev/null 2>&1 || die "$INVENTORY has no dependencies list"

WORK=$(mktemp -d "${TMPDIR:-/tmp}/facet-third-party-check.XXXXXX")
trap 'rm -rf "$WORK"' EXIT HUP INT TERM
failed=0

# The compiled closure, platform by platform: one line per non-main module,
# path and version, and the platform that links it.
: >"$WORK/compiled.raw"
for platform in $PLATFORMS; do
  (cd "$ROOT" && CGO_ENABLED=0 GOOS=${platform%/*} GOARCH=${platform#*/} GOFLAGS=-mod=readonly \
    go list -deps -f '{{with .Module}}{{if not .Main}}{{.Path}} {{.Version}}{{if .Replace}} replaced{{end}}{{end}}{{end}}' \
    "$CORE_MAIN_PACKAGE") >"$WORK/list.out" 2>"$WORK/list.err" ||
    die "go list cannot resolve $CORE_MAIN_PACKAGE for $platform: $(tail -n 5 "$WORK/list.err")"
  sed '/^$/d' "$WORK/list.out" | sort -u | sed "s|\$| $platform|" >>"$WORK/compiled.raw"
done
if grep ' replaced ' "$WORK/compiled.raw" >"$WORK/replaced"; then
  while read -r module version _ platform; do
    echo "release-third-party-check: $module $version is replaced in go.mod ($platform); a release links versioned modules only" >&2
  done <"$WORK/replaced"
  failed=1
fi
cut -d' ' -f1,2 "$WORK/compiled.raw" | LC_ALL=C sort -u >"$WORK/compiled"
jq -r '.dependencies[] | select(.ecosystem == "Go") | "\(.name) \(.version)"' "$INVENTORY" | LC_ALL=C sort -u >"$WORK/listed"

LC_ALL=C comm -23 "$WORK/compiled" "$WORK/listed" >"$WORK/unlisted"
while read -r module version; do
  where=$(awk -v m="$module" -v v="$version" '$1 == m && $2 == v { print $NF }' "$WORK/compiled.raw" | tr '\n' ' ')
  echo "release-third-party-check: $module $version is compiled into $CORE_MAIN_PACKAGE (${where% }) but not listed in THIRD_PARTY.json" >&2
  failed=1
done <"$WORK/unlisted"
LC_ALL=C comm -13 "$WORK/compiled" "$WORK/listed" >"$WORK/stale"
while read -r module version; do
  echo "release-third-party-check: THIRD_PARTY.json lists $module $version, which no release platform compiles into $CORE_MAIN_PACKAGE" >&2
  failed=1
done <"$WORK/stale"

# The toolchain: exactly one entry, and the Go that builds now.
toolchains=$(jq -r '[.dependencies[] | select(.ecosystem == "GoToolchain") | .version] | join(" ")' "$INVENTORY")
current=$(cd "$ROOT" && go env GOVERSION)
case "$toolchains" in
"$current") ;;
"")
  echo "release-third-party-check: THIRD_PARTY.json lists no Go toolchain; the standard library's notices are not attributed" >&2
  failed=1
  ;;
*" "*)
  echo "release-third-party-check: THIRD_PARTY.json lists more than one Go toolchain ($toolchains)" >&2
  failed=1
  ;;
*)
  echo "release-third-party-check: THIRD_PARTY.json was collected with $toolchains, but this build uses $current; build with $toolchains or collect the inventory again with $current" >&2
  failed=1
  ;;
esac

# Every listed notice file, present with its hash.
jq -r '.dependencies[].verbatim_files[] | "\(.sha256)  \(.path)"' "$INVENTORY" >"$WORK/sums"
[ -s "$WORK/sums" ] || {
  echo "release-third-party-check: THIRD_PARTY.json names no notice file" >&2
  failed=1
}
if ! (cd "$NOTICES" && sha256sum --check --strict --quiet -) <"$WORK/sums" >"$WORK/sums.out" 2>&1; then
  sed 's/^/release-third-party-check: /' "$WORK/sums.out" >&2
  failed=1
fi

[ "$failed" -eq 0 ] || exit 1
echo "release-third-party-check: THIRD_PARTY.json lists the $(wc -l <"$WORK/compiled") modules $CORE_MAIN_PACKAGE compiles on the six release platforms and the $current toolchain, and every notice it names is present"
