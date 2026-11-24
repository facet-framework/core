# shellcheck shell=sh
# Sourced by the Core release scripts; not run on its own.

# The variables are read by the scripts that source this file.
# shellcheck disable=SC2034
release_layout() {
  _layout=${FACET_RELEASE_LAYOUT:-}
  if [ -z "$_layout" ]; then
    if [ -f "$1/PUBLIC-SOURCE-MANIFEST.json" ]; then
      _layout=public
    elif [ -d "$1/cmd/facet-core" ] && [ -d "$1/facet-framework/core" ]; then
      _layout=development
    else
      echo "release-layout: cannot tell the layout of $1: no PUBLIC-SOURCE-MANIFEST.json (public) and no cmd/facet-core with facet-framework/core (development); restore the receipt or set FACET_RELEASE_LAYOUT" >&2
      return 2
    fi
  fi
  case "$_layout" in
  development)
    CORE_MAIN_PACKAGE=./cmd/facet-core
    CORE_LICENSE_DIR="$1/facet-framework/core"
    CORE_TAG_PREFIX=internal/core/
    ATLAS_TAG_PREFIX=internal/atlas/
    ;;
  public)
    # The receipt has to be present and name product core.
    if ! [ -f "$1/PUBLIC-SOURCE-MANIFEST.json" ] ||
      [ "$(jq -r '.product // empty' "$1/PUBLIC-SOURCE-MANIFEST.json" 2>/dev/null)" != core ]; then
      echo "release-layout: the public layout needs PUBLIC-SOURCE-MANIFEST.json naming product core in $1" >&2
      return 2
    fi
    CORE_MAIN_PACKAGE=./cmd/facet
    CORE_LICENSE_DIR=$1
    CORE_TAG_PREFIX=v
    ATLAS_TAG_PREFIX=
    ;;
  *)
    echo "release-layout: FACET_RELEASE_LAYOUT '$_layout' is not development or public" >&2
    return 2
    ;;
  esac
  LAYOUT=$_layout
}
