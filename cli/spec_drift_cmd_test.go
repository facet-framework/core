package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/facet-framework/core/internal/spec"
	"github.com/facet-framework/core/registry"
)

func pinDriftFixture(t *testing.T, p specProject, body string) []byte {
	t.Helper()
	commit := p.markdown(t, "draft", body)
	out := p.command(t, 0, "spec", "update")
	lock, raw := p.pin(t)
	if lock.Scenarios[capCase].Commit != commit || lock.Scenarios[unknownCase].Commit != commit ||
		lock.Files["pricing/discounts"].Status != "draft" || !strings.Contains(out, "added product:"+capCase) {
		t.Fatalf("fixture did not pin two draft scenarios at %s: %+v\n%s", commit, lock, out)
	}
	return raw
}

const unknownCase = "pricing/discounts#unknown"

func driftRecord(t *testing.T, p specProject, name, ref string, lineage bool) string {
	t.Helper()
	id := "checkout/pricing/" + name
	record := "schema_version: '0.1'\nid: " + id + "\nlifecycle: planned\nintent: {summary: Check pricing.}\n"
	record += "lineage:\n  owner: {team: checkout}\n  created_at: '2026-11-23'\n"
	if lineage {
		record += "  specified_by: ['product:" + ref + "']\n"
	} else {
		record += "contract:\n  scenarios:\n    - id: cap\n      statement: Given a cart\n      specified_by: product:" + ref + "\n"
	}
	writeSpecFixture(t, p.root, "docs/facets/features/"+id+".yaml", record)
	return id
}

func specDriftJSON(t *testing.T, p specProject, code int, flags ...string) []facet.SpecDriftFinding {
	t.Helper()
	out := p.command(t, code, append([]string{"spec", "drift", "--json"}, flags...)...)
	var report facet.SpecDriftReport
	decoder := json.NewDecoder(strings.NewReader(out))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&report); err != nil || report.Findings == nil {
		t.Fatalf("invalid drift report: %v\n%s", err, out)
	}
	for _, f := range report.Findings {
		if f.State == "" || f.Source != "product" || f.Records == nil || f.Detail == "" ||
			(f.State != spec.Unmeasured && !strings.HasPrefix(f.Address, "product:")) {
			t.Fatalf("incomplete finding: %+v", f)
		}
	}
	return report.Findings
}

func TestSpecDriftStepsAndCachedStrict(t *testing.T) {
	t.Setenv("FACET_STRICT_DEPS", "")
	p := newSpecProject(t)
	before := pinDriftFixture(t, p, pricingScenarios)
	id := driftRecord(t, p, "discounts", capCase, false)
	p.markdown(t, "draft", strings.Replace(pricingScenarios, "50%", "40%", 1))
	got := specDriftJSON(t, p, 0, "--diff", "product")
	if len(got) != 1 || got[0].State != spec.SpecMoved || got[0].Address != "product:"+capCase ||
		!reflect.DeepEqual(got[0].Records, []string{id}) ||
		got[0].Detail != "steps changed at follow. "+spec.ChangedSpecAdvice ||
		!strings.Contains(got[0].Diff, "--- lock\n+++ follow\n- Outcome the discount is 50%\n+ Outcome the discount is 40%") {
		t.Fatalf("unexpected step drift: %+v", got)
	}
	specGit(t, p.upstream, "branch", "-m", "unavailable")
	if out := p.command(t, 0, "spec", "drift", "--cached"); !strings.HasSuffix(strings.TrimSpace(out), spec.ChangedSpecAdvice) {
		t.Fatalf("text notice is missing the reporting instruction: %s", out)
	}
	if failed := specDriftJSON(t, p, 0); len(failed) != 1 || failed[0].State != spec.Unmeasured {
		t.Fatalf("online drift did not report the failed fetch: %+v", failed)
	}
	if cached := specDriftJSON(t, p, 1, "--strict", "--cached", "--diff"); !reflect.DeepEqual(cached, got) {
		t.Fatalf("failed fetch changed the cached branch: %+v", cached)
	}
	if _, after := p.pin(t); !bytes.Equal(before, after) {
		t.Fatal("drift rewrote the lock")
	}
}

