#!/usr/bin/env sh
# @facet: delivery/distribution/npm-packages
# @facet-role: primary
#
# Generates the npm packages of one product from a packaged release directory,
# so npm ships the bytes the GitHub release ships. Nothing is built here.

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
. "$SCRIPT_DIR/../scripts/release-npm-tag.sh"
TEMPLATES="$SCRIPT_DIR/templates"
ALL_PLATFORMS="darwin_amd64 darwin_arm64 linux_amd64 linux_arm64 windows_amd64 windows_arm64"

die() {
  echo "npm-generate: $*" >&2
  exit 2
}

ALLOW_PARTIAL=false
if [ "${1:-}" = --allow-partial ]; then
  ALLOW_PARTIAL=true
  shift
fi
[ "$#" -eq 2 ] || die "usage: generate.sh [--allow-partial] <dist> <out>"
DIST=$1
OUT=$2

for tool in jq tar unzip sha256sum; do
  command -v "$tool" >/dev/null 2>&1 || die "$tool is required"
done
MANIFEST="$DIST/release-manifest.json"
[ -f "$MANIFEST" ] || die "$MANIFEST does not exist"
jq -e '.manifest_version == 1 and (.platforms | type == "array")' "$MANIFEST" >/dev/null 2>&1 ||
  die "$MANIFEST is not a version 1 release manifest"
if [ -e "$OUT" ] && [ -n "$(ls -A "$OUT" 2>/dev/null)" ]; then
  die "$OUT already holds files; packages are generated into an empty directory only"
fi

field() { jq -r --arg k "$1" '.[$k] // "" | tostring' "$MANIFEST"; }
PRODUCT=$(field product)
VERSION=$(field version)
FULL=$(field full_version)
CHANNEL=$(field channel)
COMMIT=$(field source_commit)

# Only Core ships through npm today; the second branch is reached only by a
# separately distributed product's own release directory.
case "$PRODUCT" in
core) LAUNCHER=@facet-tools/core PRODUCT_NAME="FACET Core" ;;
atlas) LAUNCHER=@facet-tools/atlas PRODUCT_NAME="FACET Atlas" ;;
*) die "$MANIFEST names product '$PRODUCT', not core or atlas" ;;
esac
# README templates hold a @@CORE@@ … @@/CORE@@ block (usage of the Core command
# line) and an @@OTHER@@ … @@/OTHER@@ block for any other product; only the
# block for this product remains, without its marker lines.
readme_blocks() {
  if [ "$PRODUCT" = core ]; then keep=CORE drop=OTHER; else keep=OTHER drop=CORE; fi
  sed -e "/^@@$drop@@\$/,/^@@\/$drop@@\$/d" -e "/^@@\/\{0,1\}$keep@@\$/d" "$1"
}

# The npm version is the release version itself: semver, never build metadata.
printf '%s\n' "$VERSION" | grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$' ||
  die "$MANIFEST version '$VERSION' is not a semver version without build metadata"
case "$FULL" in "$VERSION+"*) ;; *) die "$MANIFEST full_version '$FULL' does not extend version '$VERSION'" ;; esac
case "$CHANNEL" in
dev | rc | stable) ;;
*) die "$MANIFEST channel '$CHANNEL' is not dev, rc or stable" ;;
esac
DIST_TAG=$(npm_dist_tag "$VERSION" "$CHANNEL" "$PRODUCT")
# Only Core has a decided npm access; for any other product the plan says so
# instead of naming a value nobody chose.
if [ "$PRODUCT" = core ]; then ACCESS='"public"'; else ACCESS=null; fi
# Core's public home is the public Core repository (bin/facet's
# FACET_UPSTREAM_REPO), and its scoped packages are published public. For any
# other product neither field is decided, so its packages get neither.
if [ "$PRODUCT" = core ]; then
  PUBLIC_FIELDS='{"repository": {"type": "git", "url": "git+https://github.com/facet-framework/core.git"}}'
  SCOPED_FIELDS='{"publishConfig": {"access": "public"}}'
else
  PUBLIC_FIELDS='{}'
  SCOPED_FIELDS='{}'
