#!/usr/bin/env sh
# @facet-ignore-file
# The wiring of the public Core workflows:
# ci.yml gates every job behind ci-green, release.yml releases only a commit
# with a green CI run behind the release environment, and npm.yml stages
# packages from verified GitHub releases. Every action is pinned by a full
# commit SHA.
set -u

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
WF="${FACET_WORKFLOWS_DIR:-$ROOT/.github/workflows}"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT HUP INT TERM
FAILED=0
check() {
  if jq -e "$2" "$WORK/$3.json" >/dev/null; then
    printf 'ok   %s\n' "$1"
  else
    printf 'FAIL %s\n' "$1"
    FAILED=1
  fi
}

for f in ci release npm scorecard; do
  yq -o=json '.' "$WF/$f.yml" > "$WORK/$f.json" || { echo "FAIL fixture: $f.yml does not parse"; exit 1; }
done
jq -e '.jobs | length > 10' "$WORK/ci.json" >/dev/null || { echo "FAIL fixture: ci.yml has no jobs"; exit 1; }
[ ! -e "$WF/check.yml" ] || { echo "FAIL check.yml still exists beside ci.yml"; FAILED=1; }

check "CI runs on main, release/** pushes, pull requests and by hand" \
  '.on.push.branches == ["main", "release/**"] and (.on | has("pull_request")) and (.on | has("workflow_dispatch"))' ci
check "ci-green needs every other job, runs always, and is named ci-green" \
  '.jobs["ci-green"] as $g | $g.if == "always()" and $g.name == "ci-green"
   and (($g.needs | sort) == ([.jobs | keys[] | select(. != "ci-green")] | sort))' ci
check "ci-green accepts skipped only for dependency review" \
  '.jobs["ci-green"].steps[0].run | test("dependency-review") and test("success")' ci
# The ci-green step itself, run against results of the jobs it needs.
jq -r '.jobs["ci-green"].steps[0].run' "$WORK/ci.json" > "$WORK/green.sh"
jq -c '[.jobs | keys[] | select(. != "ci-green") | {key: ., value: {result: "success"}}] | from_entries' "$WORK/ci.json" > "$WORK/all.json"
green() { NEEDS=$(jq -c "$1" "$WORK/all.json") bash -eo pipefail "$WORK/green.sh" >/dev/null 2>&1; }
if green '.' && green '.["dependency-review"].result = "skipped"' &&
  ! green '.race.result = "failure"' && ! green '.native.result = "cancelled"' && ! green '.go.result = "skipped"' &&
  ! green '.["dependency-review"].result = "failure"'; then
  printf 'ok   ci-green passes all-success and a skipped dependency review, fails a failed, cancelled or other skipped job\n'
else
  printf 'FAIL ci-green decides on the needed results\n'
  FAILED=1
