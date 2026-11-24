#!/usr/bin/env sh
# @facet: delivery/distribution/npm-packages
# @facet-role: related
#
# The release step after release-manifest.sh: generates the npm packages of a
# packaged release (npm/generate.sh) and packs each into a tarball. Publishes
# nothing; release workflows can keep the tarballs and plan as artifacts.

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
. "$SCRIPT_DIR/release-npm-tag.sh"

die() {
  echo "release-npm-pack: $*" >&2
  exit 2
}

PARTIAL=
if [ "${1:-}" = --allow-partial ]; then
  PARTIAL=--allow-partial
  shift
fi
if [ "${1:-}" = --source ] && [ "$#" -eq 3 ]; then
  ROOT=$2
  shift 2
fi
[ "$#" -eq 1 ] || die "usage: release-npm-pack.sh [--allow-partial] [--source <root>] <dist>"
DIST=$1
OUT="$DIST/npm"
command -v npm >/dev/null 2>&1 || die "npm is required"
command -v jq >/dev/null 2>&1 || die "jq is required"

owner=$(sed -n 's|^module[[:space:]]*github\.com/\([^/]*\)/.*|\1|p' "$ROOT/go.mod" | head -n 1)
[ -n "$owner" ] || die "cannot read the owner of the module path in $ROOT/go.mod"

# The pack summary below reports the current tag even for an older generator.
sh "$ROOT/npm/generate.sh" $PARTIAL "$DIST" "$OUT" >/dev/null || exit 2
OUT=$(CDPATH= cd -- "$OUT" && pwd)

# Every text file that will be packed is refused if it names the private owner
# or a credential. The platform binaries are not text; they are scanned below
# and reported in the plan instead.
printf '%s\n' .npmrc _authToken GH_TOKEN >"$OUT/markers.txt"
[ "$owner" = facet-framework ] || printf '%s\n' "$owner" >>"$OUT/markers.txt"
for dir in $(jq -r '.packages[].dir' "$OUT/packages.json"); do
  role=$(jq -r --arg d "$dir" '.packages[] | select(.dir == $d) | .role' "$OUT/packages.json")
  # Every file, the third-party notices under THIRD_PARTY_LICENSES/ included.
  find "$OUT/packages/$dir" -type f | LC_ALL=C sort >"$OUT/scan.txt"
  while IFS= read -r file; do
    case "$role:${file#"$OUT/packages/$dir/"}" in platform:facet | platform:facet.exe) continue ;; esac
    found=$(grep -o -i -F -f "$OUT/markers.txt" "$file" | sort -u | tr '\n' ' ')
    [ -z "$found" ] || die "$file names a private marker ($found); nothing was packed"
  done <"$OUT/scan.txt"
done
rm -f "$OUT/markers.txt" "$OUT/scan.txt"

: >"$OUT/packed.jsonl"
for dir in $(jq -r '.packages[].dir' "$OUT/packages.json"); do
  # --ignore-scripts: a generated package has no scripts, and packing must
  # never run any. The JSON on stdout is npm's own account of the tarball.
  (cd "$OUT/packages/$dir" && npm pack --json --ignore-scripts --pack-destination "$OUT") >"$OUT/pack.json" 2>"$OUT/pack.err" ||
    die "npm pack failed for $OUT/packages/$dir: $(cat "$OUT/pack.err")"
  jq -c --arg dir "$dir" '.[0] | {dir: $dir, tarball: .filename, integrity: .integrity, shasum: .shasum, size: .size,
    files: [.files[].path] | sort}' "$OUT/pack.json" >>"$OUT/packed.jsonl"
  echo "release-npm-pack: $OUT/$(jq -r '.[0].filename' "$OUT/pack.json")"
done

# Why these packages may not be published, one reason per line.
: >"$OUT/blocked.txt"
: >"$OUT/private.txt"
if [ "$owner" != facet-framework ]; then
  for dir in $(jq -r '.packages[] | select(.role == "platform") | .dir' "$OUT/packages.json"); do
    for binary in "$OUT/packages/$dir/facet" "$OUT/packages/$dir/facet.exe"; do
      [ -f "$binary" ] || continue
      if LC_ALL=C grep -a -q -F -- "$owner" "$binary"; then
        jq -r --arg d "$dir" '.packages[] | select(.dir == $d) | .name' "$OUT/packages.json" >>"$OUT/private.txt"
      fi
    done
  done
  if [ -s "$OUT/private.txt" ]; then printf 'binary names private source %s\n' "$owner" >>"$OUT/blocked.txt"; fi
fi
[ "$(jq -r .complete "$OUT/packages.json")" = true ] || echo "incomplete platform set" >>"$OUT/blocked.txt"
[ "$(jq -r .access "$OUT/packages.json")" != null ] || echo "npm access not decided" >>"$OUT/blocked.txt"
[ "$(jq '.notice_missing | length' "$OUT/packages.json")" = 0 ] || echo "third-party notices not included" >>"$OUT/blocked.txt"

version=$(jq -r .version "$OUT/packages.json")
channel=$(jq -r .channel "$OUT/packages.json")
product=$(jq -r .product "$OUT/packages.json")
tag=$(npm_dist_tag "$version" "$channel" "$product")
jq --arg tag "$tag" --slurpfile packed "$OUT/packed.jsonl" --rawfile blocked "$OUT/blocked.txt" --rawfile private "$OUT/private.txt" \
  '.dist_tag = $tag | . as $plan
   | ($blocked | split("\n") | map(select(length > 0))) as $reasons
   | {publishable: ($reasons | length == 0), blocked_by: $reasons,
      private_binaries: ($private | split("\n") | map(select(length > 0)))} + .
   | .packages |= [to_entries[] | .value as $p | ($packed[] | select(.dir == $p.dir)) as $t
     | $p + {order: (.key + 1), version: $plan.version, dist_tag: $plan.dist_tag, access: $plan.access}
          + ($t | del(.dir))]' \
  "$OUT/packages.json" >"$OUT/publish-plan.json"
rm -f "$OUT/packed.jsonl" "$OUT/pack.json" "$OUT/pack.err" "$OUT/packages.json" "$OUT/blocked.txt" "$OUT/private.txt"
echo "release-npm-pack: $OUT/publish-plan.json ($(jq '.packages | length' "$OUT/publish-plan.json") packages, dist-tag $(jq -r .dist_tag "$OUT/publish-plan.json"), publishable $(jq -r .publishable "$OUT/publish-plan.json"))"
