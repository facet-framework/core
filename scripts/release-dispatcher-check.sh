#!/usr/bin/env sh
# @facet: delivery/distribution/public-core-release
# @facet-role: related

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)

die() {
  echo "release-dispatcher-check: $*" >&2
  exit 2
}

PINNED=
if [ "${1:-}" = --version ]; then
  [ "$#" -ge 2 ] || die "--version needs a value"
  PINNED=$2
  shift 2
fi
[ "$#" -eq 2 ] || die "usage: release-dispatcher-check.sh [--version <version>] <dist> <owner>/<repo>"
DIST=$(CDPATH= cd -- "$1" && pwd) || die "cannot enter $1"
REPO=$2
case "$REPO" in */*) ;; *) die "'$REPO' is not <owner>/<repo>" ;; esac
command -v jq >/dev/null 2>&1 || die "jq is required"
MANIFEST="$DIST/release-manifest.json"
[ -f "$MANIFEST" ] || die "$MANIFEST does not exist"
[ -f "$DIST/checksums.txt" ] || die "$DIST/checksums.txt does not exist"
VERSION=$(jq -r .version "$MANIFEST")
[ -z "$PINNED" ] || [ "$PINNED" = "$VERSION" ] || die "--version $PINNED, but $MANIFEST is version $VERSION"

WORK=$(mktemp -d "${TMPDIR:-/tmp}/facet-dispatcher-check.XXXXXX")
trap 'rm -rf "$WORK"' EXIT HUP INT TERM
mkdir "$WORK/shims" "$WORK/home"

# The dispatcher's own platform and naming rules, lifted out of bin/facet.
sed -n '/^detect_platform() {/,/^}/p; /^archive_shape() {/,/^}/p; /^distribution_for_repo() {/,/^}/p' \
  "$ROOT/bin/facet" >"$WORK/rules.sh"
# shellcheck disable=SC1091
. "$WORK/rules.sh"
command -v archive_shape >/dev/null 2>&1 && command -v distribution_for_repo >/dev/null 2>&1 ||
  die "bin/facet must define detect_platform, archive_shape and distribution_for_repo"
PLATFORM=$(detect_platform)
archive_shape "$PLATFORM"
ARCHIVE="facet_${VERSION}_${PLATFORM}.${ARCHIVE_EXT}"
DISTRIBUTION=$(distribution_for_repo "$REPO")
[ -f "$DIST/$ARCHIVE" ] || die "$DIST has no $ARCHIVE for this host; package $PLATFORM first"
PREFIX="https://github.com/$REPO/releases/download/v$VERSION/"

# Serves $PREFIX<file> from the dist directory; anything else is a 404 (22).
cat >"$WORK/shims/curl" <<SH
#!/bin/sh
out= url=
while [ \$# -gt 0 ]; do
  case "\$1" in
    -o) out=\$2; shift 2 ;;
    -H) shift 2 ;;
    -*) shift ;;
    *) url=\$1; shift ;;
  esac
done
printf '%s\n' "\$url" >> "$WORK/requests"
[ -n "\${FACET_CHECK_OFFLINE:-}" ] && exit 6
case "\$url" in
  "$PREFIX"*) name=\${url#"$PREFIX"} ;;
  *) exit 22 ;;
esac
case "\$name" in */* | '') exit 22 ;; esac
[ -f "$DIST/\$name" ] || exit 22
cp "$DIST/\$name" "\$out"
SH
printf '#!/bin/sh\nexit 1\n' >"$WORK/shims/gh"
printf '#!/bin/sh\nexit 1\n' >"$WORK/shims/wget"
chmod +x "$WORK/shims/curl" "$WORK/shims/gh" "$WORK/shims/wget"

# Only the system directories after the stand-ins: no facet, no go and no
# other dispatcher input from the caller's PATH or environment.
SAFE_PATH="$WORK/shims:/usr/bin:/bin:/usr/sbin:/sbin"
PATH=$SAFE_PATH command -v facet >/dev/null 2>&1 && die "a facet on $SAFE_PATH would be chosen before the cache; the check cannot run here"