fi
check "Go is built and vetted on Linux, macOS and Windows with the go.mod version and the pinned release Go, and tested on Linux" \
  '.jobs.go.strategy.matrix.os == ["ubuntu-24.04", "macos-15", "windows-2025"] and .jobs.go.strategy.matrix.go == ["go.mod", "pinned"]
   and ([.jobs.go.steps[] | select(.run == "go build ./..." or .run == "go vet ./...") | .if // ""] == ["", ""])
   and ([.jobs.go.steps[] | select(.run == "go test ./...") | .if] == ["runner.os == '"'"'Linux'"'"'"])
   and ([.jobs.go.steps[] | select((.uses // "") | startswith("actions/setup-go@")) | .with] == [{
     "go-version": "${{ matrix.go == '"'"'pinned'"'"' && env.FACET_GO_VERSION || '"'"''"'"' }}",
     "go-version-file": "${{ matrix.go == '"'"'go.mod'"'"' && '"'"'go.mod'"'"' || '"'"''"'"' }}", "cache": false}])' ci
# One pinned Go for CI and release: both workflows carry the same exact
# version, and every setup-go but the go.mod leg and the format job uses it.
# A floating version (stable, a range) would let the toolchain move away from
# the one THIRD_PARTY.json was collected with.
for f in ci release; do
  check "$f.yml pins one exact Go version in FACET_GO_VERSION" '.env.FACET_GO_VERSION | type == "string" and test("^1\\.[0-9]+\\.[0-9]+$")' "$f"
done
if [ "$(jq -r .env.FACET_GO_VERSION "$WORK/ci.json")" = "$(jq -r .env.FACET_GO_VERSION "$WORK/release.json")" ]; then
  printf 'ok   ci.yml and release.yml pin the same Go\n'
else
  printf 'FAIL ci.yml and release.yml pin different Go versions\n'
  FAILED=1
fi
check "every setup-go in CI but the go matrix and the format job sets up the pinned Go" \
  '[.jobs | to_entries[] | select(.key != "go" and .key != "format") | .value.steps[]? | select((.uses // "") | startswith("actions/setup-go@")) | .with]
   | length > 8 and all(.["go-version"] == "${{ env.FACET_GO_VERSION }}" and (has("go-version-file") | not))' ci
check "every setup-go in the release workflow sets up the pinned Go" \
  '[.jobs[].steps[]? | select((.uses // "") | startswith("actions/setup-go@")) | .with]
   | length > 0 and all(.["go-version"] == "${{ env.FACET_GO_VERSION }}")' release
if grep -n -E 'go-version: *(stable|oldstable)' "$WF"/*.yml > "$WORK/floating"; then
  printf 'FAIL a public workflow sets up a floating Go:\n%s\n' "$(cat "$WORK/floating")"
  FAILED=1
else
  printf 'ok   no public workflow sets up a floating Go (stable, oldstable)\n'
fi
check "the six platforms run natively against the packaged dry run" \
  '[.jobs.native.strategy.matrix.include[].platform] | sort == ["darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64", "windows_amd64", "windows_arm64"]' ci
check "the FACET lint job requires its registry and strict dependencies" \
  '[.jobs["facet-lint"].steps[].run // ""] | join("\n") | test("FACET_STRICT_DEPS=1 sh tools/facet-lint/run-all.sh") and test("\\.facetrc\\.yaml docs/facets")' ci
check "receipt, changelog, packaging, reproducibility and dispatcher run through the release scripts" \
  '[.jobs[].steps[]?.run // ""] | join("\n") | test("release-receipt.sh verify") and test("release-changelog.sh notes")
   and test("release-package-core.sh") and test("dist-again") and test("release-dispatcher-check.sh")' ci

check "a release runs on main pushes and by hand, dry run by default" \
  '.on.push.branches == ["main"] and .on.workflow_dispatch.inputs.dry_run.default == true' release
check "the plan requires a reviewed release PR, successful main push CI, the receipt and a usable tag" \
  '(.jobs.plan.permissions["pull-requests"] == "read")
   and (any(.jobs.plan.steps[]; ((.run // "") | contains("release-pr-check.sh")) and .env.GH_TOKEN == "${{ github.token }}"))
   and ([.jobs.plan.steps[].run // ""] | join("\n") | contains("head_sha=$GITHUB_SHA&branch=main&event=push")
       and contains("refs/heads/main") and contains("release-publish.sh check-tag") and contains("release-receipt.sh verify")
       and contains("release-changelog.sh notes") and contains("only rc and release versions are released"))' release
check "publishing waits for build and every native check, behind the release environment" \
  '.jobs.publish.environment == "release" and (.jobs.publish.needs | sort) == ["build", "native", "plan"]
   and .jobs.publish.if == "needs.plan.outputs.dry_run != '"'"'true'"'"'"' release
check "the tag is pushed after attestation and before the release is created" \
  '[.jobs.publish.steps[] | (.uses // .run // "")
   | if startswith("actions/attest@") then "attest" elif contains("prepare-tag") then "tag"
     elif contains("release-publish.sh release") then "release" else empty end]
   == ["attest", "tag", "release"]' release
check "the Release workflow publishes with the job token and stages no packages" \
  '(.jobs | keys) == ["build", "native", "npm-dispatch", "plan", "publish"]
   and .jobs.publish.env.GH_TOKEN == "${{ github.token }}"
   and .jobs.publish.steps[-1].id == "release"
   and (.jobs.publish.steps[-1].run | startswith("sh scripts/release-publish.sh release core \"$KIND\" \"$VERSION\" \"$TAG\" dist \"$GITHUB_REPOSITORY\"\n"))
   and .jobs.publish.steps[-1].env.GH_TOKEN == null
   and (tostring | test("release-npm-|npm stage publish") | not)' release
check "only the dedicated npm-dispatch job grants actions write" \
  '.permissions == {contents: "read"}
   and ([.jobs | to_entries[] | select(.value.permissions == "write-all" or (try .value.permissions.actions catch null) == "write") | .key] == ["npm-dispatch"])
   and .jobs["npm-dispatch"].permissions == {actions: "write", contents: "read"}' release
check "npm dispatch follows successful publication when enabled and requires no checkout" \
  '.jobs["npm-dispatch"] as $dispatch
   | $dispatch.needs == "publish" and $dispatch.if == "vars.FACET_NPM_PUBLISH == '\''true'\''"
   and $dispatch["continue-on-error"] == true and $dispatch.environment == null
   and $dispatch.env == {TAG: "${{ needs.publish.outputs.tag }}", GH_TOKEN: "${{ github.token }}"}
   and .jobs.publish.outputs.tag == "${{ steps.release.outputs.tag }}"
   and (.jobs.publish.steps[-1].run | endswith("echo \"tag=$TAG\" >> \"$GITHUB_OUTPUT\"\n"))
   and ($dispatch.steps | length) == 1 and ($dispatch.steps[0] | has("uses") | not)
   and ([.jobs[].steps[] | select((.run // "") | contains("gh workflow run npm.yml"))] == $dispatch.steps)' release

jq -r '.jobs["npm-dispatch"].steps[0].run' "$WORK/release.json" > "$WORK/dispatch.sh"
mkdir "$WORK/dispatch-bin"
cat > "$WORK/dispatch-bin/gh" <<'SH'
#!/bin/sh
case "$1 $2" in
  'release view') [ "$#" -eq 7 ] && [ "$3" = "$TAG" ] && [ "$4" = --repo ] &&
    [ "$5" = "$GITHUB_REPOSITORY" ] && [ "$6 $7" = '--json isDraft,tagName' ] || exit 2
    printf '{"isDraft":%s,"tagName":"%s"}\n' "$FAKE_DRAFT" "$TAG"
    ;;
  'workflow run') printf '%s\n' "$*" >> "$DISPATCH_LOG"; exit "$FAKE_DISPATCH_EXIT" ;;
  *) exit 2 ;;
esac
SH
chmod +x "$WORK/dispatch-bin/gh"
dispatch() {
  PATH="$WORK/dispatch-bin:$PATH" GITHUB_REPOSITORY=example/core TAG=v0.1.0-rc.4 \
    FAKE_DRAFT="$1" FAKE_DISPATCH_EXIT="$2" DISPATCH_LOG="$WORK/dispatch.log" \
    bash -eo pipefail "$WORK/dispatch.sh" > "$WORK/dispatch.out" 2>&1
}
dispatch_command='workflow run npm.yml --repo example/core --ref main -f tag=v0.1.0-rc.4'
if dispatch false 0 && [ "$(cat "$WORK/dispatch.log")" = "$dispatch_command" ] && [ ! -s "$WORK/dispatch.out" ] &&
  dispatch false 1 && [ "$(cat "$WORK/dispatch.log")" = "$(printf '%s\n%s' "$dispatch_command" "$dispatch_command")" ] &&
  grep -q '^::warning::.*gh workflow run npm.yml --repo example/core --ref main -f tag=v0.1.0-rc.4$' "$WORK/dispatch.out" &&
  dispatch true 0 && [ "$(wc -l < "$WORK/dispatch.log")" -eq 2 ]; then
  printf 'ok   dispatch uses main with the published tag as input, warns on failure and skips drafts\n'
else
  printf 'FAIL npm dispatch\n  %s\n' "$(cat "$WORK/dispatch.out")"
  FAILED=1
fi

for f in release npm; do
  check "$f.yml references only the existing read-only settings secret" \
    '[.. | strings | select(test("\\bsecrets\\b"; "i"))] == ["${{ secrets['\''RELEASE_SETTINGS_TOKEN'\''] || github.token }}"]' "$f"
done
check "the release workflow pins the public layout for every job" \
  '.env.FACET_RELEASE_LAYOUT == "public"' release
check "CI pins the public layout too" '.env.FACET_RELEASE_LAYOUT == "public"' ci
check "dry runs and publishing runs queue in separate concurrency groups, never cancelled" \
  '.concurrency["cancel-in-progress"] == false
   and (.concurrency.group | test("inputs.dry_run") and test("'"'"'dry-run'"'"'") and test("'"'"'publish'"'"'"))' release
check "the plan checks only the release environment" \
  '[.jobs.plan.steps[] | select((.run // "") | contains("release-environment-check.sh"))] as $s
   | ($s | length) == 1
   and $s[0].run == "sh scripts/release-environment-check.sh release \"$GITHUB_REPOSITORY\"" and $s[0].if == null
   and $s[0].env.GH_TOKEN == "${{ secrets['\''RELEASE_SETTINGS_TOKEN'\''] || github.token }}"
   and $s[0].env.FACET_RELEASE_REQUIRE_SECOND_REVIEWER == "${{ vars.FACET_RELEASE_REQUIRE_SECOND_REVIEWER }}"' release
check "the environment check comes before the tag check, which comes before any build" \
  '[.jobs.plan.steps[] | select((.run // "") | test("release-environment-check.sh|check-tag")) | .run | test("check-tag")]
   == [false, true]' release
check "missing third-party notices fail the CI licenses job and the release plan" \
  '[.jobs.licenses.steps[].run // ""] | join("\n") | test("sh scripts/release-notice-check.sh")' ci
check "the CI licenses job checks THIRD_PARTY.json against the six-platform closure with the Go the release builds with" \
  '.jobs.licenses.steps as $s
   | ([$s[] | select((.uses // "") | startswith("actions/setup-go@")) | .with["go-version"]] == ["${{ env.FACET_GO_VERSION }}"])
   and ([$s[].run // ""] | join("\n") | test("sh scripts/release-third-party-check.sh"))' ci
check "the release build sets up the same pinned Go that the licenses job checks" \
  '[.jobs.build.steps[] | select((.uses // "") | startswith("actions/setup-go@")) | .with["go-version"]] == ["${{ env.FACET_GO_VERSION }}"]
   and ([.jobs.build.steps[].run // ""] | join("\n") | test("release-package-core.sh"))' release
check "the release plan runs the notice check" \
  '[.jobs.plan.steps[].run // ""] | join("\n") | test("sh scripts/release-notice-check.sh")' release
check "npm runs only on workflow dispatch with a required tag" \
  '(.on | keys) == ["workflow_dispatch"]
   and (.on.workflow_dispatch.inputs | keys) == ["tag"]
   and .on.workflow_dispatch.inputs.tag.required == true and .on.workflow_dispatch.inputs.tag.type == "string"' npm
check "only the npm job can request OIDC, and GitHub access is read-only" \
  '.permissions == {} and (.jobs | keys) == ["npm"] and .jobs.npm.environment == "npm"
   and .jobs.npm.permissions == {contents: "read", "id-token": "write", attestations: "read"}' npm
check "npm validates the input tag before checkout and checks the deployment environment" \
  '.jobs.npm.steps as $s
   | $s[0].env.REF == "${{ github.ref }}" and $s[0].if == null
   and $s[1].id == "tag" and $s[1].env.TAG == "${{ inputs.tag }}"
   and .jobs.npm.concurrency.group == "npm-${{ inputs.tag }}"
   and ([$s[] | select((.uses // "") | startswith("actions/checkout@")) | .with] ==
     [{ref: "${{ github.workflow_sha }}", "persist-credentials": false},
      {ref: "refs/tags/${{ steps.tag.outputs.tag }}", path: "release-source", "persist-credentials": false}])
   and any($s[]; .run == "sh scripts/release-environment-check.sh npm \"$GITHUB_REPOSITORY\"")' npm
check "npm downloads and verifies the release before packing and staging" \
  '.jobs.npm.steps as $s
   | ([$s[] | .run // "" | select(test("gh release download|release-npm-pack.sh|release-npm-publish.sh")) |
       if contains("gh release download") then "verify" elif contains("release-npm-pack.sh") then "pack" else "stage" end] == ["verify", "pack", "stage"])
   and any($s[]; .env.GH_TOKEN == "${{ github.token }}" and .env.TAG == "${{ steps.tag.outputs.tag }}"
     and ((.run // "") | test("gh release download.*sha256sum --check --strict checksums.txt.*gh attestation verify"; "m")
       and contains("--signer-workflow \"$GITHUB_REPOSITORY/.github/workflows/release.yml\"")))
   and all($s[]; ((.uses // "") | startswith("actions/download-artifact@") | not))' npm
check "npm uses the main packer and the versioned release source" \
  'any(.jobs.npm.steps[]; .run == "sh scripts/release-npm-pack.sh --source release-source release-source/dist")' npm
check "npm 11.15.0 or later is set up for staged publishing" \
  '[.jobs.npm.steps[] | select((.uses // "") | startswith("actions/setup-node@")) | .with["node-version"]] == [24]
   and ([.jobs.npm.steps[].run // ""] | join("\n") | test("npm install -g npm@11\\.(1[5-9]|[2-9][0-9])\\."))' npm
if grep -n -Ei 'NPM.*TOKEN|NODE_AUTH_TOKEN|_authToken' "$WF/npm.yml"; then
  printf 'FAIL npm.yml names a static npm token\n'
  FAILED=1
fi

jq -er '.jobs.npm.steps[0].run' "$WORK/npm.json" > "$WORK/npm-ref.sh"
npm_ref() { REF="$1" bash -eo pipefail "$WORK/npm-ref.sh" > "$WORK/npm-ref.out" 2>&1; }
if [ -s "$WORK/npm-ref.sh" ] && npm_ref refs/heads/main && ! npm_ref refs/heads/release/0.1.0-rc.4 &&
  ! npm_ref refs/tags/v0.1.0-rc.4 && ! npm_ref refs/tags/main && ! npm_ref '' &&
  grep -q '^::error::npm staging must run from refs/heads/main$' "$WORK/npm-ref.out"; then
  printf 'ok   the first npm step accepts only refs/heads/main\n'
else
  printf 'FAIL the first npm step checks the workflow ref\n  %s\n' "$(cat "$WORK/npm-ref.out")"
  FAILED=1
fi

jq -r '.jobs.npm.steps[] | select(.id == "tag") | .run' "$WORK/npm.json" > "$WORK/tag.sh"
tag_step() { TAG="$1" GITHUB_OUTPUT="$WORK/tag.out" bash -eo pipefail "$WORK/tag.sh" >"$WORK/tag.log" 2>&1; }
if tag_step v0.1.0-rc.4 && tag_step v1.2.3 &&
  [ "$(cat "$WORK/tag.out")" = "$(printf 'tag=v0.1.0-rc.4\ntag=v1.2.3')" ] &&
  ! tag_step main && ! tag_step '' && ! tag_step v01.2.3 && ! tag_step v0.1.0-rc.0 &&
  ! tag_step v0.1.0-dev.20261123.1 && ! tag_step v0.1.0+gabcdef &&
  ! tag_step "$(printf 'v1.2.3\ninjected=true')"; then
  printf 'ok   the tag step accepts Core versions and rejects malformed tags and other refs\n'
else
  printf 'FAIL the tag step validates Core tags\n  %s\n' "$(cat "$WORK/tag.log")"
  FAILED=1
fi

# Execute the verification step with release assets and recording GitHub CLI
# fixtures. Real checksums must pass before any attestation is requested.
mkdir -p "$WORK/npm-bin" "$WORK/release-assets" "$WORK/release-source"
commit=0123456789abcdef0123456789abcdef01234567
tag=v0.1.0-rc.4
version=${tag#v}
for platform in darwin_amd64 darwin_arm64 linux_amd64 linux_arm64 windows_amd64 windows_arm64; do
  case "$platform" in windows_*) ext=zip ;; *) ext=tar.gz ;; esac
  archive="facet_${version}_$platform.$ext"
  printf '%s\n' "$platform" > "$WORK/release-assets/$archive"
  jq -cn --arg platform "$platform" --arg archive "$archive" '{platform: $platform, archive: $archive}'
done > "$WORK/platforms.jsonl"
(cd "$WORK/release-assets" && sha256sum facet_* > checksums.txt)
jq -n --arg version "$version" --arg commit "$commit" --slurpfile platforms "$WORK/platforms.jsonl" \
  '{product: "core", version: $version, source_commit: $commit, platforms: $platforms}' > "$WORK/release-assets/release-manifest.json"
printf '#!/bin/sh\nprintf "%%s\\n" "%s"\n' "$commit" > "$WORK/npm-bin/git"
cat > "$WORK/npm-bin/gh" <<'SH'
#!/bin/sh
case "$1:$2" in
  release:view) printf '{"isDraft":%s,"tagName":"%s"}\n' "${FAKE_DRAFT:-false}" "$TAG" ;;
  release:download) mkdir dist && cp "$RELEASE_ASSETS/"* dist/ ;;
  attestation:verify)
    [ "$*" = "attestation verify $3 --repo $GITHUB_REPOSITORY --signer-workflow $GITHUB_REPOSITORY/.github/workflows/release.yml --source-digest $SOURCE_COMMIT --source-ref refs/heads/main" ] || exit 1
    printf '%s\n' "${3#./}" >> "$ATTEST_LOG"
    [ "${FAKE_REJECT:-}" != "$3" ] ;;
  *) exit 2 ;;
esac
SH
chmod +x "$WORK/npm-bin/git" "$WORK/npm-bin/gh"
jq -r '.jobs.npm.steps[] | select((.run // "") | contains("gh release download")) | .run' "$WORK/npm.json" > "$WORK/verify.sh"
verify_release() {
  mkdir "$WORK/release-source/$1"
  : > "$WORK/attest.log"
  (cd "$WORK/release-source/$1" && PATH="$WORK/npm-bin:$PATH" TAG="$tag" RUNNER_TEMP="$WORK" \
    GITHUB_REPOSITORY=example/core RELEASE_ASSETS="$WORK/release-assets" SOURCE_COMMIT="$commit" \
    ATTEST_LOG="$WORK/attest.log" bash -eo pipefail "$WORK/verify.sh") > "$WORK/verify.out" 2>&1
}
if verify_release good && cmp -s "$WORK/core-archives" "$WORK/attest.log" &&
  [ "$(wc -l < "$WORK/attest.log")" -eq 6 ] &&
  ! FAKE_REJECT="./facet_${version}_linux_amd64.tar.gz" verify_release unsigned &&
  grep -Fxq "facet_${version}_linux_amd64.tar.gz" "$WORK/attest.log" &&
  ! FAKE_DRAFT=true verify_release draft && [ ! -e "$WORK/release-source/draft/dist" ]; then
  printf 'ok   verification accepts all six attestations and refuses a rejected attestation and a draft release\n'
else
  printf 'FAIL release verification\n  %s\n' "$(cat "$WORK/verify.out")"
  FAILED=1
fi
printf '%s\n' changed > "$WORK/release-assets/facet_${version}_linux_amd64.tar.gz"
if ! verify_release changed && grep -q 'FAILED' "$WORK/verify.out" && [ ! -s "$WORK/attest.log" ]; then
  printf 'ok   verification stops a changed archive before requesting attestations\n'
else
  printf 'FAIL changed archive verification\n  %s\n' "$(cat "$WORK/verify.out")"
  FAILED=1
fi

check "Scorecard publishes with only the required write permissions" \
  '(.permissions == "read-all")
   and (.jobs.scorecard.permissions == {contents: "read", "id-token": "write", "security-events": "write"})
   and any(.jobs.scorecard.steps[]; ((.uses // "") | startswith("ossf/scorecard-action@")) and .with.publish_results == true)' scorecard

# The plan's gate steps themselves, run as the runner runs them (bash -eo
# pipefail) against a stub gh: the ref, the CI runs of this commit and the
# release environment.
mkdir "$WORK/bin" "$WORK/api"
cat > "$WORK/bin/gh" <<'SH'
#!/bin/sh
printf '%s\n' "$*" >> "$GH_LOG"
case "$2" in
  */actions/workflows/ci.yml/runs*)
    [ -n "${FAKE_GREEN:-}" ] || { echo "gh: HTTP 502" >&2; exit 1; }
    echo "$FAKE_GREEN"; exit 0 ;;
esac
f="$FAKE_API/$(printf '%s' "$2" | tr '/' '_')"
[ -f "$f" ] || { echo "gh: Not Found (HTTP 404)" >&2; exit 1; }
cat "$f"
SH
chmod +x "$WORK/bin/gh"
step() { jq -r --arg n "$1" '.jobs.plan.steps[] | select(.name == $n) | .run' "$WORK/release.json" > "$WORK/step.sh"; [ -s "$WORK/step.sh" ]; }
runstep() { (cd "$ROOT" && PATH="$WORK/bin:$PATH" GH_LOG="$WORK/gh.log" FAKE_API="$WORK/api" GITHUB_SHA=0123abc \
  GITHUB_REPOSITORY=example/core bash -eo pipefail "$WORK/step.sh") >"$WORK/step.out" 2>&1; }

if step "Releases come from main only" && REF=refs/heads/main runstep &&
  ! REF=refs/heads/release/1.21.0 runstep && ! REF=refs/tags/v1.21.0 runstep && ! REF= runstep; then
  printf 'ok   the ref step passes main and refuses a release branch, a tag and an empty ref\n'
else
  printf 'FAIL the ref step decides on main\n  %s\n' "$(cat "$WORK/step.out")"
  FAILED=1
fi
: > "$WORK/gh.log"
if step "Wait for CI on the squash commit" && FAKE_GREEN=success runstep &&
  grep -q 'api repos/example/core/actions/workflows/ci.yml/runs?head_sha=0123abc&branch=main&event=push' "$WORK/gh.log" &&
  ! FAKE_GREEN=0 runstep && ! FAKE_GREEN= runstep; then
  printf 'ok   the CI step passes one green ci.yml run of the commit, refuses a failed run and a failed API call\n'
else
  printf 'FAIL the CI step decides on the green runs of the commit\n  %s %s\n' "$(cat "$WORK/step.out")" "$(cat "$WORK/gh.log")"
  FAILED=1
fi
E="$WORK/api/repos_example_core_environments_release"
printf '%s\n' '{"name": "release", "can_admins_bypass": false, "protection_rules": [{"type": "required_reviewers", "prevent_self_review": true, "reviewers": [{"type": "User"}]}],
  "deployment_branch_policy": {"protected_branches": false, "custom_branch_policies": true}}' > "$E"
printf '%s\n' '[{"branch_policies": [{"name": "main", "type": "branch"}]}]' > "${E}_deployment-branch-policies?per_page=100"
# The repository protections the same step reads: main behind a ruleset that
# requires one approval and nobody may bypass, protected v* tags and
# immutable releases.
A="$WORK/api/repos_example_core"
printf '%s\n' '[{"type": "pull_request", "ruleset_id": 11, "parameters": {"required_approving_review_count": 1}}]' > "${A}_rules_branches_main"
printf '%s\n' '{"id": 11, "name": "main", "enforcement": "active", "bypass_actors": []}' > "${A}_rulesets_11"
printf '%s\n' '[{"id": 21, "name": "tags", "target": "tag", "enforcement": "active"}]' > "${A}_rulesets?targets=tag&includes_parents=true&per_page=100"
printf '%s\n' '{"id": 21, "name": "tags", "enforcement": "active", "bypass_actors": [],
  "conditions": {"ref_name": {"include": ["refs/tags/v*"], "exclude": []}}, "rules": [{"type": "update"}, {"type": "deletion"}, {"type": "non_fast_forward"}]}' > "${A}_rulesets_21"
