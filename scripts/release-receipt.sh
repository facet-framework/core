#!/usr/bin/env sh
# @facet: delivery/distribution/public-core-release
# @facet-role: primary
#
# Reads and checks PUBLIC-SOURCE-MANIFEST.json, the receipt the export writes
# at the root of the public Core repository: the product version and, per
# file, its path, sha256 and mode.

set -eu

die() {
  echo "release-receipt: $*" >&2
  exit 2
}

[ "$#" -ge 1 ] && [ "$#" -le 2 ] || die "usage: release-receipt.sh version|verify|diff [<root>]"
COMMAND=$1
ROOT=${2:-$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)}
RECEIPT="$ROOT/PUBLIC-SOURCE-MANIFEST.json"
command -v jq >/dev/null 2>&1 || die "jq is required"
[ -f "$RECEIPT" ] || die "$RECEIPT does not exist; this is not an exported Core tree"
jq -e '.schema_version == 1 and .product == "core" and (.version | type == "string") and (.files | type == "array")' \
  "$RECEIPT" >/dev/null 2>&1 || die "$RECEIPT is not a Core receipt (schema_version 1, product core, version, files)"

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | cut -d' ' -f1
  else
    shasum -a 256 "$1" | cut -d' ' -f1
  fi
}

case "$COMMAND" in
version)
  jq -r .version "$RECEIPT"
  exit 0
  ;;
verify | diff) ;;
*) die "unknown command '$COMMAND'" ;;
esac

WORK=$(mktemp -d "${TMPDIR:-/tmp}/facet-receipt.XXXXXX")
trap 'rm -rf "$WORK"' EXIT HUP INT TERM

# path<TAB>sha256<TAB>mode, sorted by path; a path with a tab or a newline is refused.
jq -e '[.files[].path] | (length == (unique | length)) and all(test("^[^/\t\n][^\t\n]*$") and (test("(^|/)\\.\\.(/|$)") | not))' \
  "$RECEIPT" >/dev/null || die "$RECEIPT lists a path twice, an absolute path, a .. segment or a control character"
# Joined by hand, not with @tsv, which would write a backslash in a path as \\.
jq -r '.files[] | "\(.path)\t\(.sha256)\t\(.mode)"' "$RECEIPT" >"$WORK/listed.tsv"
cut -f1 "$WORK/listed.tsv" | LC_ALL=C sort >"$WORK/listed"
# Unquoted, NUL-terminated paths: a non-ASCII name is compared as the receipt
# writes it, not as git's "\303\244" escape. A newline inside a path becomes
# \001 and so can never equal a receipt path, which may hold neither.
git -C "$ROOT" -c core.quotePath=false ls-files -s -z >"$WORK/index.z" || die "git ls-files failed in $ROOT"
tr '\n\0' '\001\n' <"$WORK/index.z" >"$WORK/index"
# <mode> <object> <stage>\t<path>; the path is everything after the first tab
# and may hold a tab itself, so it is the last field: mode<TAB>path.
awk '{ t = index($0, "\t"); split(substr($0, 1, t - 1), a, " "); print a[1] "\t" substr($0, t + 1) }' "$WORK/index" >"$WORK/tracked.tsv"
cut -f2- "$WORK/tracked.tsv" | grep -vx 'PUBLIC-SOURCE-MANIFEST.json' | LC_ALL=C sort >"$WORK/tracked" || true

: >"$WORK/problems"
LC_ALL=C comm -13 "$WORK/listed" "$WORK/tracked" | sed 's/^/tracked but not in the receipt: /' >>"$WORK/problems"
LC_ALL=C comm -23 "$WORK/listed" "$WORK/tracked" | sed 's/^/in the receipt but not tracked: /' >>"$WORK/problems"
# Does this file system keep an executable bit? A fresh file is not executable;
# after chmod it is, where the bit exists at all.
: >"$WORK/probe"
chmod 0755 "$WORK/probe"
exec_bit=0
[ -x "$WORK/probe" ] && {
  chmod 0644 "$WORK/probe"
  [ ! -x "$WORK/probe" ]
} && exec_bit=1

while IFS="$(printf '\t')" read -r path sum mode; do
  file="$ROOT/$path"
  if [ -L "$file" ] || [ ! -f "$file" ]; then
    printf 'not a regular file: %s\n' "$path" >>"$WORK/problems"
    continue
  fi
  got=$(sha256 "$file")
  [ "$got" = "$sum" ] || printf 'sha256 differs: %s (receipt %s, file %s)\n' "$path" "$sum" "$got" >>"$WORK/problems"
  case "$mode" in
  0755) want_git=100755 ;;
  0644) want_git=100644 ;;
  *)
    printf 'mode %s is neither 0644 nor 0755: %s\n' "$mode" "$path" >>"$WORK/problems"
    continue
    ;;
  esac
  # ENVIRON, not -v: awk would read backslashes in the path as escapes.
  git_mode=$(P="$path" awk '{ t = index($0, "\t") } substr($0, t + 1) == ENVIRON["P"] { print substr($0, 1, t - 1) }' "$WORK/tracked.tsv")
  [ -z "$git_mode" ] || [ "$git_mode" = "$want_git" ] ||
    printf 'mode differs in git: %s (receipt %s, git %s)\n' "$path" "$mode" "$git_mode" >>"$WORK/problems"
  if [ "$exec_bit" = 1 ]; then
    if [ "$mode" = 0755 ] && [ ! -x "$file" ]; then
      printf 'not executable on disk: %s (receipt 0755)\n' "$path" >>"$WORK/problems"
    elif [ "$mode" = 0644 ] && [ -x "$file" ]; then
      printf 'executable on disk: %s (receipt 0644)\n' "$path" >>"$WORK/problems"
    fi
  fi
done <"$WORK/listed.tsv"

if [ "$COMMAND" = diff ]; then
  cat "$WORK/problems"
  exit 0
fi
if [ -s "$WORK/problems" ]; then
  cat "$WORK/problems" >&2
  die "$(wc -l <"$WORK/problems" | tr -d ' ') difference(s) between the tree and $RECEIPT"
fi
echo "release-receipt: $(wc -l <"$WORK/listed" | tr -d ' ') files match $RECEIPT (version $(jq -r .version "$RECEIPT")), nothing else is tracked"