fi

npm_platform() {
  case "$1" in
  linux_amd64) echo linux-x64 ;;
  linux_arm64) echo linux-arm64 ;;
  darwin_amd64) echo darwin-x64 ;;
  darwin_arm64) echo darwin-arm64 ;;
  windows_amd64) echo win32-x64 ;;
  windows_arm64) echo win32-arm64 ;;
  *) die "$MANIFEST names platform '$1', which has no npm platform" ;;
  esac
}

# A package directory name: the package name without '@' and with '/' as '-'.
package_dir() { printf '%s\n' "$1" | sed 's|^@||; s|/|-|'; }

# The license id is read from the LICENSE the archive carries, never assumed.
# The Apache License 2.0 text (Core) maps to Apache-2.0 and the GNU AGPL
# version 3 text to AGPL-3.0-only; any other text stops the run.
spdx_of() {
  head -n 5 "$1" | tr -s ' \t' '  ' >"$WORK/license.head"
  if grep -q 'Apache License' "$WORK/license.head" && grep -q 'Version 2.0, January 2004' "$WORK/license.head"; then
    echo Apache-2.0
  elif grep -q 'GNU AFFERO GENERAL PUBLIC LICENSE' "$WORK/license.head" && grep -q 'Version 3, 19 November 2007' "$WORK/license.head"; then
    echo AGPL-3.0-only
  else
    return 1
  fi
}

# The Core third-party corpus, if the archive carries one: THIRD_PARTY.json and
# the files under THIRD_PARTY_LICENSES/, regular files only, no member name
# that leaves the extraction directory. An archive with one but not the other
# is a packaging defect.
extract_corpus() { # <archive> <dir>
  case "$1" in
  *.zip) unzip -Z1 "$1" >"$WORK/members" ;;
  *) tar -tzf "$1" >"$WORK/members" ;;
  esac || die "cannot list the members of $1"
  grep -E '^THIRD_PARTY(\.json$|_LICENSES/)' "$WORK/members" >"$WORK/corpus.members" || return 0
  if grep -E '(^|/)\.\.(/|$)|^/' "$WORK/corpus.members" >/dev/null; then die "$1 names a notice file outside the archive root"; fi
  grep -qx THIRD_PARTY.json "$WORK/corpus.members" && grep -q '^THIRD_PARTY_LICENSES/.' "$WORK/corpus.members" ||
    die "$1 carries only part of the third-party notices (THIRD_PARTY.json and THIRD_PARTY_LICENSES/ go together)"
  case "$1" in
  *.zip) unzip -q "$1" 'THIRD_PARTY.json' 'THIRD_PARTY_LICENSES/*' -d "$2" 2>"$WORK/extract.err" ;;
  *) tar -xzf "$1" -C "$2" -T "$WORK/corpus.members" 2>"$WORK/extract.err" ;;
  esac || die "cannot extract the third-party notices from $1: $(cat "$WORK/extract.err")"
  [ -z "$(find "$2/THIRD_PARTY.json" "$2/THIRD_PARTY_LICENSES" ! -type f ! -type d)" ] ||
    die "$1: the third-party notices hold something that is not a regular file"
}

WORK=$(mktemp -d "${TMPDIR:-/tmp}/facet-npm-generate.XXXXXX")
trap 'rm -rf "$WORK"' EXIT HUP INT TERM
mkdir -p "$WORK/out/packages"

count=$(jq '.platforms | length' "$MANIFEST")
[ "$count" -gt 0 ] || die "$MANIFEST lists no platforms"
present=$(jq -r '.platforms[].platform' "$MANIFEST" | LC_ALL=C sort | tr '\n' ' ' | sed 's/ $//')
[ "$(jq -r '.platforms[].platform' "$MANIFEST" | LC_ALL=C sort -u | wc -l)" -eq "$count" ] ||
  die "$MANIFEST lists a platform more than once"
COMPLETE=true
[ "$present" = "$ALL_PLATFORMS" ] || COMPLETE=false
if [ "$COMPLETE" = false ] && [ "$ALLOW_PARTIAL" = false ]; then
  die "$MANIFEST has platforms '$present'; a release needs all of '$ALL_PLATFORMS' (--allow-partial for a local build)"