printf '%s\n' '{"enabled": true}' > "${A}_immutable-releases"
if step "The release environment requires a reviewer and deploys from main only" && runstep; then
  jq '.protection_rules[0].prevent_self_review = false' "$E" > "$E.new" && mv "$E.new" "$E"
  if FACET_RELEASE_REQUIRE_SECOND_REVIEWER=true runstep; then
    printf 'FAIL the release environment step lets self-review through although a second reviewer is required\n'
    FAILED=1
  elif FACET_RELEASE_REQUIRE_SECOND_REVIEWER= runstep && grep -q 'releaser may approve their own run' "$WORK/step.out"; then
    printf 'ok   the release environment step refuses self-review when the repository requires a second reviewer and passes it with a notice otherwise\n'
  else
    printf 'FAIL the release environment step passes self-review with a notice when no second reviewer is required\n  %s\n' "$(cat "$WORK/step.out")"
    FAILED=1
  fi
  rm "${A}_rulesets_21"
  if runstep; then
    printf 'FAIL the release environment step lets an unreadable tag ruleset through\n'
    FAILED=1
  else
    rm "$E" "${E}_deployment-branch-policies?per_page=100"
    if runstep; then
      printf 'FAIL the release environment step lets a missing environment through\n'
      FAILED=1
    else
      printf 'ok   the release environment step passes a protected environment and repository, refuses a missing one and an unreadable tag ruleset\n'
    fi
  fi
