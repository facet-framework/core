// Fixtures below contain example @facet: tags; this file demonstrates the
// syntax rather than implementing a feature.
// @facet-ignore-file

package facet

import (
	"path/filepath"
	"strings"
	"testing"
)

// multiVariantProject models a web app, a native app, shared business
// logic and React Native platform extensions in a single directory.
func multiVariantProject(t *testing.T) Registry {
	t.Helper()
	root := t.TempDir()

	mustWrite(t, filepath.Join(root, "docs/facets/variants.yaml"), `schema_version: "0.1"
variants:
  web:
    paths: ["apps/web/**", "**/*.web.tsx"]
  ios:
    paths: ["apps/ios/**", "**/*.ios.tsx"]
shared:
  paths: ["packages/core/**", "services/api/**"]
`)
	mustWrite(t, filepath.Join(root, "docs/facets/features/payments/checkout/session.yaml"), `schema_version: "0.1"
id: payments/checkout/session
variants:
  web:
    lifecycle: active
  ios:
    lifecycle: planned
intent:
  summary: |
    Creates a checkout session so a customer can pay for a plan.
contract:
  scenarios:
    - id: created
      statement: |
        Given a plan
        When the customer subscribes
        Then a session is created
lineage:
  owner:
    team: payments
  created_at: "2026-01-01"
`)
	return Open(root)
}

func variantOf(t *testing.T, reg Registry, rel string) (string, bool) {
	t.Helper()
	sm, err := LoadVariantMap(reg.VariantMapPath())
	if err != nil {
		t.Fatal(err)
	}
	return sm.VariantOf(rel)
}

func TestSurfaceResolution(t *testing.T) {
	reg := multiVariantProject(t)
	cases := []struct {
		path    string
		variant string
		mapped  bool
	}{
		{"apps/web/checkout.ts", "web", true},
		{"apps/ios/Checkout.swift", "ios", true},
		{"packages/core/checkout.ts", SharedVariant, true},
		{"services/api/checkout.go", SharedVariant, true},
		// React Native platform extensions share a directory; only a file
		// pattern can tell them apart.
		{"packages/ui/Button.web.tsx", "web", true},
		{"packages/ui/Button.ios.tsx", "ios", true},
		// Nothing claims this, which is the map rotting.
		{"apps/desktop/main.ts", "", false},
	}
	for _, c := range cases {
		got, mapped := variantOf(t, reg, c.path)
		if got != c.variant || mapped != c.mapped {
			t.Errorf("%s: got (%q,%v), want (%q,%v)", c.path, got, mapped, c.variant, c.mapped)
		}
	}
}

// The longest pattern wins, so the outcome cannot depend on the order entries
// happen to be written in.
func TestLongestPatternWins(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "docs/facets/variants.yaml"), `schema_version: "0.1"
variants:
  web:
    paths: ["packages/ui-web/**"]
shared:
  paths: ["packages/**"]
`)
	mustWrite(t, filepath.Join(root, "docs/facets/features/a/b/c.yaml"), "id: a/b/c\n")
	reg := Open(root)

	if got, _ := variantOf(t, reg, "packages/ui-web/Button.tsx"); got != "web" {
		t.Fatalf("the more specific pattern must win, got %q", got)
	}
	if got, _ := variantOf(t, reg, "packages/core/thing.ts"); got != SharedVariant {
		t.Fatalf("the broader pattern still applies elsewhere, got %q", got)
	}
}

// A path claimed by two variants at the same specificity belongs exclusively to
// neither, so it is shared.
func TestPathClaimedByTwoSurfacesIsShared(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "docs/facets/variants.yaml"), `schema_version: "0.1"
variants:
  ios:
    paths: ["packages/native-core/**"]
  android:
    paths: ["packages/native-core/**"]
`)
	mustWrite(t, filepath.Join(root, "docs/facets/features/a/b/c.yaml"), "id: a/b/c\n")
	reg := Open(root)

	if got, _ := variantOf(t, reg, "packages/native-core/session.ts"); got != SharedVariant {
		t.Fatalf("code shared by two clients is exclusive to neither, got %q", got)
	}
}

func variantLint(t *testing.T, reg Registry) Report {
	t.Helper()
	rep, err := Lint(reg, LintOptions{Skip: map[string]bool{"schema": true}, Today: "2026-07-27"})
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func findingsFor(r Report, check string) []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.Check == check {
			out = append(out, f)
		}
	}
	return out
}

