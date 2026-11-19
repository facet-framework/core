#!/usr/bin/env sh
# @facet: delivery/distribution/release-version-resolution
# @facet-role: primary
#
# The one place a FACET Core release version is computed. The release workflow,
# the packaging scripts and their tests all ask this script; none of them
# assembles a version string of its own.

set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
MANIFEST="${FACET_CORE_VERSION_FILE:-$ROOT/release/core-version.json}"

die() {
  echo "release-version: $*" >&2
  exit 2
}

# A SemVer numeric identifier: no leading zero, at most nine digits so shell
# arithmetic never overflows.
is_number() {
  case "$1" in
  '' | *[!0-9]* | 0?*) return 1 ;;
  esac
  [ "${#1}" -le 9 ]
}

# A real calendar date written as YYYYMMDD.
is_date() {
  case "$1" in
  [1-9][0-9][0-9][0-9][0-1][0-9][0-3][0-9]) ;;
  *) return 1 ;;
  esac
  _y=$(expr "$(printf '%s' "$1" | cut -c1-4)" + 0)
  _m=$(expr "$(printf '%s' "$1" | cut -c5-6)" + 0)
  _d=$(expr "$(printf '%s' "$1" | cut -c7-8)" + 0)
  [ "$_m" -ge 1 ] && [ "$_m" -le 12 ] && [ "$_d" -ge 1 ] || return 1
  case "$_m" in
  4 | 6 | 9 | 11) _max=30 ;;
  2)
    if [ $((_y % 4)) -eq 0 ] && { [ $((_y % 100)) -ne 0 ] || [ $((_y % 400)) -eq 0 ]; }; then
      _max=29
    else
      _max=28
    fi
    ;;
  *) _max=31 ;;
  esac
  [ "$_d" -le "$_max" ]
}

target() {
  [ -f "$MANIFEST" ] || die "cannot read $MANIFEST"
  _t=$(sed -n 's/^[[:space:]]*"target_version":[[:space:]]*"\([^"]*\)".*/\1/p' "$MANIFEST" | head -n 1)
  is_triple "$_t" || die "target_version in $MANIFEST is '$_t', not MAJOR.MINOR.PATCH"
  printf '%s\n' "$_t"
}

# MAJOR.MINOR.PATCH, each a numeric identifier (0 allowed).
is_triple() {
  case "$1" in
  *[!0-9.]* | '' | .* | *. | *..*) return 1 ;;
  esac
  _a=${1%%.*}
  _r=${1#*.}
  [ "$_r" != "$1" ] || return 1
  _b=${_r%%.*}
  _c=${_r#*.}
  [ "$_c" != "$_r" ] || return 1
  for _n in "$_a" "$_b" "$_c"; do
    [ "$_n" = 0 ] || is_number "$_n" || return 1
  done
}

# The kind of any version written in one of the three release shapes, whatever
# its base. Used for a version that is an explicit input (the Atlas
# distribution) rather than one computed here from target_version.
shape() {
  case "$1" in
  *+*) die "'$1' carries build metadata; archive names and tags use the version without it" ;;
  *-rc.*)
    is_triple "${1%%-rc.*}" && is_number "${1#*-rc.}" && [ "${1#*-rc.}" != 0 ] ||
      die "'$1' is not <MAJOR.MINOR.PATCH>-rc.<N>"
    echo rc
    ;;
  *-dev.*.*)
    _rest=${1#*-dev.}
    _date=${_rest%%.*}
    _count=${_rest#*.}
    is_triple "${1%%-dev.*}" && is_date "$_date" && is_number "$_count" && [ "$_count" != 0 ] ||
      die "'$1' is not <MAJOR.MINOR.PATCH>-dev.<YYYYMMDD>.<counter>"
    echo dev
    ;;
  *-*)
    die "'$1' has a pre-release that is neither rc.<N> nor dev.<YYYYMMDD>.<counter>"
    ;;
  *)
    is_triple "$1" || die "'$1' is not MAJOR.MINOR.PATCH"
    echo release
    ;;
  esac
}

[ "$#" -ge 1 ] || die "usage: release-version.sh dev <YYYYMMDD> <counter> | rc <N> | release | full <version> <commit> | kind <version> | shape <version> | channel <kind>"
command=$1
shift

case "$command" in
dev)
  [ "$#" -eq 2 ] || die "dev needs exactly a UTC date (YYYYMMDD) and a counter"
  is_date "$1" || die "dev date '$1' is not a calendar date written as YYYYMMDD"
  is_number "$2" || die "dev counter '$2' is not a positive integer without leading zeros"
  [ "$2" != 0 ] || die "dev counter must be positive"
  printf '%s-dev.%s.%s\n' "$(target)" "$1" "$2"
  ;;
rc)
  [ "$#" -eq 1 ] || die "rc needs exactly one number"
  is_number "$1" || die "rc number '$1' is not a positive integer without leading zeros"
  [ "$1" != 0 ] || die "rc number must be positive"
  printf '%s-rc.%s\n' "$(target)" "$1"
  ;;
release)
  [ "$#" -eq 0 ] || die "release takes no arguments"
  target
  ;;
full)
  [ "$#" -eq 2 ] || die "full needs a version and a 40-character commit"
  case "$2" in
  *[!0-9a-f]*) die "commit '$2' is not a lowercase hexadecimal object name" ;;
  esac
  [ "${#2}" -eq 40 ] || [ "${#2}" -eq 64 ] || die "commit '$2' is not a full object name"
  shape "$1" >/dev/null
  printf '%s+g%s\n' "$1" "$(printf '%s' "$2" | cut -c1-12)"
  ;;
shape)
  [ "$#" -eq 1 ] || die "shape needs exactly one version"
  shape "$1"
  ;;
kind)
  [ "$#" -eq 1 ] || die "kind needs exactly one version"
  t=$(target)
  case "$1" in
  "$t")
    echo release
    ;;
  "$t"-rc.*)
    is_number "${1#"$t"-rc.}" && [ "${1#"$t"-rc.}" != 0 ] ||
      die "'$1' is not a release candidate of $t"
    echo rc
    ;;
  "$t"-dev.*.*)
    _rest=${1#"$t"-dev.}
    _date=${_rest%%.*}
    _count=${_rest#*.}
    is_date "$_date" && is_number "$_count" && [ "$_count" != 0 ] ||
      die "'$1' is not a development version of $t (<target>-dev.<YYYYMMDD>.<counter>)"
    echo dev
    ;;
  *)
    die "'$1' is not a version this resolver produces for target $t"
    ;;
  esac
  ;;
channel)
  [ "$#" -eq 1 ] || die "channel needs exactly one kind"
  case "$1" in
  dev) echo dev ;;
  rc) echo rc ;;
  release) echo stable ;;
  *) die "kind '$1' is not dev, rc or release" ;;
  esac
  ;;
*)
  die "unknown command '$command'"
  ;;
esac
