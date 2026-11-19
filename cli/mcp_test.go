// Fixtures below contain example @facet: tags; this file demonstrates the
// syntax rather than implementing a feature.
// @facet-ignore-file

package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/facet-framework/core/registry"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// monorepo builds two registries that both document auth/login/password, which
// under global ids is one feature described independently by each product.
func monorepo(t *testing.T) facet.RegistrySet {
	t.Helper()
	root := t.TempDir()
	write(t, filepath.Join(root, ".facetrc.yaml"), `
registries:
  storefront:
    records: apps/storefront/docs/facets
    sources: [apps/storefront, packages/shared]
  admin:
    records: apps/admin/docs/facets
    sources: [apps/admin, packages/shared]
`)
	write(t, filepath.Join(root, "apps/storefront/docs/facets/features/auth/login/password.yaml"),
		"schema_version: \"0.1\"\nid: auth/login/password\nlifecycle: active\nintent:\n  summary: Sign in with an email and a password.\n")
	write(t, filepath.Join(root, "apps/admin/docs/facets/features/auth/login/password.yaml"),
		"schema_version: \"0.1\"\nid: auth/login/password\nlifecycle: planned\nintent:\n  summary: Staff sign-in, SSO pending.\n")
	write(t, filepath.Join(root, "apps/storefront/login.go"),
		"// @facet: auth/login/password\npackage storefront\n")
	write(t, filepath.Join(root, "apps/admin/login.go"),
		"// @facet: auth/login/password\npackage admin\n")
	return facet.OpenSet(root)
}

// The point of global ids: one call, both products' answers, side by side.
func TestFeatureGetReturnsEveryRegistrysRecord(t *testing.T) {
	set := monorepo(t)

	out, err := featureGet(set, getInput{ID: "auth/login/password"})
	if err != nil {
		t.Fatal(err)
	}
	if out.ID != "auth/login/password" {
		t.Errorf("id = %q", out.ID)
	}
	if len(out.Records) != 2 {
		t.Fatalf("want both registries' records, got %d", len(out.Records))
	}
	if out.Records[0].Registry != "admin" || out.Records[1].Registry != "storefront" {
		t.Fatalf("records must be in alphabetical order, got %q then %q",
			out.Records[0].Registry, out.Records[1].Registry)
	}
	// The comparison this exists for: same feature, different state.
	if out.Records[0].Lifecycle != "planned" || out.Records[1].Lifecycle != "active" {
		t.Errorf("lifecycles = %q, %q — want planned, active",
			out.Records[0].Lifecycle, out.Records[1].Lifecycle)
	}
}

func TestFeatureGetNarrowsToAQualifiedRegistry(t *testing.T) {
	set := monorepo(t)

	out, err := featureGet(set, getInput{ID: "admin:auth/login/password"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Records) != 1 || out.Records[0].Registry != "admin" {
		t.Fatalf("want only admin's record, got %+v", out.Records)
	}
}

// feature_get returns a list even for a project with exactly one registry,
// keeping the response shape independent of registry count.
func TestFeatureGetReturnsAListInASingleRegistryProject(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "docs/facets/features/auth/login/password.yaml"),
		"schema_version: \"0.1\"\nid: auth/login/password\nlifecycle: active\n")
	set := facet.OpenSet(root)

	out, err := featureGet(set, getInput{ID: "auth/login/password"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Records) != 1 {
		t.Fatalf("want one record, got %d", len(out.Records))
	}
	if out.Records[0].Registry != "" {
		t.Errorf("a project declaring no registries must not be given one, got %q", out.Records[0].Registry)
	}
}

func TestFeatureGetRejectsAnUnknownId(t *testing.T) {
	set := monorepo(t)
	if _, err := featureGet(set, getInput{ID: "auth/login/webauthn"}); err == nil {
		t.Fatal("want an error for an id no registry has")
	}
}

// Where a feature lives in each product is the other half of the comparison.
func TestAnchorListLabelsEachAnchorWithItsRegistry(t *testing.T) {
	set := monorepo(t)

	out, err := anchorList(set, anchorListInput{ID: "auth/login/password"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Anchors) != 2 {
		t.Fatalf("want an anchor from each registry, got %+v", out.Anchors)
	}
	byRegistry := map[string]string{}
	for _, a := range out.Anchors {
		byRegistry[a.Registry] = a.Path
	}
	if byRegistry["storefront"] != "apps/storefront/login.go" {
		t.Errorf("storefront anchor = %q", byRegistry["storefront"])
	}
	if byRegistry["admin"] != "apps/admin/login.go" {
		t.Errorf("admin anchor = %q", byRegistry["admin"])
	}
}

// Direct resolution and tree listing must agree on dot-directory anchors.
func TestAnchorResolveAndAnchorListAgreeOnADotDirectoryAnchor(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "docs/facets/features/platform/cicd/github-actions-pipelines.yaml"),
		"schema_version: \"0.1\"\nid: platform/cicd/github-actions-pipelines\nlifecycle: active\nintent:\n  summary: Our CI pipeline.\n")
	write(t, filepath.Join(root, ".github/workflows/facet-lint.yml"),
		"# @facet: platform/cicd/github-actions-pipelines\n# @facet-role: primary\nname: facet-lint\n")
	set := facet.OpenSet(root)

	resolved, err := anchorResolve(set, resolveInput{Path: ".github/workflows/facet-lint.yml"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.Features) != 1 || resolved.Features[0] != "platform/cicd/github-actions-pipelines" {
		t.Fatalf("anchor_resolve = %+v, want [platform/cicd/github-actions-pipelines]", resolved.Features)
	}

	listed, err := anchorList(set, anchorListInput{ID: "platform/cicd/github-actions-pipelines"})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Anchors) != 1 || listed.Anchors[0].Path != ".github/workflows/facet-lint.yml" {
		t.Fatalf("anchor_list = %+v, must agree with anchor_resolve and report the .github/workflows/ anchor", listed.Anchors)
	}
}

// A registry prefix scopes a search to one product.
func TestFeatureFindScopesToARegistry(t *testing.T) {
	set := monorepo(t)

	out, err := featureFind(set, findInput{Query: "admin:"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Total != 1 {
		t.Fatalf("want only admin's records, got %d: %+v", out.Total, out.Matches)
	}
	if out.Matches[0].ID != "admin:auth/login/password" {
		t.Errorf("a match in a monorepo must name its registry, got %q", out.Matches[0].ID)
	}
}

// An empty or null YAML file still owns its id by path, but parses to a nil
// map. Return {}: the inferred MCP object schema does not permit null.
func TestFeatureGetRecordMarshalsToAnEmptyObjectNotNull(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "docs/facets/features/auth/login/password.yaml"), "")
	set := facet.OpenSet(root)

	out, err := featureGet(set, getInput{ID: "auth/login/password"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Records) != 1 {
		t.Fatalf("want one record, got %d", len(out.Records))
	}
	if out.Records[0].Record == nil {
		t.Fatal("Record is nil - the MCP go-sdk infers a bare map as type:\"object\" with no null in the union, so this fails feature_get's own output-schema validation")
	}

	out2, err := json.Marshal(out.Records[0].Record)
	if err != nil {
		t.Fatal(err)
	}
	if string(out2) != "{}" {
		t.Fatalf("want the record to marshal to {}, got %s", out2)
	}
}