// Shared code alone does not prove that a client can reach the feature;
// surface presence requires an anchor in that client.
func TestSharedCodeAloneDoesNotProveSurfacePresence(t *testing.T) {
	reg := multiVariantProject(t)
	mustWrite(t, filepath.Join(reg.Root, "packages/core/checkout.ts"), `// @facet: payments/checkout/session
// @facet-role: primary
export function create() {}
`)
	found := findingsFor(variantLint(t, reg), "orphan-records")
	if len(found) != 1 || found[0].Severity != SeverityWarning {
		t.Fatalf("expected one warning that web is active on shared code only, got %+v", found)
	}
}

func TestExclusiveCodeProvesSurfacePresence(t *testing.T) {
	reg := multiVariantProject(t)
	mustWrite(t, filepath.Join(reg.Root, "apps/web/checkout.ts"), `// @facet: payments/checkout/session
// @facet-role: primary
export function create() {}
`)
	if found := findingsFor(variantLint(t, reg), "orphan-records"); len(found) != 0 {
		t.Fatalf("an anchor exclusive to web satisfies web, got %+v", found)
	}
}

func TestActiveSurfaceWithNoAnchorIsAnError(t *testing.T) {
	reg := multiVariantProject(t)
	mustWrite(t, filepath.Join(reg.Root, "apps/ios/Checkout.swift"), `// @facet: payments/checkout/session
// @facet-role: primary
func create() {}
`)
	// ios is 'planned' and now anchored; web is 'active' and is not.
	found := findingsFor(variantLint(t, reg), "orphan-records")
	if len(found) != 1 || found[0].Severity != SeverityError {
		t.Fatalf("expected one error for web, got %+v", found)
	}
}

func TestAnchorOnUndeclaredSurfaceIsReported(t *testing.T) {
	reg := multiVariantProject(t)
	mustWrite(t, filepath.Join(reg.Root, "docs/facets/variants.yaml"), `schema_version: "0.1"
variants:
  web:
    paths: ["apps/web/**"]
  ios:
    paths: ["apps/ios/**"]
  android:
    paths: ["apps/android/**"]
`)
	mustWrite(t, filepath.Join(reg.Root, "apps/web/checkout.ts"), `// @facet: payments/checkout/session
// @facet-role: primary
export function create() {}
`)
	mustWrite(t, filepath.Join(reg.Root, "apps/android/Checkout.kt"), `// @facet: payments/checkout/session
// @facet-role: primary
fun create() {}
`)
	var found bool
	for _, f := range findingsFor(variantLint(t, reg), "variants") {
		if f.Severity == SeverityError && f.Path == "apps/android/Checkout.kt" {
			found = true
		}
	}
	if !found {
		t.Fatal("implementing a feature on a client the record never mentions must be reported")
	}
}

// An unmapped anchored file means the variant map stopped covering the repo.
func TestUnmappedAnchoredFileIsReported(t *testing.T) {
	reg := multiVariantProject(t)
	mustWrite(t, filepath.Join(reg.Root, "apps/web/checkout.ts"), `// @facet: payments/checkout/session
// @facet-role: primary
export function create() {}
`)
	mustWrite(t, filepath.Join(reg.Root, "apps/desktop/checkout.ts"), `// @facet: payments/checkout/session
// @facet-role: related
export function create() {}
`)
	var found bool
	for _, f := range findingsFor(variantLint(t, reg), "variants") {
		if f.Path == "apps/desktop/checkout.ts" && f.Severity == SeverityError {
			found = true
		}
	}
	if !found {
		t.Fatal("a file matching no pattern must be reported, or the map rots silently")
	}
}

// Without a variant map, the registry uses the record-level lifecycle
// and does not require variant-specific presence.
func TestProjectWithoutVariantMapIsUnaffected(t *testing.T) {
	reg := newProject(t)
	rep := variantLint(t, reg)
	if len(findingsFor(rep, "variants")) != 0 {
		t.Fatal("a single-variant project must see no variant findings")
	}
	if errs := errorsOnly(rep); len(errs) != 0 {
		t.Fatalf("expected no errors, got %+v", errs)
	}
}

// Flags: the valuable check is that a declared key still exists in the code.

func flagProject(t *testing.T, retireBy string) Registry {
	t.Helper()
	reg := newProject(t)
	mustWrite(t, reg.PathForID("payments/checkout/session"), `schema_version: "0.1"
id: payments/checkout/session
lifecycle: active
intent:
  summary: |
    Creates a checkout session so a customer can pay for a plan.
contract:
  scenarios: []
flags:
  - key: checkout.session_v2
    system: launchdarkly
    default: false
    retire_by: "`+retireBy+`"
lineage:
  owner:
    team: payments
  created_at: "2026-01-01"
`)
	return reg
}

func TestDeclaredFlagMissingFromSourceIsAnError(t *testing.T) {
	reg := flagProject(t, "2099-01-01")
	found := findingsFor(variantLint(t, reg), "flags")
	if len(found) != 1 || found[0].Severity != SeverityError {
		t.Fatalf("a flag key that appears nowhere must be an error, got %+v", found)
	}
}

