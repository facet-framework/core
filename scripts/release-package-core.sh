#!/usr/bin/env sh
# @facet: delivery/distribution/core-release-packaging
# @facet-role: primary

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
# shellcheck source=release-layout.sh
. "$SCRIPT_DIR/release-layout.sh"
DEFAULT_PLATFORMS="linux_amd64 linux_arm64 darwin_amd64 darwin_arm64 windows_amd64 windows_arm64"

die() {
  echo "release-package-core: $*" >&2
  exit 2
}

archive_name() {
  case "$1" in
  linux_amd64 | linux_arm64 | darwin_amd64 | darwin_arm64) printf 'facet_%s_%s.tar.gz\n' "$2" "$1" ;;
  windows_amd64 | windows_arm64) printf 'facet_%s_%s.zip\n' "$2" "$1" ;;
  *) die "platform '$1' is not one of: $DEFAULT_PLATFORMS" ;;
  esac
}

if [ "${1:-}" = --archive-name ]; then
  [ "$#" -eq 3 ] || die "usage: release-package-core.sh --archive-name <platform> <version>"
  archive_name "$2" "$3"
  exit 0
fi

[ "$#" -ge 2 ] || die "usage: release-package-core.sh <version> <out-dir> [<platform> ...]"
VERSION=$1
OUT=$2
shift 2
PLATFORMS=${*:-$DEFAULT_PLATFORMS}

release_layout "$ROOT" || exit 2
KIND=$(sh "$SCRIPT_DIR/release-version.sh" kind "$VERSION") || exit 2
CHANNEL=$(sh "$SCRIPT_DIR/release-version.sh" channel "$KIND") || exit 2
for p in $PLATFORMS; do archive_name "$p" "$VERSION" >/dev/null; done

tar --version 2>/dev/null | head -n 1 | grep -q 'GNU tar' ||
  die "GNU tar is required for reproducible archives"
command -v gzip >/dev/null 2>&1 || die "gzip is required"
command -v zip >/dev/null 2>&1 || case " $PLATFORMS " in
*" windows_"*) die "zip is required for the Windows archives" ;;
esac

if [ -e "$OUT" ] && [ -n "$(ls -A "$OUT" 2>/dev/null)" ]; then
  die "$OUT already holds files; packaging writes into an empty directory only"
fi

LICENSE_SRC="$CORE_LICENSE_DIR/LICENSE"
NOTICE_SRC="$CORE_LICENSE_DIR/NOTICE"
for f in "$LICENSE_SRC" "$NOTICE_SRC"; do
  [ -f "$f" ] || die "$f is missing; Core is not packaged without its license and notice"
  ! grep -q '{{' "$f" || die "$f still holds a template placeholder; it is not a final text"
done
head -n 5 "$LICENSE_SRC" | grep -qx '[[:space:]]*Apache License[[:space:]]*' &&
  head -n 5 "$LICENSE_SRC" | grep -qx '[[:space:]]*Version 2\.0, January 2004[[:space:]]*' ||
  die "$LICENSE_SRC is not the Apache License 2.0 text (no 'Apache License' / 'Version 2.0, January 2004' heading); Core is Apache-2.0"

[ -f "$SCRIPT_DIR/release-notice-check.sh" ] || die "$SCRIPT_DIR/release-notice-check.sh is missing"
NOTICE_NAMES="THIRD_PARTY.json THIRD_PARTY_LICENSES"

# The clean-tree gate runs inside ldflags; nothing is built before it passed.
COMMIT=$(sh "$SCRIPT_DIR/release-stamp.sh" commit)
FULL=$(sh "$SCRIPT_DIR/release-version.sh" full "$VERSION" "$COMMIT") || exit 2
LDFLAGS=$(sh "$SCRIPT_DIR/release-stamp.sh" ldflags "$FULL" "$CHANNEL") || exit 2
BUILD_TIME=$(sh "$SCRIPT_DIR/release-stamp.sh" build-time)
EPOCH=$(git -C "$ROOT" log -1 --format=%ct HEAD)
TOUCH_STAMP=$(TZ=UTC git -C "$ROOT" log -1 --date=format-local:%Y%m%d%H%M.%S --format=%cd HEAD)

mkdir -p "$OUT"
OUT=$(CDPATH= cd -- "$OUT" && pwd)
WORK=$(mktemp -d "${TMPDIR:-/tmp}/facet-core-package.XXXXXX")
trap 'rm -rf "$WORK"' EXIT HUP INT TERM

if [ "$LAYOUT" = public ]; then
  NOTICES_DIR=$ROOT
else
  NOTICES_DIR="$WORK/notices"
  command -v python3 >/dev/null 2>&1 || die "python3 is required to collect the third-party notices"
  [ -f "$ROOT/tools/public-release/licenses.py" ] || die "$ROOT/tools/public-release/licenses.py is missing"
  (cd "$ROOT" && go list -deps "$CORE_MAIN_PACKAGE" >/dev/null) ||
    die "cannot resolve the dependencies of $CORE_MAIN_PACKAGE"
  python3 -B "$ROOT/tools/public-release/licenses.py" --output "$NOTICES_DIR" \
    --go-module "$ROOT" --go-package "$CORE_MAIN_PACKAGE" >"$WORK/notices.log" 2>&1 ||
    die "collecting the third-party notices of $CORE_MAIN_PACKAGE failed: $(tail -n 5 "$WORK/notices.log")"