run_dispatcher() {
  if [ -n "$PINNED" ]; then
    env -i PATH="$SAFE_PATH" HOME="$WORK/home" XDG_CACHE_HOME="$WORK/cache" FACET_REPOS="$REPO" \
      FACET_VERSION="$PINNED" ${FACET_CHECK_OFFLINE:+FACET_CHECK_OFFLINE=1} \
      sh "$ROOT/bin/facet" version --json
  else
    env -i PATH="$SAFE_PATH" HOME="$WORK/home" XDG_CACHE_HOME="$WORK/cache" FACET_REPOS="$REPO" \
      ${FACET_CHECK_OFFLINE:+FACET_CHECK_OFFLINE=1} \
      sh "$ROOT/bin/facet" version --json
  fi
}

FAILED=0
fail() {
  printf 'FAIL %s\n' "$*" >&2
  FAILED=1
}
pass() { printf 'ok   %s\n' "$*"; }

: >"$WORK/requests"
run_dispatcher >"$WORK/first.json" 2>"$WORK/first.err" ||
  die "the dispatcher did not install and run $VERSION: $(cat "$WORK/first.err")"
printf '%s%s\n%schecksums.txt\n' "$PREFIX" "$ARCHIVE" "$PREFIX" >"$WORK/want-requests"
if cmp -s "$WORK/requests" "$WORK/want-requests"; then
  pass "the dispatcher asked for $ARCHIVE and checksums.txt under tag v$VERSION and nothing else"
else
  fail "requests: $(tr '\n' ' ' <"$WORK/requests") (want $(tr '\n' ' ' <"$WORK/want-requests"))"
fi

if jq -e --arg v "$VERSION" --arg d "$DISTRIBUTION" --slurpfile m "$MANIFEST" '
  .product == "core" and (.version | startswith($v + "+g")) and .version == $m[0].full_version
  and .distribution == $d and .channel == $m[0].channel and .source_commit == $m[0].source_commit
  and .build_time == $m[0].build_time and .dirty == false and .install.origin == "facet-cache"' \
  "$WORK/first.json" >/dev/null; then
  pass "the installed binary reports $(jq -r .version "$WORK/first.json"), $DISTRIBUTION, channel $(jq -r .channel "$WORK/first.json"), its commit and build time, from the cache"
else
  fail "version --json: $(cat "$WORK/first.json") (want $VERSION, $DISTRIBUTION and the values of release-manifest.json)"
fi

INSTALL="$WORK/cache/facet/dist/core/$DISTRIBUTION/$PLATFORM/$VERSION"
want_sum=$(jq -r --arg p "$PLATFORM" '.platforms[] | select(.platform == $p) | .binary_sha256' "$MANIFEST")
if [ -x "$INSTALL/$BINARY_NAME" ] && [ -f "$INSTALL/install.json" ] &&
  grep -q "\"version\": *\"$VERSION\"" "$INSTALL/install.json" &&
  grep -q "\"binary_sha256\": *\"$want_sum\"" "$INSTALL/install.json" &&
  [ "$(readlink "$WORK/cache/facet/dist/core/$DISTRIBUTION/$PLATFORM/current")" = "$VERSION" ] &&
  [ "$(readlink "$WORK/cache/facet/dist/core/selected")" = "$DISTRIBUTION/$PLATFORM/current" ]; then
  pass "the cache holds dist/core/$DISTRIBUTION/$PLATFORM/$VERSION with the manifest's binary, install.json, current and selected"
else
  fail "cache layout under $WORK/cache/facet/dist/core: $(cd "$WORK/cache/facet/dist/core" 2>/dev/null && find . | sort | tr '\n' ' ') install.json: $(cat "$INSTALL/install.json" 2>/dev/null)"
fi

: >"$WORK/requests"
if FACET_CHECK_OFFLINE=1 run_dispatcher >"$WORK/second.json" 2>"$WORK/second.err" &&
  [ ! -s "$WORK/requests" ] && cmp -s "$WORK/first.json" "$WORK/second.json"; then
  pass "a second run with every download failing is served from the cache, with no request"
else
  fail "offline second run: requests $(tr '\n' ' ' <"$WORK/requests"); $(cat "$WORK/second.err") $(cat "$WORK/second.json")"
fi

[ "$FAILED" -eq 0 ] || die "the packaged release does not install through bin/facet as published"
echo "release-dispatcher-check: $VERSION for $PLATFORM installs through bin/facet from $REPO"