func TestDeclaredFlagPresentInSourceIsClean(t *testing.T) {
	reg := flagProject(t, "2099-01-01")
	mustWrite(t, filepath.Join(reg.Root, "src/checkout.ts"), `// @facet: payments/checkout/session
// @facet-role: primary
export function create() {
  if (flags.isEnabled("checkout.session_v2")) { return null }
}
`)
	if found := findingsFor(variantLint(t, reg), "flags"); len(found) != 0 {
		t.Fatalf("a flag present in source is clean, got %+v", found)
	}
}

func TestExpiredFlagIsAWarningNotAnError(t *testing.T) {
	reg := flagProject(t, "2020-01-01")
	mustWrite(t, filepath.Join(reg.Root, "src/checkout.ts"), `// @facet: payments/checkout/session
// @facet-role: primary
export function create() { return flags.get("checkout.session_v2") }
`)
	rep := variantLint(t, reg)
	found := findingsFor(rep, "flags")
	if len(found) != 1 || found[0].Severity != SeverityWarning {
		t.Fatalf("an overdue flag is debt, not a broken build, got %+v", found)
	}
	if rep.Failed() {
		t.Fatal("an overdue flag must not fail the run")
	}
}

// With per-variant Knowledge overrides, the gap warning must not multiply by
// the number of variants.
func TestKnowledgeGapWarningIsNotMultipliedBySurfaces(t *testing.T) {
	reg := multiVariantProject(t)
	mustWrite(t, filepath.Join(reg.Root, "apps/web/checkout.ts"), `// @facet: payments/checkout/session
// @facet-role: primary
export function create() {}
`)
	mustWrite(t, reg.PathForID("payments/checkout/session"), `schema_version: "0.1"
id: payments/checkout/session
variants:
  web:
    lifecycle: active
  ios:
    lifecycle: active
intent:
  summary: |
    Creates a checkout session so a customer can pay for a plan.
contract:
  scenarios: []
knowledge:
  prerequisite_knowledge:
    question: "What must the user already understand?"
  not_for:
    question: "When is this the wrong tool?"
  common_pitfalls:
    question: "What goes wrong?"
  success_signal:
    question: "How does the user know it worked?"
lineage:
  owner:
    team: payments
  created_at: "2026-01-01"
`)
	found := findingsFor(variantLint(t, reg), "knowledge")
	if len(found) != 1 {
		t.Fatalf("expected exactly one knowledge finding for the record, got %d — the warning is multiplying by variants", len(found))
	}
}

func TestVariantMapIsOptionalAndMissingFileIsNotAnError(t *testing.T) {
	m, err := LoadVariantMap(filepath.Join(t.TempDir(), "variants.yaml"))
	if err != nil {
		t.Fatalf("a project without variants must not error: %v", err)
	}
	if m.Configured() {
		t.Fatal("an absent map means no variants")
	}
}

// A slot answered only per variant is answered, not a gap. Without this the
// per-variant override would report itself as missing.
func TestPerSurfaceAnswerIsNotAGap(t *testing.T) {
	reg := multiVariantProject(t)
	mustWrite(t, filepath.Join(reg.Root, "apps/web/checkout.ts"), `// @facet: payments/checkout/session
// @facet-role: primary
export function create() {}
`)
	mustWrite(t, reg.PathForID("payments/checkout/session"), `schema_version: "0.1"
id: payments/checkout/session
variants:
  web:
    lifecycle: active
intent:
  summary: |
    Creates a checkout session so a customer can pay for a plan.
contract:
  scenarios: []
knowledge:
  discoverability:
    question: "Where does the user enter this flow?"
    by_variant:
      web: { answer: "Settings then Billing" }
lineage:
  owner:
    team: payments
  created_at: "2026-01-01"
`)
	if found := findingsFor(variantLint(t, reg), "knowledge"); len(found) != 0 {
		t.Fatalf("a per-variant answer answers the slot, got %+v", found)
	}
}