else
  printf 'FAIL the release environment step passes a protected environment\n  %s\n' "$(cat "$WORK/step.out")"
  FAILED=1
fi

check "only the publish job may write contents, and only publish gets an OIDC token" \
  '.permissions == {"contents": "read"}
   and ([.jobs | to_entries[] | select(.value.permissions.contents == "write") | .key] == ["publish"])
   and ([.jobs | to_entries[] | select(.value.permissions["id-token"] == "write") | .key] == ["publish"])' release

# Every uses: is owner/repo[/path]@<40 hex> # <tag>. The workflows use public
# tags and GitHub-hosted runners.
pins=1
forbidden='internal/(core|atlas)|self-hosted'
module_owner=$(sed -n 's|^module[[:space:]]*github\.com/\([^/]*\)/.*|\1|p' "$ROOT/go.mod")
if [ -n "$module_owner" ] && [ "$module_owner" != facet-framework ]; then
  forbidden="$forbidden|$module_owner"
fi
for f in "$WF"/*.yml; do
  grep -n 'uses:' "$f" | grep -vE 'uses: [A-Za-z0-9_.-]+/[A-Za-z0-9_./-]+@[0-9a-f]{40} # v[0-9]+(\.[0-9]+)*$' > "$WORK/unpinned" &&
    { pins=0; printf 'FAIL unpinned action in %s:\n%s\n' "${f##*/}" "$(cat "$WORK/unpinned")"; FAILED=1; }
  if grep -n -i -E "$forbidden" "$f" > "$WORK/private"; then
    printf 'FAIL %s names something private:\n%s\n' "${f##*/}" "$(cat "$WORK/private")"
    FAILED=1
  fi
done
[ "$pins" = 1 ] && printf 'ok   every action is pinned by a full commit SHA with its version tag\n'

exit "$FAILED"