fi

# Core packages carry the archive's NOTICE beside its LICENSE.
if [ "$PRODUCT" = core ]; then
  MEMBERS="LICENSE NOTICE"
  FILES_EXTRA='["NOTICE"]'
else
  MEMBERS="LICENSE"
  FILES_EXTRA='[]'
fi
LICENSE_SUM=
NOTICE_SUM=
# The corpus digest of the first archive; "none" when it carries no corpus.
CORPUS_SUM=
CORPUS_DIR=
: >"$WORK/platforms.jsonl"
entry() { jq -r --argjson i "$i" --arg k "$1" '.platforms[$i][$k] // ""' "$MANIFEST"; }
i=0
while [ "$i" -lt "$count" ]; do
  platform=$(entry platform)
  archive=$(entry archive)
  archive_sha=$(entry archive_sha256)
  binary=$(entry binary)
  binary_sha=$(entry binary_sha256)
  i=$((i + 1))
  npm_key=$(npm_platform "$platform")
  case "$platform" in windows_*) want_binary=facet.exe ;; *) want_binary=facet ;; esac
  [ "$binary" = "$want_binary" ] || die "$MANIFEST names binary '$binary' for $platform, expected $want_binary"
  case "$archive" in */* | '' | .*) die "$MANIFEST names archive '$archive', not a file name" ;; esac
  [ -f "$DIST/$archive" ] || die "$DIST/$archive is named in the manifest and does not exist"

  got=$(sha256sum "$DIST/$archive" | cut -d' ' -f1)
  [ "$got" = "$archive_sha" ] || die "$DIST/$archive hashes to $got, the manifest says $archive_sha"

  x="$WORK/x/$platform"
  mkdir -p "$x"
  case "$archive" in
  *.zip) unzip -q "$DIST/$archive" "$binary" $MEMBERS -d "$x" 2>"$WORK/extract.err" ||
    die "$DIST/$archive yields no $binary $MEMBERS at its root: $(cat "$WORK/extract.err")" ;;
  *.tar.gz) tar -xzf "$DIST/$archive" -C "$x" "$binary" $MEMBERS 2>"$WORK/extract.err" ||
    die "$DIST/$archive yields no $binary $MEMBERS at its root: $(cat "$WORK/extract.err")" ;;
  *) die "$DIST/$archive is neither a tar.gz nor a zip" ;;
  esac
  [ -f "$x/$binary" ] && [ ! -L "$x/$binary" ] || die "$DIST/$archive: $binary is not a regular file"
  for member in $MEMBERS; do
    [ -f "$x/$member" ] && [ ! -L "$x/$member" ] || die "$DIST/$archive: $member is not a regular file"
  done
  [ "$PRODUCT" != core ] || extract_corpus "$DIST/$archive" "$x"
  got=$(sha256sum "$x/$binary" | cut -d' ' -f1)
  [ "$got" = "$binary_sha" ] || die "$binary from $DIST/$archive hashes to $got, the manifest says $binary_sha"

  sum=$(sha256sum "$x/LICENSE" | cut -d' ' -f1)
  if [ -z "$LICENSE_SUM" ]; then
    LICENSE_SUM=$sum
    LICENSE_FILE="$x/LICENSE"
    SPDX=$(spdx_of "$LICENSE_FILE") || die "LICENSE in $DIST/$archive is not a license text this generator recognises"
  elif [ "$sum" != "$LICENSE_SUM" ]; then
    die "LICENSE in $DIST/$archive differs from the LICENSE in the other archives"
  fi
  if [ "$PRODUCT" = core ]; then
    sum=$(sha256sum "$x/NOTICE" | cut -d' ' -f1)
    if [ -z "$NOTICE_SUM" ]; then
      NOTICE_SUM=$sum
      NOTICE_FILE="$x/NOTICE"
    elif [ "$sum" != "$NOTICE_SUM" ]; then
      die "NOTICE in $DIST/$archive differs from the NOTICE in the other archives"
    fi
    sum=none
    [ ! -f "$x/THIRD_PARTY.json" ] || sum=$(cd "$x" && find THIRD_PARTY.json THIRD_PARTY_LICENSES -type f -exec sha256sum {} + |
      LC_ALL=C sort -k2 | sha256sum | cut -d' ' -f1)
    if [ -z "$CORPUS_SUM" ]; then
      CORPUS_SUM=$sum
      if [ "$sum" != none ]; then
        CORPUS_DIR=$x
        FILES_EXTRA='["NOTICE", "THIRD_PARTY.json", "THIRD_PARTY_LICENSES/"]'
      fi
    elif [ "$sum" != "$CORPUS_SUM" ]; then
      die "the third-party notices in $DIST/$archive differ from those in the other archives"
    fi
  fi

  name="$LAUNCHER-$npm_key"
  dir="$WORK/out/packages/$(package_dir "$name")"
  mkdir -p "$dir"
  cp "$x/$binary" "$dir/$binary"
  chmod 0755 "$dir/$binary"
  sed -e "s|@@PACKAGE@@|$name|g" -e "s|@@PRODUCT@@|$PRODUCT_NAME|g" -e "s|@@PLATFORM@@|$npm_key|g" \
    -e "s|@@LAUNCHER@@|$LAUNCHER|g" -e "s|@@BINARY@@|$binary|g" "$TEMPLATES/platform/package.json" >"$WORK/template.json"
  jq --arg name "$name" --arg version "$VERSION" --arg license "$SPDX" \
    --arg os "${npm_key%-*}" --arg cpu "${npm_key#*-}" --argjson public "$PUBLIC_FIELDS" --argjson scoped "$SCOPED_FIELDS" \
    --argjson extra_files "$FILES_EXTRA" \
    '{name: $name, version: $version, description: .description, license: $license} + $public
      + {os: [$os], cpu: [$cpu], files: (.files + $extra_files), preferUnplugged: .preferUnplugged} + $scoped' \
    "$WORK/template.json" >"$dir/package.json"
  readme_blocks "$TEMPLATES/platform/README.md" |
    sed -e "s|@@PACKAGE@@|$name|g" -e "s|@@PRODUCT@@|$PRODUCT_NAME|g" -e "s|@@PLATFORM@@|$npm_key|g" \
      -e "s|@@LAUNCHER@@|$LAUNCHER|g" -e "s|@@VERSION@@|$VERSION|g" >"$dir/README.md"
  jq -n -c --arg name "$name" --arg dir "$(package_dir "$name")" --arg platform "$platform" \
    '{name: $name, dir: $dir, role: "platform", platform: $platform}' >>"$WORK/platforms.jsonl"
done

# Package written from a template directory: README with placeholders, the
# template package.json merged under the generated name, version and license.
write_package() { # <name> <template> <extra jq object> [<js file>]
  dir="$WORK/out/packages/$(package_dir "$1")"
  mkdir -p "$dir"
  sed -e "s|@@PACKAGE@@|$1|g" -e "s|@@PRODUCT@@|$PRODUCT_NAME|g" "$TEMPLATES/$2/package.json" >"$WORK/template.json"
  jq --arg name "$1" --arg version "$VERSION" --arg license "$SPDX" --argjson extra "$3" --argjson public "$PUBLIC_FIELDS" \
    --argjson extra_files "$FILES_EXTRA" \
    '{name: $name, version: $version, description: .description, license: $license} + $public + (del(.description))
      + {files: (.files + $extra_files)} + $extra' \
    "$WORK/template.json" >"$dir/package.json"
  readme_blocks "$TEMPLATES/$2/README.md" |
    sed -e "s|@@PACKAGE@@|$1|g" -e "s|@@PRODUCT@@|$PRODUCT_NAME|g" -e "s|@@VERSION@@|$VERSION|g" >"$dir/README.md"
  copy_notices "$dir"
  chmod 0644 "$dir/README.md" "$dir/package.json"
  if [ "$#" -ge 4 ]; then
    cp "$SCRIPT_DIR/$4" "$dir/$4"
    chmod 0755 "$dir/$4"
  fi
}

# LICENSE, and for Core NOTICE and the third-party corpus, exactly as the
# archives carry them.
copy_notices() {
  cp "$LICENSE_FILE" "$1/LICENSE"
  chmod 0644 "$1/LICENSE"
  if [ "$PRODUCT" = core ]; then
    cp "$NOTICE_FILE" "$1/NOTICE"
    chmod 0644 "$1/NOTICE"
  fi
  if [ -n "$CORPUS_DIR" ]; then
    cp "$CORPUS_DIR/THIRD_PARTY.json" "$1/THIRD_PARTY.json"
    cp -R "$CORPUS_DIR/THIRD_PARTY_LICENSES" "$1/THIRD_PARTY_LICENSES"
    find "$1/THIRD_PARTY.json" "$1/THIRD_PARTY_LICENSES" -type d -exec chmod 0755 {} +
    find "$1/THIRD_PARTY.json" "$1/THIRD_PARTY_LICENSES" -type f -exec chmod 0644 {} +
  fi
}

for pdir in "$WORK"/out/packages/*/; do
  copy_notices "$pdir"
  chmod 0644 "$pdir/README.md" "$pdir/package.json"