// A rewrite duplicates everything, not just clients: backends and pipelines
// too. Restricting variants to clients would classify legacy/api and v2/api as
// shared code and make migration progress inexpressible.
func TestRewriteSurfacesCoverBackendsNotOnlyClients(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "docs/facets/variants.yaml"), `schema_version: "0.1"
variants:
  legacy-api:
    paths: ["legacy/api/**"]
  v2-api:
    paths: ["v2/api/**"]
    replaces: legacy-api
  legacy-web:
    paths: ["legacy/web/**"]
  v2-web:
    paths: ["v2/web/**"]
    replaces: legacy-web
shared:
  paths: ["packages/**"]
`)
	mustWrite(t, filepath.Join(root, "docs/facets/features/payments/checkout/session.yaml"), `schema_version: "0.1"
id: payments/checkout/session
variants:
  legacy-api:
    lifecycle: active
  v2-api:
    lifecycle: active
  legacy-web:
    lifecycle: active
  v2-web:
    lifecycle: planned
intent:
  summary: |
    Creates a checkout session so a customer can pay for a plan.
contract:
  scenarios: []
lineage:
  owner:
    team: payments
  created_at: "2026-01-01"
`)
	for _, p := range []string{"legacy/api/session.go", "v2/api/session.go", "legacy/web/checkout.ts"} {
		mustWrite(t, filepath.Join(root, p), "// @facet: payments/checkout/session\n// @facet-role: primary\n")
	}
	reg := Open(root)

	// The backend migration is half done and that is expressible, not a finding.
	if errs := errorsOnly(variantLint(t, reg)); len(errs) != 0 {
		t.Fatalf("a half-migrated rewrite is a valid state, got %+v", errs)
	}

	sm, err := LoadVariantMap(reg.VariantMapPath())
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := sm.VariantOf("v2/api/session.go"); got != "v2-api" {
		t.Fatalf("a backend must be able to be a variant, got %q", got)
	}
	if sm.Replaces("v2-api") != "legacy-api" {
		t.Fatal("the replacement relationship must be readable for migration reporting")
	}
}

// A 'replaces' naming nothing would silently drop a migration out of every
// report built on it.
func TestReplacesMustNameADeclaredSurface(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "docs/facets/variants.yaml"), `schema_version: "0.1"
variants:
  v2-api:
    paths: ["v2/api/**"]
    replaces: legcy-api
`)
	mustWrite(t, filepath.Join(root, "docs/facets/features/a/b/c.yaml"), `schema_version: "0.1"
id: a/b/c
variants:
  v2-api:
    lifecycle: planned
intent:
  summary: |
    A feature used only to exercise the variant map validation path here.
contract:
  scenarios: []
lineage:
  owner:
    team: platform
  created_at: "2026-01-01"
`)
	var found bool
	for _, f := range findingsFor(variantLint(t, Open(root)), "variants") {
		if f.Severity == SeverityError && strings.Contains(f.Message, "replaces") {
			found = true
		}
	}
	if !found {
		t.Fatal("a typo in 'replaces' must be reported")
	}
}

// A non-empty derive_from declares how a Knowledge slot is handled, even
// when answer is empty, so the slot is not a pending owner gap.
func TestASlotNamingItsDerivationIsNotAGap(t *testing.T) {
	reg := multiVariantProject(t)
	mustWrite(t, filepath.Join(reg.Root, "apps/web/checkout.ts"), `// @facet: payments/checkout/session
// @facet-role: primary
export function create() {}
`)
	mustWrite(t, reg.PathForID("payments/checkout/session"), `schema_version: "0.1"
id: payments/checkout/session
variants:
  web:
    lifecycle: active
intent:
  summary: |
    Creates a checkout session so a customer can pay for a plan.
contract:
  scenarios: []
knowledge:
  discoverability:
    question: "Where does the user enter this flow?"
    derive_from: [anchors]
lineage:
  owner:
    team: payments
  created_at: "2026-01-01"
`)
	if found := findingsFor(variantLint(t, reg), "knowledge"); len(found) != 0 {
		t.Fatalf("derive_from declares the slot handled, got %+v", found)
	}
}

// An empty derive_from is not a declaration. The schema gives the field a
// default of [], so treating the field's presence as the signal would silence
// every slot that carries the default it was written with.
func TestAnEmptyDeriveFromIsStillAGap(t *testing.T) {
	reg := multiVariantProject(t)
	mustWrite(t, filepath.Join(reg.Root, "apps/web/checkout.ts"), `// @facet: payments/checkout/session
// @facet-role: primary
export function create() {}
`)
	mustWrite(t, reg.PathForID("payments/checkout/session"), `schema_version: "0.1"
id: payments/checkout/session
variants:
  web:
    lifecycle: active
intent:
  summary: |
    Creates a checkout session so a customer can pay for a plan.
contract:
  scenarios: []
knowledge:
  discoverability:
    question: "Where does the user enter this flow?"
    derive_from: []
lineage:
  owner:
    team: payments
  created_at: "2026-01-01"
`)
	found := findingsFor(variantLint(t, reg), "knowledge")
	if len(found) != 1 {
		t.Fatalf("an empty derive_from declares nothing, got %+v", found)
	}
	if !strings.Contains(found[0].Message, "discoverability") {
		t.Fatalf("want discoverability named as the gap, got %q", found[0].Message)
	}
}
