#!/usr/bin/env sh
# @facet-ignore-file

set -u

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
WORK=$(mktemp -d "${TMPDIR:-/tmp}/facet-wget-token-XXXXXX")
trap 'rm -rf "$WORK"' EXIT HUP INT TERM
FAILED=0
fail() {
  printf 'FAIL %s\n  %s\n' "$1" "$2"
  FAILED=1
}
pass() { printf 'ok   %s\n' "$1"; }

VERSION=9.9.9-wget.1
ARCHIVE="facet_${VERSION}_linux_amd64.tar.gz"
REPO=facet-framework/core

# The release: one archive with a facet that names itself, and its checksums.
mkdir -p "$WORK/release/pkg" "$WORK/bin" "$WORK/data"
printf '#!/bin/sh\necho "installed %s"\n' "$VERSION" >"$WORK/release/pkg/facet"
chmod +x "$WORK/release/pkg/facet"
tar -czf "$WORK/release/$ARCHIVE" -C "$WORK/release/pkg" facet
(cd "$WORK/release" && sha256sum "$ARCHIVE" >checksums.txt)

# A PATH with every tool of this system except curl, wget and gh, so the
# dispatcher takes the wget branch and cannot ask gh.
for dir in /usr/local/bin /usr/bin /bin; do
  [ -d "$dir" ] || continue
  for tool in "$dir"/*; do
    name=${tool##*/}
    case "$name" in curl | wget | gh | facet) continue ;; esac
    [ -e "$WORK/bin/$name" ] || ln -s "$tool" "$WORK/bin/$name"
  done
done
# uname is replaced, not written through: the farm entry is a symlink into
# the system directory.
rm -f "$WORK/bin/uname"
printf '#!/bin/sh\ncase "$1" in -m) echo x86_64 ;; *) echo Linux ;; esac\n' >"$WORK/bin/uname"
chmod +x "$WORK/bin/uname"

cat >"$WORK/bin/wget" <<'SH'
#!/bin/sh
# Stub GNU wget: anonymous release URLs are 404, the API answers with a token,
# an asset is a 302 to the storage host, and without --max-redirect=0 the
# redirect is followed with the same headers, as GNU wget does.
out= url= auth=no follow=yes
while [ $# -gt 0 ]; do
  case "$1" in
    -O | -qO) out=$2; shift ;;
    -q | -S) ;;
    --max-redirect=0) follow=no ;;
    --header=Authorization:*) auth=yes ;;
    --header=*) ;;
    -*) echo "stub wget: unexpected option $1" >&2; exit 2 ;;
    *) url=$1 ;;
  esac
  shift
done
serve() { # <url> <auth>
  printf '%s auth=%s\n' "$1" "$2" >> "$WGET_LOG"
  case "$1" in
    https://objects.storage.invalid/blob/11) cat "$RELEASE/$ARCHIVE" > "$out" ;;
    https://objects.storage.invalid/blob/12) cat "$RELEASE/checksums.txt" > "$out" ;;
    *) exit 8 ;;
  esac
}
case "$url" in
  https://github.com/*/releases/download/*)
    printf '%s auth=%s\n' "$url" "$auth" >> "$WGET_LOG"; exit 8 ;;
  https://api.github.com/repos/*/releases/tags/*)
    printf '%s auth=%s\n' "$url" "$auth" >> "$WGET_LOG"
    [ "$auth" = yes ] || exit 6
    printf '{"name": "release", "assets": [\n  {"url": "https://api.github.com/repos/%s/releases/assets/11", "name": "%s"},\n  {"url": "https://api.github.com/repos/%s/releases/assets/12", "name": "checksums.txt"}]}\n' \
      "$STUB_REPO" "$ARCHIVE" "$STUB_REPO" > "$out" ;;
  https://api.github.com/repos/*/releases/assets/*)
    printf '%s auth=%s\n' "$url" "$auth" >> "$WGET_LOG"
    [ "$auth" = yes ] || exit 6
    location="https://objects.storage.invalid/blob/${url##*/}"
    if [ "$follow" = no ]; then
      printf '  HTTP/1.1 302 Found\n  Location: %s\n  Content-Length: 0\n' "$location" >&2
      exit 8
    fi
    serve "$location" "$auth" ;;
  https://objects.storage.invalid/*) serve "$url" "$auth" ;;
  *) printf '%s auth=%s\n' "$url" "$auth" >> "$WGET_LOG"; exit 4 ;;