done

optional=$(jq -s -c --arg v "$VERSION" 'map({(.name): $v}) | add' "$WORK/platforms.jsonl")
write_package "$LAUNCHER" launcher "$(jq -n -c --argjson o "$optional" --argjson s "$SCOPED_FIELDS" '{optionalDependencies: $o} + $s')" launcher.js
chmod 0644 "$WORK/out/packages/$(package_dir "$LAUNCHER")/launcher.js"
ENTRIES='[]'
if [ "$PRODUCT" = core ]; then
  deps=$(jq -n -c --arg n "$LAUNCHER" --arg v "$VERSION" '{dependencies: {($n): $v}}')
  write_package facet-framework facet-framework "$deps" facet-framework.js
  write_package facet-tools facet-tools '{}' facet-tools.js
  ENTRIES='[{"name": "facet-framework", "dir": "facet-framework", "role": "entry"},
            {"name": "facet-tools", "dir": "facet-tools", "role": "entry"}]'
fi

: >"$WORK/notice-missing"
if [ "$PRODUCT" = core ]; then
  for ref in $(grep -o -E '(^|[^A-Za-z0-9_])[A-Z][A-Z0-9_]+(\.[a-z]+|/)' "$NOTICE_FILE" | sed 's/^[^A-Z]//' | sort -u); do
    for pdir in "$WORK"/out/packages/*/; do
      case "$ref" in
      */) [ -d "$pdir/${ref%/}" ] || echo "$ref" >>"$WORK/notice-missing" ;;
      *) [ -e "$pdir/$ref" ] || echo "$ref" >>"$WORK/notice-missing" ;;
      esac
    done
  done
