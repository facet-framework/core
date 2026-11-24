#!/usr/bin/env sh
# @facet: delivery/distribution/public-core-release
# @facet-role: related
#
# Checks deployment environments, a reviewed main, protected v* tags and
# immutable releases before publishing.

set -eu

# One line, also when an API answer embedded in the message has several.
die() {
  echo "release-environment-check: $(printf '%s' "$*" | tr '\n' ' ')" >&2
  exit 2
}

[ "$#" -eq 2 ] || die "usage: release-environment-check.sh <environment> <owner>/<repo>"
ENV_NAME=$1
REPO=$2
command -v gh >/dev/null 2>&1 || die "gh is required"
command -v jq >/dev/null 2>&1 || die "jq is required"
NO_ADMIN_READ="the token cannot read it; give this step a token with read access to the repository administration (fine-grained: Administration read)"
env_json=$(gh api "repos/$REPO/environments/$ENV_NAME" 2>&1) ||
  die "environment '$ENV_NAME' cannot be read in $REPO ($env_json); create it with its required deployment rules"
if [ "$ENV_NAME" = npm ]; then
  printf '%s' "$env_json" | jq -e 'all(.protection_rules[]?; .type != "required_reviewers")' >/dev/null ||
    die "environment 'npm' must have no required reviewers; approval happens on npmjs.com"
  REVIEW_TEXT="has no required reviewers"
else
  case "${FACET_RELEASE_REQUIRE_SECOND_REVIEWER:-}" in
  true) SECOND_REVIEWER=1 ;;
  '' | false) SECOND_REVIEWER= ;;
  *) die "FACET_RELEASE_REQUIRE_SECOND_REVIEWER is '$FACET_RELEASE_REQUIRE_SECOND_REVIEWER'; set it to true, false or nothing" ;;
  esac
  printf '%s' "$env_json" | jq -e '[.protection_rules[]? | select(.type == "required_reviewers") | (.reviewers // []) | length] | any(. > 0)' >/dev/null ||
    die "environment '$ENV_NAME' has no required reviewers; nothing it gates would wait for an approval"
  if [ -n "$SECOND_REVIEWER" ]; then
    printf '%s' "$env_json" | jq -e '[.protection_rules[]? | select(.type == "required_reviewers" and ((.reviewers // []) | length) > 0) | .prevent_self_review == true] | any' >/dev/null ||
      die "environment '$ENV_NAME' lets a reviewer approve a run they triggered themselves, and FACET_RELEASE_REQUIRE_SECOND_REVIEWER is true; enable 'Prevent self-review' on its required reviewers"
    REVIEW_TEXT="requires a reviewer other than the one who triggered it"
  else
    echo "release-environment-check: notice: FACET_RELEASE_REQUIRE_SECOND_REVIEWER is not true, so the releaser may approve their own run in environment '$ENV_NAME'"
    REVIEW_TEXT="requires a reviewer (who may be the one who triggered it)"
  fi
fi
printf '%s' "$env_json" | jq -e '.can_admins_bypass == false' >/dev/null ||
  die "environment '$ENV_NAME' can be bypassed by repository administrators (can_admins_bypass: $(printf '%s' "$env_json" | jq -c '.can_admins_bypass')); disable 'Allow administrators to bypass configured protection rules'"
printf '%s' "$env_json" | jq -e '.deployment_branch_policy.custom_branch_policies == true and .deployment_branch_policy.protected_branches == false' >/dev/null ||
  die "environment '$ENV_NAME' does not limit deployments to selected branches and tags (deployment_branch_policy: $(printf '%s' "$env_json" | jq -c '.deployment_branch_policy'))"

policies=$(gh api "repos/$REPO/environments/$ENV_NAME/deployment-branch-policies?per_page=100" --paginate --slurp 2>&1) ||
  die "the branch policies of environment '$ENV_NAME' cannot be read ($policies)"
printf '%s' "$policies" | jq -e '
  [.[] | .branch_policies[] | {name, type}] == [{name: "main", type: "branch"}]' >/dev/null ||
  die "environment '$ENV_NAME' must allow deployments from the main branch only"

# --- main: a reviewed pull request, for administrators too -----------------
main_ok=
main_unreadable=
main_reasons=
note() { main_reasons="$main_reasons; $*"; }

if rules=$(gh api "repos/$REPO/rules/branches/main" 2>&1) && printf '%s' "$rules" | jq -e 'type == "array"' >/dev/null 2>&1; then
  ids=$(printf '%s' "$rules" | jq -r '[.[] | select(.type == "pull_request" and ((.parameters.required_approving_review_count // 0) >= 1)) | .ruleset_id] | unique | .[]')
  [ -n "$ids" ] || note "no branch ruleset requires a pull request with at least one approving review on main"
  for id in $ids; do
    if ! ruleset=$(gh api "repos/$REPO/rulesets/$id" 2>&1); then
      main_unreadable="ruleset $id of main cannot be read ($ruleset)"
    elif ! printf '%s' "$ruleset" | jq -e 'has("bypass_actors")' >/dev/null 2>&1; then
      main_unreadable="the bypass list of ruleset $id ('$(printf '%s' "$ruleset" | jq -r '.name // ""')') of main is not in the answer"
    elif printf '%s' "$ruleset" | jq -e '(.bypass_actors | length) == 0 and .enforcement == "active"' >/dev/null 2>&1; then
      main_ok=1
    else
      note "branch ruleset '$(printf '%s' "$ruleset" | jq -r '.name // ""')' lets $(printf '%s' "$ruleset" | jq -c '[.bypass_actors[]? | "\(.actor_type) \(.actor_id // "")"]') bypass the review on main; empty its bypass list"
    fi
  done
else
  main_unreadable="the rules of main cannot be read ($rules)"
fi

if [ -z "$main_ok" ]; then
  if protection=$(gh api "repos/$REPO/branches/main/protection" 2>&1); then
    if ! printf '%s' "$protection" | jq -e '(.required_pull_request_reviews.required_approving_review_count // 0) >= 1' >/dev/null 2>&1; then
      note "branch protection of main does not require a pull request with at least one approving review ('Require approvals')"
    elif ! printf '%s' "$protection" | jq -e '.enforce_admins.enabled == true' >/dev/null 2>&1; then
      note "branch protection of main does not apply to administrators ('Do not allow bypassing the above settings')"
    elif ! printf '%s' "$protection" | jq -e '[.required_pull_request_reviews.bypass_pull_request_allowances // {} | .[]? | length] | all(. == 0)' >/dev/null 2>&1; then
      note "branch protection of main lets someone merge without the required review ('Allow specified actors to bypass required pull requests')"
    else
      main_ok=1
    fi
  else
    case "$protection" in
    *"Branch not protected"*) note "main has no branch protection" ;;
    *) main_unreadable="the branch protection of main cannot be read ($protection)" ;;
    esac
  fi
