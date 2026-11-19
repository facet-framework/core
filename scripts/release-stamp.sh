#!/usr/bin/env sh
# @facet: delivery/distribution/release-version-resolution
# @facet-role: related
#
# The provenance a release build writes into its binary, measured here once and
# used by every release build.

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)

die() {
  echo "release-stamp: $*" >&2
  exit 2
}

# Subcommands that only the development layout has. The public layout stops
# before any of their code runs.
development_only() {
  # shellcheck source=release-layout.sh
  . "$SCRIPT_DIR/release-layout.sh"
  release_layout "$ROOT" || exit 2
  [ "$LAYOUT" = development ] ||
    die "$1 belongs to the development layout; the $LAYOUT layout releases Core only and never runs it"
}

distribution() {
  printf 'github-%s\n' "$1" | tr '[:upper:]' '[:lower:]' | sed 's/[^a-z0-9._-]/-/g'
}

repo() {
  _repo="${FACET_RELEASE_REPO:-${GITHUB_REPOSITORY:-}}"
  case "$_repo" in
  */*) ;;
  *) die "set FACET_RELEASE_REPO (or GITHUB_REPOSITORY) to <owner>/<repo>; a release build does not guess its source" ;;
  esac
  printf '%s\n' "$_repo"
}

clean() {
  git -C "$ROOT" rev-parse --verify HEAD >/dev/null 2>&1 ||
    die "$ROOT is not a git checkout with a commit; the source state cannot be established"
  _status=$(git -C "$ROOT" status --porcelain --untracked-files=all) ||
    die "git status failed in $ROOT; the source state cannot be established"
  if [ -n "$_status" ]; then
    printf '%s\n' "$_status" >&2
    die "the working tree is dirty; a release build is made from a clean commit only"
  fi
}

commit() {
  git -C "$ROOT" rev-parse --verify HEAD
}

build_time() {
  _t=$(TZ=UTC git -C "$ROOT" log -1 --date=format-local:%Y-%m-%dT%H:%M:%SZ --format=%cd HEAD)
  [ -n "$_t" ] || die "cannot read the commit time of HEAD"
  printf '%s\n' "$_t"
}

module() {
  _m=$(sed -n 's/^module[[:space:]]*//p' "$ROOT/go.mod" | head -n 1)
  [ -n "$_m" ] || die "cannot read the module path from $ROOT/go.mod"
  printf '%s\n' "$_m"
}

[ "$#" -ge 1 ] || die "usage: release-stamp.sh distribution <owner>/<repo> | clean | commit | build-time | ldflags <full-version> <channel>"
command=$1
shift
case "$command" in
distribution)
  [ "$#" -eq 1 ] || die "distribution needs <owner>/<repo>"
  distribution "$1"
  ;;
clean)
  [ "$#" -eq 0 ] || die "clean takes no arguments"
  clean
  ;;
commit)
  commit
  ;;
build-time)
  build_time
  ;;
ldflags)
  [ "$#" -eq 2 ] || die "ldflags needs a full version and a channel"
  case "$2" in
  dev | rc | stable) ;;
  *) die "channel '$2' is not dev, rc or stable" ;;
  esac
  case "$1" in
  *+g[0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f]) ;;
  *) die "full version '$1' does not end in +g<12 hex>" ;;
  esac
  _repo=$(repo)
  clean
  _commit=$(commit)
  case "$1" in
  *+g"$(printf '%s' "$_commit" | cut -c1-12)") ;;
  *) die "full version '$1' names a different commit than HEAD ($_commit)" ;;
  esac
  _pkg="$(module)/release"
  printf '%s\n' "-s -w -X main.version=$1 -X $_pkg.Distribution=$(distribution "$_repo") -X $_pkg.SourceCommit=$_commit -X $_pkg.Dirty=false -X $_pkg.BuildTime=$(build_time) -X $_pkg.BuildChannel=$2"
  ;;
reset-submodules)
  development_only reset-submodules
  # Leaves no submodule state from an earlier job in a reused workspace, so
  # `clean` judges only this commit: deinitialised for core, reset to the
  # committed gitlink for atlas.
  [ "$#" -eq 1 ] || die "reset-submodules needs a product"
  case "$1" in
  core)
    if [ -f "$ROOT/.gitmodules" ]; then
      git -C "$ROOT" submodule deinit --force --all >/dev/null ||
        die "git submodule deinit failed"
    fi
    ;;
  atlas)
    git -C "$ROOT" submodule foreach --quiet 'git clean -ffdxq && git reset -q --hard' ||
      die "could not reset the submodules"
    ;;
  *) die "product '$1' is not core or atlas" ;;
  esac
  git -C "$ROOT" status --porcelain
  ;;
atlas-env)
  development_only atlas-env
  # <atlas version> <core full version>: NAME=value lines for $GITHUB_ENV.
  # The channel stamped is the Core version's, never the Atlas version's:
  # release.BuildChannel is the channel of the Core identity that
  # `facet version` reports beside the Core version.
  [ "$#" -eq 2 ] || die "atlas-env needs an Atlas version and the Core full version"
  sh "$ROOT/scripts/release-version.sh" shape "$1" >/dev/null || exit 2
  _core_kind=$(sh "$ROOT/scripts/release-version.sh" kind "${2%%+*}") || exit 2
  _channel=$(sh "$ROOT/scripts/release-version.sh" channel "$_core_kind") || exit 2
  _ui=$(sed -n 's/^[[:space:]]*"version":[[:space:]]*"\([^"]*\)".*/\1/p' "$ROOT/web/facet-ui/package.json" 2>/dev/null | head -n 1)
  [ -n "$_ui" ] || die "cannot read the UI package version required by this build layout"
  _flags=$(sh "$0" ldflags "$2" "$_channel") || exit 2
  printf 'FACET_ATLAS_VERSION=%s\n' "$1"
  printf 'FACET_RELEASE_LDFLAGS=%s -X main.atlasVersion=%s -X main.uiVersion=%s\n' "$_flags" "$1" "$_ui"
  ;;
*)
  die "unknown command '$command'"
  ;;
esac