fi
for n in $NOTICE_NAMES; do
  [ ! -L "$NOTICES_DIR/$n" ] || die "$NOTICES_DIR/$n is a symlink; notices are packaged as regular files only"
done
if [ -n "$(find "$NOTICES_DIR/THIRD_PARTY_LICENSES" -type l 2>/dev/null)" ]; then
  die "$NOTICES_DIR/THIRD_PARTY_LICENSES holds a symlink; notices are packaged as regular files only"
fi
# The inventory has to describe this build: a dependency or Go toolchain
# change without a new THIRD_PARTY.json stops here, before anything is built.
if [ -f "$NOTICES_DIR/THIRD_PARTY.json" ]; then
  [ -f "$SCRIPT_DIR/release-third-party-check.sh" ] || die "$SCRIPT_DIR/release-third-party-check.sh is missing"
  sh "$SCRIPT_DIR/release-third-party-check.sh" "$NOTICES_DIR" >"$WORK/third-party.log" 2>&1 ||
    die "THIRD_PARTY.json does not describe this build: $(cat "$WORK/third-party.log")"
fi

echo "release-package-core: $FULL ($CHANNEL) from $COMMIT, $CORE_MAIN_PACKAGE ($LAYOUT layout)"
for platform in $PLATFORMS; do
  goos=${platform%_*}
  goarch=${platform#*_}
  binary=facet
  [ "$goos" != windows ] || binary=facet.exe
  stage="$WORK/$platform"
  mkdir -p "$stage/schema"
  (cd "$ROOT" && CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build -trimpath -buildvcs=false -ldflags "$LDFLAGS" -o "$stage/$binary" "$CORE_MAIN_PACKAGE") ||
    die "go build $CORE_MAIN_PACKAGE failed for $platform"
  cp "$LICENSE_SRC" "$stage/LICENSE"
  cp "$NOTICE_SRC" "$stage/NOTICE"
  cp "$ROOT/README.md" "$ROOT/CHANGELOG.md" "$stage/"
  cp "$ROOT"/schema/*.json "$stage/schema/"
  chmod 0755 "$stage/$binary" "$stage/schema"
  chmod 0644 "$stage/LICENSE" "$stage/NOTICE" "$stage/README.md" "$stage/CHANGELOG.md" "$stage"/schema/*.json
  [ ! -f "$NOTICES_DIR/THIRD_PARTY.json" ] || cp "$NOTICES_DIR/THIRD_PARTY.json" "$stage/THIRD_PARTY.json"
  [ ! -d "$NOTICES_DIR/THIRD_PARTY_LICENSES" ] || cp -R "$NOTICES_DIR/THIRD_PARTY_LICENSES" "$stage/THIRD_PARTY_LICENSES"
  find "$stage" -path "$stage/THIRD_PARTY*" -type d -exec chmod 0755 {} +
  find "$stage" -path "$stage/THIRD_PARTY*" -type f -exec chmod 0644 {} +
  sh "$SCRIPT_DIR/release-notice-check.sh" "$stage" >"$WORK/notice-check.log" 2>&1 ||
    die "the $platform archive would not carry what its NOTICE refers to: $(cat "$WORK/notice-check.log")"
  # TOUCH_STAMP is the commit time written in UTC, and touch -t reads its
  # argument in the local zone: both must agree, or a zip packaged in another
  # timezone records different DOS times and different bytes.
  TZ=UTC find "$stage" -exec touch -h -t "$TOUCH_STAMP" {} +

  archive=$(archive_name "$platform" "$VERSION")
  # Members named without a ./ prefix, sorted, files only: the binary is the
  # archive-root member the dispatcher extracts by name.
  (cd "$stage" && find . -type f) >"$WORK/$platform.found" || die "cannot list $stage"
  sed 's|^\./||' "$WORK/$platform.found" | LC_ALL=C sort >"$WORK/$platform.members"
  if [ "$goos" = windows ]; then
    # No extra attributes, no directory entries, DOS times read in UTC.
    (cd "$stage" && TZ=UTC zip -X -q -D "$OUT/$archive" -@ <"$WORK/$platform.members") ||
      die "zip failed for $platform"
  else
    tar --owner=0 --group=0 --numeric-owner --mtime="@$EPOCH" --format=gnu --no-recursion \
      -cf "$WORK/$platform.tar" -C "$stage" -T "$WORK/$platform.members" || die "tar failed for $platform"
    gzip -n -9 -c "$WORK/$platform.tar" >"$OUT/$archive" || die "gzip failed for $platform"
  fi
  echo "release-package-core: $OUT/$archive"
done

: >"$WORK/checksums.txt"
for platform in $PLATFORMS; do
  archive=$(archive_name "$platform" "$VERSION")
  sum=$(sha256sum "$OUT/$archive" | cut -d' ' -f1)
  [ -n "$sum" ] || die "cannot hash $OUT/$archive"
  printf '%s  %s\n' "$sum" "$archive" >>"$WORK/checksums.txt"
done
LC_ALL=C sort -k2 "$WORK/checksums.txt" >"$OUT/checksums.txt"

sh "$SCRIPT_DIR/release-manifest.sh" "$OUT" core "$VERSION" "$FULL" "$CHANNEL" "$COMMIT" "$BUILD_TIME"