fi
if [ -z "$main_ok" ]; then
  [ -z "$main_unreadable" ] || die "$main_unreadable; $NO_ADMIN_READ"
  die "main can change without a reviewed pull request: ${main_reasons#; }; require a pull request with at least one approving review on main that administrators cannot bypass (branch protection or a branch ruleset)"
fi

# --- v* tags: allow creation, restrict changes ----------------------------
tag_ok=
tag_unreadable=
tag_reasons=
if tagsets=$(gh api "repos/$REPO/rulesets?targets=tag&includes_parents=true&per_page=100" 2>&1) &&
  printf '%s' "$tagsets" | jq -e 'type == "array"' >/dev/null 2>&1; then
  ids=$(printf '%s' "$tagsets" | jq -r '.[] | select((.target // "tag") == "tag" and .enforcement == "active") | .id')
  [ -n "$ids" ] || tag_reasons="no active tag ruleset"
  for id in $ids; do
    if ! ruleset=$(gh api "repos/$REPO/rulesets/$id" 2>&1); then
      tag_unreadable="tag ruleset $id cannot be read ($ruleset)"
      continue
    fi
    name=$(printf '%s' "$ruleset" | jq -r '.name // ""')
    if ! printf '%s' "$ruleset" | jq -e '.enforcement == "active"
        and ([.conditions.ref_name.include[]?] | any(. == "refs/tags/v*" or . == "~ALL"))
        and ((.conditions.ref_name.exclude // []) | length) == 0' >/dev/null 2>&1; then
      tag_reasons="$tag_reasons; tag ruleset '$name' does not cover all refs/tags/v*"
    elif ! printf '%s' "$ruleset" | jq -e '(.bypass_actors | type) == "array"' >/dev/null 2>&1; then
      tag_unreadable="the bypass list of tag ruleset $id ('$name') is not in the answer"
    elif printf '%s' "$ruleset" | jq -e 'any(.rules[]?; .type == "creation")
        and (any(.bypass_actors[]; .actor_type == "Integration" and .actor_id == 15368 and .bypass_mode == "always") | not)' >/dev/null 2>&1; then
      die "tag ruleset '$name' restricts creation of v* tags; allow the release workflow to create them"
    elif ! printf '%s' "$ruleset" | jq -e '(["update", "deletion", "non_fast_forward"] - [.rules[]? | .type]) == []' >/dev/null 2>&1; then
      tag_reasons="$tag_reasons; tag ruleset '$name' does not restrict update, deletion and non-fast-forward changes to refs/tags/v*"
    elif printf '%s' "$ruleset" | jq -e '[.bypass_actors[] | select((.actor_type == "Integration" and .actor_id == 15368) or .actor_type == "DeployKey" | not)] | length == 0' >/dev/null 2>&1; then
      tag_ok=1
    else
      tag_reasons="$tag_reasons; tag ruleset '$name' lets $(printf '%s' "$ruleset" | jq -c '[.bypass_actors[] | select((.actor_type == "Integration" and .actor_id == 15368) or .actor_type == "DeployKey" | not) | "\(.actor_type) \(.actor_id // "")"]') update or delete v* tags"
    fi
  done
else
  tag_unreadable="the tag rulesets cannot be read ($tagsets)"
fi
if [ -z "$tag_ok" ]; then
  [ -z "$tag_unreadable" ] || die "$tag_unreadable; $NO_ADMIN_READ"
  die "v* tags lack the required protection: ${tag_reasons#; }; require update, deletion and non-fast-forward restrictions with an empty bypass list or only GitHub Actions and deploy keys"
fi

immutable=$(gh api "repos/$REPO/immutable-releases" 2>&1) || {
  if printf '%s\n' "$immutable" | grep -Eq '\(HTTP 404\)$'; then
    die "immutable releases are disabled in $REPO; enable them before publishing"
  fi
  die "immutable releases settings cannot be read in $REPO ($immutable); $NO_ADMIN_READ; verify immutable releases are enabled"
}
printf '%s' "$immutable" | jq -e '.enabled == true' >/dev/null 2>&1 ||
  die "immutable releases are disabled in $REPO; enable them before publishing"

echo "release-environment-check: environment '$ENV_NAME' $REVIEW_TEXT, has no administrator bypass and deploys from the main branch only; main needs a reviewed pull request; v* tags restrict changes; immutable releases are enabled"
