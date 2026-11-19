#!/usr/bin/env sh
# @facet: navigation/interfaces/mcp-server
# @facet-role: primary
# Start the shared MCP server for the project, not the installed plugin tree.
set -eu

FACET_MCP_PLUGIN_ROOT="${PLUGIN_ROOT:-${CLAUDE_PLUGIN_ROOT:-}}"
if [ -z "$FACET_MCP_PLUGIN_ROOT" ]; then
  FACET_MCP_PLUGIN_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
fi
# Existing binary resolution uses Claude-compatible names on both hosts.
export CLAUDE_PLUGIN_ROOT="$FACET_MCP_PLUGIN_ROOT"
if [ -n "${PLUGIN_DATA:-}" ]; then
  export CLAUDE_PLUGIN_DATA="$PLUGIN_DATA"
fi

# Claude provides its project directory. Codex starts MCP servers in the
# session working directory; preserve that directory, including worktrees.
FACET_MCP_PROJECT_ROOT="${CLAUDE_PROJECT_DIR:-$(pwd -P)}"
if [ ! -d "$FACET_MCP_PROJECT_ROOT" ]; then
  printf 'facet: MCP project directory does not exist: %s\n' "$FACET_MCP_PROJECT_ROOT" >&2
  exit 1
fi

if [ "${CLAUDE_CODE_REMOTE:-}" = "true" ] && [ -z "${FACET_BINARY:-}" ]; then
  if [ -x "${HOME:-/root}/.local/bin/facet" ]; then
    export FACET_BINARY="${HOME:-/root}/.local/bin/facet"
  else
    export FACET_DEV_BUILD="${FACET_DEV_BUILD:-1}"
  fi
fi
exec "$FACET_MCP_PLUGIN_ROOT/bin/facet" mcp --root "$FACET_MCP_PROJECT_ROOT" "$@"