fi
NOTICE_MISSING=$(sort -u "$WORK/notice-missing" | jq -R . | jq -s -c .)

jq -s --arg product "$PRODUCT" --arg version "$VERSION" --arg full "$FULL" --arg channel "$CHANNEL" \
  --arg tag "$DIST_TAG" --argjson access "$ACCESS" --arg commit "$COMMIT" --argjson complete "$COMPLETE" \
  --arg launcher "$LAUNCHER" --arg launcher_dir "$(package_dir "$LAUNCHER")" --arg license "$SPDX" \
  --argjson entries "$ENTRIES" --argjson notice_missing "$NOTICE_MISSING" \
  '{plan_version: 1, product: $product, version: $version, full_version: $full, channel: $channel,
    dist_tag: $tag, access: $access, license: $license, source_commit: $commit, complete: $complete,
    notice_missing: $notice_missing,
    packages: ((sort_by(.name)) + [{name: $launcher, dir: $launcher_dir, role: "launcher"}] + $entries)}' \
  "$WORK/platforms.jsonl" >"$WORK/out/packages.json"

mkdir -p "$OUT"
cp -R "$WORK/out/." "$OUT/"
echo "npm-generate: $(jq '.packages | length' "$OUT/packages.json") $PRODUCT packages for $VERSION ($DIST_TAG) in $OUT/packages"
