#!/usr/bin/env sh
# @facet: delivery/distribution/core-release-packaging
# @facet-role: related
#
# The release workflow's writes to the repository, each one a subcommand so it
# can be run and tested outside the YAML.

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
# shellcheck source=release-layout.sh
. "$SCRIPT_DIR/release-layout.sh"

die() {
  echo "release-publish: $*" >&2
  exit 2
}

release_layout "$ROOT" || exit 2

valid_tag() {
  if [ "$LAYOUT" = public ]; then
    case "$1" in
    "$CORE_TAG_PREFIX"[0-9]*) ;;
    *) die "'$1' is not a $CORE_TAG_PREFIX<version> tag; the public Core repository releases Core only" ;;
    esac
    return 0
  fi
  case "$1" in
  "$CORE_TAG_PREFIX"?* | "$ATLAS_TAG_PREFIX"?*) ;;
  *) die "'$1' is not a $CORE_TAG_PREFIX or $ATLAS_TAG_PREFIX tag" ;;
  esac
}

# The tag a product version is released under in this layout.
tag_for() {
  case "$1:$LAYOUT" in
  core:*) printf '%s%s\n' "$CORE_TAG_PREFIX" "$2" ;;
  atlas:development) printf '%s%s\n' "$ATLAS_TAG_PREFIX" "$2" ;;
  *) die "product $1 is not released from the $LAYOUT layout" ;;
  esac
}

# The commit the tag names on origin, or nothing when origin has no such tag.
# `git ls-remote --exit-code` exits 2 for "no matching ref"; anything else
# non-zero is a failure to ask, and that is never read as "free".
remote_tag_commit() {
  # The trailing * is what makes ls-remote list the peeled <tag>^{} line; it
  # also matches longer names (rc.1 → rc.10), which the exact awk match drops.
  _out=$(git -C "$ROOT" ls-remote --exit-code --tags origin "refs/tags/$1*") && _rc=0 || _rc=$?
  case "$_rc" in
  0) ;;
  2) return 0 ;;
  *) die "git ls-remote origin failed (exit $_rc); cannot tell whether $1 exists" ;;
  esac
  # An annotated tag lists the peeled commit as <tag>^{}; a lightweight one
  # lists the commit itself.
  _peeled=$(printf '%s\n' "$_out" | awk -v r="refs/tags/$1^{}" '$2 == r { print $1 }')
  if [ -n "$_peeled" ]; then
    printf '%s\n' "$_peeled"
  else
    printf '%s\n' "$_out" | awk -v r="refs/tags/$1" '$2 == r { print $1 }'
  fi
}

# absent | draft | published, from gh. gh exits 1 both for "release not found"
# and for real failures, so the message decides; an unknown failure stops.
release_state() {
  _errfile=$(mktemp "${TMPDIR:-/tmp}/facet-gh-view.XXXXXX") || die "mktemp failed"
  if _state=$(gh release view "$1" --repo "$2" --json isDraft --jq .isDraft 2>"$_errfile"); then
    rm -f "$_errfile"
    case "$_state" in
    true) echo draft ;;
    false) echo published ;;
    *) die "gh release view $1 answered '$_state'" ;;
    esac
  else
    _err=$(cat "$_errfile" 2>/dev/null || true)
    rm -f "$_errfile"
    case "$_err" in
    *"release not found"*) echo absent ;;
    *) die "gh release view $1 failed: $_err" ;;
    esac
  fi
}

# Prints "free" or "resume", or fails. Shared by check-tag and prepare-tag.
tag_decision() {
  _tag=$1
  _repo=$2
  # Called inside $(...) || exit, where set -e does not hold: every step that
  # can fail is checked by hand.
  _head=$(git -C "$ROOT" rev-parse HEAD) || exit 2
  _remote=$(remote_tag_commit "$_tag") || exit 2
  _local=$(git -C "$ROOT" rev-parse -q --verify "refs/tags/$_tag^{commit}" || true)
  if [ -z "$_remote" ]; then
    if [ -n "$_local" ] && [ "$_local" != "$_head" ]; then
      die "tag $_tag exists here on $_local, not on HEAD $_head"
    fi
    echo free
    return 0
  fi
  [ "$_remote" = "$_head" ] ||
    die "tag $_tag exists on origin for commit $_remote, not HEAD $_head; a version is built once"
  _state=$(release_state "$_tag" "$_repo") || exit 2
  [ "$_state" != published ] ||
    die "tag $_tag already has a published release; a version is built once"
  echo resume
}

