#!/usr/bin/env sh
# @facet: navigation/interfaces/product-services
# @facet-role: related
# SessionEnd hook — hands the host's own end-of-session to the binary, so an
# installed product that keeps a session journal can record the boundary. Such
# a product writes only when its own opt-in is set in the hook environment.

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
. "$SCRIPT_DIR/hook-lib.sh"

INPUT=$(cat)

facet_delegate_to_binary "session-end" "$INPUT" && exit 0
exit 0