esac
SH
chmod +x "$WORK/bin/wget"

# Premise: the stub PATH has no curl and no gh, and the stub is the wget.
if PATH="$WORK/bin" command -v curl >/dev/null 2>&1 || PATH="$WORK/bin" command -v gh >/dev/null 2>&1; then
  fail "fixture" "curl or gh is still reachable; the wget branch would not be taken"
  exit 1
fi

out=$(env -i HOME="$WORK/home" PATH="$WORK/bin" GH_TOKEN=secret-token \
  FACET_VERSION="$VERSION" CLAUDE_PLUGIN_DATA="$WORK/data" \
  WGET_LOG="$WORK/wget.log" RELEASE="$WORK/release" ARCHIVE="$ARCHIVE" STUB_REPO="$REPO" \
  sh "$ROOT/bin/facet" --version 2>"$WORK/err")
status=$?

if [ "$status" -ne 0 ] || [ "$out" != "installed $VERSION" ]; then
  fail "the private release installs through wget and the API" "status $status, output '$out': $(cat "$WORK/err") log: $(cat "$WORK/wget.log" 2>/dev/null)"
elif ! grep -q '^https://objects.storage.invalid/blob/11 ' "$WORK/wget.log" ||
  ! grep -q '^https://objects.storage.invalid/blob/12 ' "$WORK/wget.log"; then
  fail "fixture" "no request reached the storage host, so the token check below proves nothing: $(cat "$WORK/wget.log")"
elif ! grep -q '^https://api.github.com/repos/facet-framework/core/releases/assets/11 auth=yes$' "$WORK/wget.log"; then
  fail "the token goes to the releases API" "$(cat "$WORK/wget.log")"
elif grep -q '^https://objects.storage.invalid/.* auth=yes$' "$WORK/wget.log"; then
  fail "the token never reaches the storage host" "$(cat "$WORK/wget.log")"
else
  pass "with only wget, the token goes to the releases API and never to the storage host it redirects to"
fi

# A redirect back to the API's own origin keeps the token; anything else is
# fetched without it. Exercised on the helper directly, sourced from the
# dispatcher, with a stub that redirects twice.
cat >"$WORK/bin/wget" <<'SH'
#!/bin/sh
out= url= auth=no
while [ $# -gt 0 ]; do
  case "$1" in
    -O | -qO) out=$2; shift ;;
    --header=Authorization:*) auth=yes ;;
    -*) ;;
    *) url=$1 ;;
  esac
  shift
done
printf '%s auth=%s\n' "$url" "$auth" >> "$WGET_LOG"
case "$url" in
  https://api.example.invalid/first) printf '  location: /second\n' >&2; exit 8 ;;
  https://api.example.invalid/second) printf '  Location: http://api.example.invalid/third\n' >&2; exit 8 ;;
  http://api.example.invalid/third) echo body > "$out" ;;
  *) exit 4 ;;
esac
SH
chmod +x "$WORK/bin/wget"
sed -n '/^facet_url_origin() {/,/^}/p; /^facet_wget_with_token() {/,/^}/p' "$ROOT/bin/facet" >"$WORK/helpers.sh"
: >"$WORK/wget.log"
if ! grep -q '^facet_wget_with_token() {' "$WORK/helpers.sh"; then
  fail "fixture" "bin/facet has no facet_wget_with_token to source"
elif (PATH="$WORK/bin" WGET_LOG="$WORK/wget.log" sh -c '. "$1"; facet_wget_with_token https://api.example.invalid/first "$2" tok application/json' sh "$WORK/helpers.sh" "$WORK/third") &&
  [ "$(cat "$WORK/third" 2>/dev/null)" = body ] &&
  [ "$(cat "$WORK/wget.log")" = "https://api.example.invalid/first auth=yes
https://api.example.invalid/second auth=yes
http://api.example.invalid/third auth=no" ]; then
  pass "a same-origin redirect keeps the token, a change of scheme or host drops it"
else
  fail "redirect origins decide the token" "$(cat "$WORK/wget.log")"
fi

exit "$FAILED"
