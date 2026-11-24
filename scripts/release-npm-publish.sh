#!/usr/bin/env sh
# @facet: delivery/distribution/npm-packages
# @facet-role: related

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
. "$SCRIPT_DIR/release-npm-tag.sh"

die() {
  echo "release-npm-publish: $*" >&2
  exit 2
}

[ "$#" -eq 1 ] || die "usage: release-npm-publish.sh <dist>"
case "$1" in
/* | ./*) dist=$1 ;;
*) dist=./$1 ;;
esac
PLAN="$dist/npm/publish-plan.json"
[ -f "$PLAN" ] || die "$PLAN is missing"
command -v jq >/dev/null 2>&1 || die "jq is required"
command -v npm >/dev/null 2>&1 || die "npm is required"

jq -e '.publishable == true and .complete == true and (.packages | length > 0)' "$PLAN" >/dev/null ||
  die "the plan is not publishable: $(jq -c '{publishable, complete, blocked_by}' "$PLAN")"

npm_version=$(npm --version)
# npm stage first shipped in npm 11.15.0.
printf '%s\n' "$npm_version" | awk -F. '{ exit !($1 > 11 || ($1 == 11 && $2 >= 15)) }' ||
  die "npm $npm_version cannot stage packages; 11.15.0 or later is needed"

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT HUP INT TERM
ERR="$TMP/err"
TAB=$(printf '\t')
jq -r '.packages | sort_by(.order)[] | [.name, .version, .tarball, .integrity, .access] | @tsv' "$PLAN" >"$TMP/list"
channel=$(jq -r '.channel // ""' "$PLAN")
product=$(jq -r '.product // "core"' "$PLAN")

pending=false
while IFS=$TAB read -r name version tarball integrity access; do
  local_tarball="$dist/npm/$tarball"
  [ -f "$local_tarball" ] || die "$name@$version: $local_tarball is missing"
  if found=$(npm view "$name@$version" dist.integrity 2>"$ERR"); then
    if [ "$found" = "$integrity" ]; then
      echo "release-npm-publish: $name@$version is already published with this tarball; skipped"
      continue
    fi
    [ -n "$found" ] || die "the registry answered nothing for $name@$version; refusing to guess"
    die "$name@$version is published with integrity $found, not the plan's $integrity"
  elif ! grep -q 'E404' "$ERR"; then
    die "cannot ask the registry for $name@$version: $(cat "$ERR")"
  fi
  [ "$access" = public ] || die "$name@$version: the plan must specify public access"
  tag=$(npm_dist_tag "$version" "$channel" "$product")
  if npm stage publish "$local_tarball" --provenance --access public --tag "$tag" 2>"$ERR"; then
    cat "$ERR" >&2
    echo "release-npm-publish: $name@$version staged with dist-tag $tag, awaiting approval on npmjs.com"
  elif grep -Fxq 'npm error code E409' "$ERR" && grep -Fq 'Cannot publish over previously staged version' "$ERR"; then
    # Inspecting a pending stage requires interactive authentication. The OIDC
    # conflict reports the version but does not expose the existing dist-tag.
    echo "release-npm-publish: $name@$version staged earlier, tag fixed at staging; awaiting approval on npmjs.com"
  else
    die "cannot stage $name@$version: $(cat "$ERR")"
  fi
  pending=true
done <"$TMP/list"
if [ "$pending" = false ]; then
  echo "release-npm-publish: every package of the plan is already published"
fi