func TestSpecDriftContextAndOrphan(t *testing.T) {
	p := newSpecProject(t)
	const background = "Background:\n  Given the 2025 price list\n"
	pinDriftFixture(t, p, background+pricingScenarios)
	id := driftRecord(t, p, "discounts", unknownCase, false)
	p.markdown(t, "draft", strings.Replace(background, "2025", "2026", 1)+strings.Split(pricingScenarios, "@case:unknown")[0])
	got := specDriftJSON(t, p, 0, "--diff")
	if len(got) != 2 || got[0].State != spec.SpecMoved || got[0].Address != "product:"+capCase ||
		got[0].Detail != "context changed at follow. "+spec.ChangedSpecAdvice || got[0].Diff != "" ||
		got[1].State != spec.Orphaned || got[1].Address != "product:"+unknownCase ||
		!reflect.DeepEqual(got[1].Records, []string{id}) ||
		!strings.Contains(got[1].Diff, "- Outcome the discount is 0%") ||
		!strings.HasSuffix(got[1].Detail, spec.ChangedSpecAdvice) {
		t.Fatalf("unexpected context drift and orphan: %+v", got)
	}
	specGit(t, p.upstream, "rm", "specs/pricing.md")
	p.commit(t)
	got = specDriftJSON(t, p, 0)
	if len(got) != 2 || got[0].State != spec.Orphaned || got[1].State != spec.Orphaned {
		t.Fatalf("removed source directory did not orphan both pins: %+v", got)
	}
}

func TestSpecDriftClaimsAndDeprecation(t *testing.T) {
	p := newSpecProject(t)
	const file = "pricing/discounts"
	pinDriftFixture(t, p, pricingScenarios)
	claim := driftRecord(t, p, "case-owner", capCase, false)
	if got := specDriftJSON(t, p, 0, "--strict"); len(got) != 0 {
		t.Fatalf("draft scenarios produced findings: %+v", got)
	}
	p.markdown(t, "approved", pricingScenarios)
	got := specDriftJSON(t, p, 0)
	if len(got) != 1 || got[0].State != spec.Unclaimed || got[0].Address != "product:"+unknownCase ||
		len(got[0].Records) != 0 || !strings.Contains(got[0].Detail, "no record claims the file") {
		t.Fatalf("unexpected unclaimed report: %+v", got)
	}
	first := driftRecord(t, p, "file-owner-a", file, true)
	second := driftRecord(t, p, "file-owner-b", file, true)
	got = specDriftJSON(t, p, 1, "--strict", "--cached")
	if len(got) != 1 || got[0].State != spec.Unclaimed || got[0].Address != "product:"+unknownCase ||
		!reflect.DeepEqual(got[0].Records, []string{first, second}) {
		t.Fatalf("file lineage attribution is incomplete: %+v", got)
	}
	p.markdown(t, "deprecated", pricingScenarios)
	got = specDriftJSON(t, p, 0)
	if len(got) != 1 || got[0].State != spec.Deprecated || got[0].Address != "product:"+file ||
		!reflect.DeepEqual(got[0].Records, []string{claim, first, second}) {
		t.Fatalf("unexpected deprecation: %+v", got)
	}
	if out := p.command(t, 0, "spec", "update"); !strings.Contains(out, "changed product:"+capCase) {
		t.Fatalf("deprecation fixture did not update the pin: %s", out)
	}
	if got := specDriftJSON(t, p, 0, "--strict"); len(got) != 0 {
		t.Fatalf("already pinned deprecation produced findings: %+v", got)
	}
}

