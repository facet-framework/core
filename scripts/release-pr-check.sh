#!/usr/bin/env sh
# @facet: delivery/distribution/public-core-release
# @facet-role: related
#
# Verify that the checked-out commit is the reviewed release PR's squash
# result. The PR head may have been deleted after merge; use its saved SHA.

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)

die() {
  echo "release-pr-check: $*" >&2
  exit 2
}

[ "$#" -eq 2 ] || die "usage: release-pr-check.sh <version> <owner>/<repo>"
version=$1
repo=$2
branch="release/$version"

receipt_version=$(sh "$SCRIPT_DIR/release-receipt.sh" version) || die "cannot read the Core release receipt"
[ "$version" = "$receipt_version" ] ||
  die "requested version $version differs from the Core release receipt ($receipt_version)"
kind=$(sh "$SCRIPT_DIR/release-version.sh" kind "$version") ||
  die "receipt version $version disagrees with release/core-version.json"
case "$kind" in
rc | release) ;;
*) die "$version is not a release candidate or a stable release" ;;
esac

commit=$(git -C "$ROOT" rev-parse HEAD)
tree=$(git -C "$ROOT" rev-parse 'HEAD^{tree}')
# The workflow uses a shallow checkout. Read the parent from the commit header.
squash_object=$(git -C "$ROOT" cat-file commit "$commit")
squash_parent=$(printf '%s\n' "$squash_object" | awk '
  /^$/ { exit }
  /^parent / { parent = $2; count++ }
  END { if (count != 1) exit 1; print parent }') ||
  die "$commit must have exactly one parent for a squash release"

pulls=$(gh api --paginate --slurp "repos/$repo/commits/$commit/pulls?per_page=100") ||
  die "cannot find pull requests for $commit; the job token needs Pull requests read"
number=$(printf '%s' "$pulls" | jq -er --arg sha "$commit" '
  [ .[][] | select(.merge_commit_sha == $sha) | .number ]
  | select(length == 1) | .[0] | select(type == "number")') ||
  die "expected one pull request that produced $commit"
pull=$(gh api "repos/$repo/pulls/$number") ||
  die "cannot read pull request #$number; the job token needs Pull requests read"
printf '%s' "$pull" | jq -e --arg sha "$commit" '
  .merged == true and .merge_commit_sha == $sha and (.merged_at | type == "string")' >/dev/null ||
  die "pull request #$number was not merged as $commit"
printf '%s' "$pull" | jq -e --arg repo "$repo" '
  .base.ref == "main" and .base.repo.full_name == $repo' >/dev/null ||
  die "pull request #$number must target $repo main"
printf '%s' "$pull" | jq -e --arg repo "$repo" --arg branch "$branch" '
  .head.ref == $branch and .head.repo.full_name == $repo' >/dev/null ||
  die "pull request #$number must come from $repo $branch"
head=$(printf '%s' "$pull" | jq -er '.head.sha | select(test("^[a-f0-9]{40}$"))') ||
  die "pull request #$number has no valid head SHA"
merged_at=$(printf '%s' "$pull" | jq -r .merged_at)
author=$(printf '%s' "$pull" | jq -er '.user.id | select(type == "number")') ||
  die "pull request #$number has no valid author ID"

reviews=$(gh api --paginate --slurp "repos/$repo/pulls/$number/reviews?per_page=100") ||
  die "cannot read reviews of pull request #$number; the job token needs Pull requests read"
printf '%s' "$reviews" | jq -e --arg head "$head" --arg merged "$merged_at" --argjson author "$author" '
  [ .[][] | select((.user.id | type) == "number" and .user.id != $author
      and (.submitted_at | type == "string") and .submitted_at <= $merged
      and (.state | IN("APPROVED", "CHANGES_REQUESTED", "DISMISSED"))) ]
  | group_by(.user.id)
  | any(sort_by(.submitted_at, .id) | last | .state == "APPROVED" and .commit_id == $head
      and (.author_association | IN("OWNER", "MEMBER", "COLLABORATOR")))' >/dev/null ||
  die "pull request #$number has no approving review from an owner, member or collaborator for $head at merge time"

head_commit=$(gh api "repos/$repo/git/commits/$head") ||
  die "cannot read pull request head $head; the job token needs Contents read"
head_tree=$(printf '%s' "$head_commit" | jq -er --arg head "$head" '
  select(.sha == $head) | .tree.sha | select(test("^[a-f0-9]{40}$"))') ||
  die "pull request head $head has no valid tree SHA"
printf '%s' "$head_commit" | jq -e --arg parent "$squash_parent" '
  select(.parents | type == "array") | [.parents[].sha] == [$parent]' >/dev/null ||
  die "pull request #$number head $head must have squash parent $squash_parent as its only parent"
[ "$tree" = "$head_tree" ] ||
  die "merge commit $commit has tree $tree, but approved pull request #$number has tree $head_tree"

echo "release-pr-check: $commit matches the approved $branch tree from pull request #$number"
