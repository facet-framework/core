#!/usr/bin/env sh
# Shared dist-tag policy for npm release plans and staging.

npm_dist_tag() {
  # Older stable versions still select latest. Check the current registry tag
  # before staging or approving a backfill to avoid moving latest backwards.
  case "${2:-}:${3:-core}:$1" in
    dev:*) printf '%s\n' dev ;;
    *:core:0.0.*-* | *:core:0.1.0-*) printf '%s\n' latest ;;
    *-*) printf '%s\n' next ;;
    *) printf '%s\n' latest ;;
  esac
}