func TestSpecDriftUnmeasuredAndUsage(t *testing.T) {
	p := newSpecProject(t)
	p.markdown(t, "draft", pricingScenarios)
	t.Setenv("FACET_STRICT_DEPS", "")
	unmeasured := func(code int, reason string, flags ...string) {
		t.Helper()
		got := specDriftJSON(t, p, code, flags...)
		if len(got) != 1 || got[0].State != spec.Unmeasured || got[0].Address != "" ||
			!strings.Contains(got[0].Detail, reason) || strings.Contains(got[0].Detail, p.remote) {
			t.Fatalf("unexpected unmeasured report: %+v", got)
		}
	}
	unmeasured(0, "cannot read cached branch", "--cached")
	if _, err := os.Stat(filepath.Join(p.root, ".facet/spec-cache")); !os.IsNotExist(err) {
		t.Fatalf("cached drift created a cache: %v", err)
	}
	t.Setenv("FACET_STRICT_DEPS", "1")
	unmeasured(1, "cannot read cached branch", "--cached")
	specGit(t, p.upstream, "branch", "-m", "unavailable")
	unmeasured(1, "cannot fetch branch")
	t.Setenv("FACET_STRICT_DEPS", "")
	unmeasured(0, "cannot fetch branch")
	specGit(t, p.upstream, "branch", "-m", "main")
	p.markdown(t, "draft", strings.Replace(pricingScenarios, "@case:cap\n", "", 1))
	unmeasured(0, "needs one @case")
	if out := p.command(t, 1, "spec", "drift", "missing"); !strings.Contains(out, `spec source "missing" is not declared`) {
		t.Fatal(out)
	}
	writeSpecFixture(t, p.root, spec.LockName, "product: invalid\n")
	if out := p.command(t, 1, "spec", "drift", "--cached"); !strings.Contains(out, spec.LockName) {
		t.Fatal(out)
	}
}

func TestSpecDriftCacheIsSpecificToTheFollowBranch(t *testing.T) {
	p := newSpecProject(t)
	pinDriftFixture(t, p, pricingScenarios)
	specGit(t, p.upstream, "switch", "-c", "review")
	p.markdown(t, "draft", strings.Replace(pricingScenarios, "50%", "40%", 1))
	p.configure(t, fmt.Sprintf("spec_sources:\n  - id: product\n    repo: %s\n    path: specs\n    follow: review\n", p.remote))
	if got := specDriftJSON(t, p, 0, "--cached"); len(got) != 1 || got[0].State != spec.Unmeasured {
		t.Fatalf("cache used a different branch: %+v", got)
	}
	if got := specDriftJSON(t, p, 0); len(got) != 1 || got[0].State != spec.SpecMoved ||
		got[0].Address != "product:"+capCase {
		t.Fatalf("drift did not fetch and compare the configured branch: %+v", got)
	}
}

func runSpecHook(t *testing.T, p specProject, event, payload string) []byte {
	t.Helper()
	cmd := newRootCmd()
	cmd.SetArgs([]string{"--root", p.root, "hook", event})
	cmd.SetIn(strings.NewReader(payload))
	var out bytes.Buffer
	cmd.SetOut(&out)
	if code := Run(cmd); code != 0 {
		t.Fatalf("hook %s exited %d: %s", event, code, &out)
	}
	return out.Bytes()
}