[ "$#" -ge 1 ] || die "usage: release-publish.sh check-tag <tag> <repo> | prepare-tag <tag> <title> <repo> | release <product> <kind> <version> <tag> <dist> <repo>"
command=$1
shift
case "$command" in
check-tag)
  [ "$#" -eq 2 ] || die "check-tag needs a tag and a repository"
  valid_tag "$1"
  decision=$(tag_decision "$1" "$2") || exit 2
  case "$decision" in
  free) echo "release-publish: $1 is free" ;;
  resume) echo "release-publish: $1 is already on origin at HEAD with no published release; this run resumes it" ;;
  esac
  ;;
prepare-tag)
  [ "$#" -eq 3 ] || die "prepare-tag needs a tag, a title and a repository"
  valid_tag "$1"
  if [ "$LAYOUT" = public ]; then
    sh "$SCRIPT_DIR/release-pr-check.sh" "${1#v}" "$3" || exit 2
  fi
  decision=$(tag_decision "$1" "$3") || exit 2
  if [ "$decision" = resume ]; then
    echo "release-publish: $1 already points at HEAD on origin; not creating it again"
    exit 0
  fi
  git -C "$ROOT" rev-parse -q --verify "refs/tags/$1" >/dev/null || git -C "$ROOT" tag -a "$1" -m "$2"
  git -C "$ROOT" push origin "refs/tags/$1"
  ;;
release)
  [ "$#" -eq 6 ] || die "release needs <product> <kind> <version> <tag> <dist> <repo>"
  product=$1 kind=$2 version=$3 tag=$4 dist=$5 repo=$6
  valid_tag "$tag"
  [ "$tag" = "$(tag_for "$product" "$version")" ] || die "tag $tag does not name $product $version"
  case "$product" in
  core) prefix=facet title="FACET Core $version" ;;
  atlas) prefix=facet-atlas title="FACET Atlas $version" ;;
  *) die "product '$product' is not core or atlas" ;;
  esac
  [ -f "$dist/checksums.txt" ] || die "$dist/checksums.txt is missing"
  [ -f "$dist/release-manifest.json" ] || die "$dist/release-manifest.json is missing"
  set --
  for f in "$dist/${prefix}_${version}_"*; do
    [ -f "$f" ] && set -- "$@" "$f"
  done
  [ "$#" -gt 0 ] || die "no ${prefix}_${version}_* archive in $dist"
  set -- "$@" "$dist/checksums.txt" "$dist/release-manifest.json"

  state=$(release_state "$tag" "$repo") || exit 2
  [ "$state" != published ] || die "$tag already has a published release; a version is built once"

  notes=$(mktemp "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/facet-release-notes.XXXXXX")
  trap 'rm -f "$notes"' EXIT HUP INT TERM
  commit=$(git -C "$ROOT" rev-parse HEAD)
  if [ "$LAYOUT" = public ]; then
    sh "$SCRIPT_DIR/release-changelog.sh" notes "$version" "$ROOT/CHANGELOG.md" >"$notes" ||
      die "CHANGELOG.md holds no release notes for $version; nothing was created"
  elif [ "$product" = core ] && [ "$kind" != dev ]; then
    target=$(sh "$SCRIPT_DIR/release-version.sh" release)
    start=$(grep -n "^## \[$target\]" "$ROOT/CHANGELOG.md" | head -n 1 | cut -d: -f1)
    [ -n "$start" ] || die "CHANGELOG.md has no [$target] section"
    next=$(awk -v from="$((start + 1))" 'NR >= from && /^## \[/ { print NR; exit }' "$ROOT/CHANGELOG.md")
    if [ -n "$next" ]; then
      sed -n "$((start + 1)),$((next - 1))p" "$ROOT/CHANGELOG.md" >"$notes"
    else
      sed -n "$((start + 1)),\$p" "$ROOT/CHANGELOG.md" >"$notes"
    fi
  else
    printf '%s, %s build from %s.\n' "$title" "$kind" "$commit" >"$notes"
  fi
  prerelease=false
  [ "$kind" = release ] || prerelease=true

  if [ "$state" = absent ]; then
    gh release create "$tag" --repo "$repo" --verify-tag --draft --title "$title" --notes-file "$notes" --prerelease="$prerelease"
  else
    echo "release-publish: resuming the draft release for $tag"
    gh release edit "$tag" --repo "$repo" --title "$title" --notes-file "$notes" --prerelease="$prerelease"
  fi
  gh release upload "$tag" "$@" --repo "$repo" --clobber
  gh release edit "$tag" --repo "$repo" --draft=false
  ;;
*)
  die "unknown command '$command'"
  ;;
esac