func TestSpecDriftStopHookReportsCachedMoveOnce(t *testing.T) {
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	p := newSpecProject(t)
	pinDriftFixture(t, p, pricingScenarios)
	p.markdown(t, "draft", strings.Replace(pricingScenarios, "50%", "40%", 1))
	start := fmt.Sprintf(`{"cwd":%q,"session_id":"spec-stop","hook_event_name":"SessionStart","source":"startup"}`, p.root)
	runSpecHook(t, p, "session-start", start)
	if got := specDriftJSON(t, p, 0, "--cached"); len(got) != 1 || got[0].State != spec.SpecMoved {
		t.Fatalf("SessionStart did not fetch the moved spec: %+v", got)
	}

	p.markdown(t, "draft", pricingScenarios)
	payload := fmt.Sprintf(`{"cwd":%q,"session_id":"spec-stop","hook_event_name":"Stop"}`, p.root)
	out := runSpecHook(t, p, "stop", payload)
	var notice struct{ Decision, Reason string }
	if err := json.Unmarshal(out, &notice); err != nil || notice.Decision != "block" ||
		!strings.Contains(notice.Reason, "spec-moved product:"+capCase) ||
		!strings.HasSuffix(notice.Reason, spec.ChangedSpecAdvice) {
		t.Fatalf("unexpected Stop notice: %v, %s", err, out)
	}
	if again := runSpecHook(t, p, "stop", payload); len(again) != 0 {
		t.Fatalf("Stop repeated the notice: %s", again)
	}
	if other := runSpecHook(t, p, "stop", strings.Replace(payload, "spec-stop", "other-session", 1)); !bytes.Equal(other, out) {
		t.Fatalf("new session did not receive the original notice: %s", other)
	}
	p.configure(t, "{}\n")
	if noSources := runSpecHook(t, p, "stop", strings.Replace(payload, "spec-stop", "no-sources", 1)); len(noSources) != 0 {
		t.Fatalf("Stop reported spec drift with no configured sources: %s", noSources)
	}
}

func TestSpecDriftSessionStartHookBoundsUnreachableSource(t *testing.T) {
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	t.Setenv("FACET_SPEC_REFRESH_TIMEOUT", "700ms")
	t.Setenv("NO_PROXY", "127.0.0.1")
	p := newSpecProject(t)
	pinDriftFixture(t, p, pricingScenarios)
	driftRecord(t, p, "discounts", capCase, false)

	requested := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		select {
		case requested <- struct{}{}:
		default:
		}
		select {
		case <-request.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}))
	defer server.Close()
	p.configure(t, fmt.Sprintf("spec_sources:\n  - id: stalled\n    repo: %s\n  - id: product\n    repo: %s\n    path: specs\n", server.URL, p.remote))
	p.markdown(t, "draft", strings.Replace(pricingScenarios, "50%", "40%", 1))
	payload := fmt.Sprintf(`{"cwd":%q,"session_id":"spec-timeout","hook_event_name":"SessionStart","source":"startup"}`, p.root)
	start := time.Now()
	out := runSpecHook(t, p, "session-start", payload)
	elapsed := time.Since(start)
	select {
	case <-requested:
	default:
		t.Fatal("SessionStart did not attempt the unreachable source")
	}
	var response hookOutput
	if err := json.Unmarshal(out, &response); err != nil || response.HookSpecificOutput.HookEventName != "SessionStart" ||
		!strings.Contains(response.HookSpecificOutput.AdditionalContext, "checkout/pricing/discounts") ||
		bytes.Contains(out, []byte("stalled")) {
		t.Fatalf("SessionStart did not return normal context: %v, %s", err, out)
	}
	if elapsed < 600*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("SessionStart took %s with a 700ms refresh budget", elapsed)
	}
	if got := specDriftJSON(t, p, 0, "--cached", "product"); len(got) != 0 {
		t.Fatalf("later source was fetched after the total refresh budget expired: %+v", got)
	}
}

func TestSpecDriftAnApprovedRemovalIsOnlyOrphaned(t *testing.T) {
	p := newSpecProject(t)
	pinDriftFixture(t, p, pricingScenarios)
	driftRecord(t, p, "case-owner", capCase, false)
	remaining := strings.Split(pricingScenarios, "@case:unknown")[0]
	if !strings.Contains(remaining, "@case:cap") || strings.Contains(remaining, "unknown") {
		t.Fatalf("fixture did not keep only the claimed case: %q", remaining)
	}
	p.markdown(t, "approved", remaining)
	got := specDriftJSON(t, p, 0)
	if len(got) != 1 || got[0].State != spec.Orphaned || got[0].Address != "product:"+unknownCase {
		t.Fatalf("an absent scenario must be orphaned and nothing else: %+v", got)
	}
}
