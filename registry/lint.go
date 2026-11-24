// @facet: registry/integrity/record-validation
// @facet-role: related

package facet

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Severity of a lint finding.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Finding is one lint result.
type Finding struct {
	Severity Severity `json:"severity"`
	Check    string   `json:"check"`
	// Registry names which of a monorepo's registries produced the finding.
	// Empty in a project that declares none, where the name would be invented.
	Registry string `json:"registry,omitempty"`
	Path     string `json:"path,omitempty"`
	ID       string `json:"id,omitempty"`
	Message  string `json:"message"`
	Hint     string `json:"hint,omitempty"`
}

// Report collects findings across all checks.
type Report struct {
	Findings []Finding `json:"findings"`
	Checked  struct {
		Records    int      `json:"records"`
		Anchors    int      `json:"anchors"`
		Registries []string `json:"registries,omitempty"`
	} `json:"checked"`
}

// Failed reports whether anything blocking was found. Warnings never fail a
// run — an empty Knowledge slot is work for an owner, not a broken build.
func (r Report) Failed() bool {
	for _, f := range r.Findings {
		if f.Severity == SeverityError {
			return true
		}
	}
	return false
}

func (r *Report) add(f Finding) { r.Findings = append(r.Findings, f) }

// LintOptions selects which checks run.
type LintOptions struct {
	specPin *specPin
	Scan    ScanOptions
	Schema  bool // validate records against JSON Schema
	Skip    map[string]bool
	// Today is an ISO date used for flag retirement checks. Injected rather
	// than read from the clock so a run is reproducible (spec §6.3).
	Today string
	// OwnedElsewhere holds ids some other registry of the same project has a
	// record for. It excuses an anchor only in shared code — see Shared.
	OwnedElsewhere map[string]bool
	// Shared reports whether a repository-relative path is claimed by more than
	// one registry. Nil means nothing is shared, which is true of every project
	// declaring one registry.
	Shared func(rel string) bool
	// KnownRegistries contains the declared registry names. A qualified anchor
	// prefix outside this set is reported as an unknown registry.
	KnownRegistries map[string]bool
	// KnownIDs holds every record id the project has, across all categories and
	// all registries. A lineage reference is judged against the whole project,
	// not against one registry: a monorepo's products legitimately depend on
	// each other, and `depends_on` carries no registry prefix to say so.
	KnownIDs map[string]bool
	// IsTestPath classifies a repository-relative path as test code for the
	// test-only-anchors check. Nil uses IsTestPath's default heuristic; a
	// project whose tests, parity harnesses, or benchmarks live somewhere that
	// default does not recognise supplies its own.
	IsTestPath func(rel string) bool
	// ErrorCodePattern is the regular expression the error-codes check uses to
	// find stable identifiers in intent.summary, contract.scenarios[].statement,
	// and knowledge.common_pitfalls. Empty means DefaultErrorCodePattern —
	// the E\d{4} band — which is what a caller that never read
	// .facetrc.yaml's error_codes key should get.
	ErrorCodePattern string
	// Changes is the change set the lost-anchors check reads, loaded once by
	// LintAll for every registry. Nil means Lint loads one itself against the
	// worktree's default baseline.
	Changes *ChangeSet
	// PluginsDir is the directory of installed plugins whose facet.lint
	// sidecar tool runs as part of this lint (RunPluginLint). Empty means
	// none run, which is what every caller that does not set it gets.
	PluginsDir string
	// Registry narrows LintAll to the findings of one declared registry. The
	// others are still read: shared code and lineage are judged against the
	// whole project, and linting one registry alone would report every shared
	// anchor and cross-registry dependency of another product as dangling.
	// Empty lints every registry.
	Registry string
}

// Lint runs the registry's integrity checks.
func Lint(reg Registry, opts LintOptions) (Report, error) {
	reg.categorySnapshot = make(map[string]categoryLoad)
	return lint(reg, opts)
}

// ValidateRecord checks the facts a write hook can establish from one saved
// record or registry document. Registry-wide contracts and source walks stay
// with Lint.
func ValidateRecord(reg Registry, path string) (Report, error) {
	var rep Report
	path = filepath.Clean(path)
	// Validate registry-wide documents before dispatching record categories.
	documents := []struct{ path, schema string }{
		{reg.ManifestPath(), "generation-manifest.schema.json"},
		{reg.TaxonomyPath(), "taxonomy.schema.json"},
		{reg.GlossaryPath(), "glossary.schema.json"},
		{reg.VariantMapPath(), "variants.schema.json"},
	}
	for _, document := range registeredProductDocuments() {
		documents = append(documents, struct{ path, schema string }{document.Path(reg), document.Schema})
	}
	for _, document := range documents {
		if path != filepath.Clean(document.path) {
			continue
		}
		schemaPath := filepath.Join(reg.SchemaDir, document.schema)
		if isFile(schemaPath) {
			schema, err := compileSchema(schemaPath)
			if err != nil {
				rep.add(Finding{Severity: SeverityError, Check: "schema", Path: reg.Rel(schemaPath), Message: fmt.Sprintf("schema could not be compiled: %v", err)})
			} else {
				validateRecord(reg, &rep, schema, Record{Path: path, Fields: loadYAMLFields(path)})
			}
		}
		// Root declarations have a few local contracts beyond their schemas.
		// Keep these inputs limited to the saved document: neither needs sibling
		// records or an anchor walk, so write-time validation stays constant-size.
		switch path {
		case filepath.Clean(reg.TaxonomyPath()):
			if _, err := LoadTaxonomy(path); err != nil {
				rep.add(Finding{Severity: SeverityError, Check: "taxonomy", Path: reg.Rel(path), Message: fmt.Sprintf("taxonomy could not be read: %v", err)})
			}
		case filepath.Clean(reg.GlossaryPath()):
			glossary, err := LoadGlossary(path)
			if err != nil {
				rep.add(Finding{Severity: SeverityError, Check: "glossary", Path: reg.Rel(path), Message: fmt.Sprintf("glossary could not be read: %v", err)})
			} else if glossary.Configured() {
				lintDuplicateGlossaryTerms(reg, &rep, glossary)
				lintDuplicateGlossaryCategories(reg, &rep, glossary)
				lintUndeclaredGlossaryCategories(reg, &rep, glossary)
			}
		case filepath.Clean(reg.VariantMapPath()):
			variants, err := LoadVariantMap(path)
			if err != nil {
				rep.add(Finding{Severity: SeverityError, Check: "variants", Path: reg.Rel(path), Message: fmt.Sprintf("variant map could not be read: %v", err)})
			} else if variants.Configured() {
				lintVariants(reg, &rep, nil, nil, variants)
			}
		}
		return finishValidateRecord(reg, rep), nil
	}
	rel, err := filepath.Rel(reg.RecordsDir, path)
	if err != nil {
		return rep, err
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 2 || SchemaFor(parts[0]) == "" {
		return rep, fmt.Errorf("path is not a FACET record: %s", path)
	}
	rec, err := LoadRecord(path, parts[0])
	if err != nil {
		return rep, err
	}
	if schemaPath := filepath.Join(reg.SchemaDir, SchemaFor(parts[0])); isFile(schemaPath) {
		schema, err := compileSchema(schemaPath)
		if err != nil {
			rep.add(Finding{Severity: SeverityError, Check: "schema", Path: reg.Rel(schemaPath), Message: fmt.Sprintf("schema could not be compiled: %v", err)})
		} else {
			validateRecord(reg, &rep, schema, rec)
		}
	}
	if parts[0] == "features" {
		one := []Record{rec}
		lintSizing(reg, &rep, one)
		lintKnowledge(reg, &rep, one)
		lintExamples(reg, &rep, one)
		lintProves(reg, &rep, one)
		lintVerificationTargets(reg, &rep, one)

		// These checks need only the saved record plus the small registry
		// declarations; loading siblings or scanning source would make saving a
		// record scale with the whole project.
		variantMap, variantErr := LoadVariantMap(reg.VariantMapPath())
		if variantErr != nil {
			rep.add(Finding{Severity: SeverityError, Check: "variants", Path: reg.Rel(reg.VariantMapPath()), Message: fmt.Sprintf("variant map could not be read: %v", variantErr)})
			variantMap = &VariantMap{}
		}
		if variantMap.Configured() {
			lintVariants(reg, &rep, one, nil, variantMap)
		}
		lintBacking(reg, &rep, one, variantMap)

		taxonomy, taxonomyErr := LoadTaxonomy(reg.TaxonomyPath())
		if taxonomyErr != nil {
			rep.add(Finding{Severity: SeverityError, Check: "taxonomy", Path: reg.Rel(reg.TaxonomyPath()), Message: fmt.Sprintf("taxonomy could not be read: %v", taxonomyErr), Hint: "fix the YAML before saving records that use it"})
		} else if taxonomy.Configured() {
			lintTaxonomy(reg, &rep, one, taxonomy)
		}
	}
	return finishValidateRecord(reg, rep), nil
}

func finishValidateRecord(reg Registry, rep Report) Report {
	for i := range rep.Findings {
		rep.Findings[i].Registry = reg.Name
	}
	if rep.Findings == nil {
		rep.Findings = []Finding{}
	}
	return rep
}

func lint(reg Registry, opts LintOptions) (Report, error) {
	var rep Report
	// The invocation's frozen selection wins over a registry loaded earlier.
	if opts.Scan.View != nil {
		reg.SourceView = opts.Scan.View
	}
	if err := reg.ValidateSourceView(); err != nil {
		return rep, err
	}

	features, err := reg.LoadFeatures()
	if err != nil {
		return rep, err
	}

	scanOpts := opts.Scan
	if reg.SourceView != nil {
		scanOpts = reg.ScanOptions()
	}
	opts.Scan = scanOpts
	scanned, walked, err := ScanWithFiles(reg.Root, scanOpts)
	if err != nil {
		return rep, err
	}

	// A tag naming another registry is that registry's business, not this one's.
	// It still has to name a registry that exists — see lintRegistryRefs.
	anchors := make([]Anchor, 0, len(scanned))
	for _, a := range scanned {
		if a.BelongsTo(reg.Name) {
			anchors = append(anchors, a)
		}
	}
	byID := AnchorsByID(anchors)
	rep.Checked.Anchors = len(anchors)
	if reg.Name != "" {
		rep.Checked.Registries = []string{reg.Name}
	}

	if !opts.Skip["registry-refs"] {
		lintRegistryRefs(reg, &rep, scanned, opts.KnownRegistries)
	}

	variantMap, err := LoadVariantMap(reg.VariantMapPath())
	if err != nil {
		return rep, err
	}

	taxonomy := &Taxonomy{}
	if !opts.Skip["taxonomy"] {
		loaded, taxErr := LoadTaxonomy(reg.TaxonomyPath())
		switch {
		case taxErr == nil:
			taxonomy = loaded
		default:
			rep.add(Finding{
				Severity: SeverityError,
				Check:    "taxonomy",
				Path:     reg.Rel(reg.TaxonomyPath()),
				Message:  fmt.Sprintf("taxonomy could not be read: %v", taxErr),
				Hint:     "fix the YAML, or pass --skip taxonomy to run the other checks meanwhile",
			})
		}
	}

	glossary := &Glossary{}
	if !opts.Skip["glossary"] {
		loaded, glosErr := LoadGlossary(reg.GlossaryPath())
		switch {
		case glosErr == nil:
			glossary = loaded
		default:
			rep.add(Finding{
				Severity: SeverityError,
				Check:    "glossary",
				Path:     reg.Rel(reg.GlossaryPath()),
				Message:  fmt.Sprintf("glossary could not be read: %v", glosErr),
				Hint:     "fix the YAML, or pass --skip glossary to run the other checks meanwhile",
			})
		}
	}

	if !opts.Skip["schema"] {
		if err := lintSchema(reg, &rep, opts); err != nil {
			return rep, err
		}
	}
	if !opts.Skip["orphan-ids"] {
		lintOrphanIDs(reg, &rep, byID, opts)
	}
	if !opts.Skip["orphan-records"] {
		lintOrphanRecords(reg, &rep, features, byID, variantMap, opts)
	}
	if !opts.Skip["variants"] && variantMap.Configured() {
		lintVariants(reg, &rep, features, anchors, variantMap)
	}
	if !opts.Skip["taxonomy"] && taxonomy.Configured() {
		lintTaxonomy(reg, &rep, features, taxonomy)
	}
	if !opts.Skip["duplicate-glossary-term"] && glossary.Configured() {
		lintDuplicateGlossaryTerms(reg, &rep, glossary)
	}
	if !opts.Skip["glossary-near-miss"] && glossary.Configured() {
		lintGlossaryNearMiss(reg, &rep, features, glossary)
	}
	if !opts.Skip["duplicate-glossary-category"] && glossary.Configured() {
		lintDuplicateGlossaryCategories(reg, &rep, glossary)
	}
	if !opts.Skip["undeclared-glossary-category"] && glossary.Configured() {
		lintUndeclaredGlossaryCategories(reg, &rep, glossary)
	}
	if !opts.Skip["empty-glossary-category"] && glossary.Configured() {
		lintEmptyGlossaryCategories(reg, &rep, glossary)
	}
	if !opts.Skip["sizing"] {
		lintSizing(reg, &rep, features)
	}
	if !opts.Skip["flags"] {
		lintFlags(reg, &rep, features, opts)
	}
	if !opts.Skip["knowledge"] {
		lintKnowledge(reg, &rep, features)
	}
	if !opts.Skip["backing"] {
		lintBacking(reg, &rep, features, variantMap)
	}
	if !opts.Skip["deviation"] {
		lintDeviation(reg, &rep, features)
	}
	if !opts.Skip["spec-conformance"] {
		pin := opts.specPin
		if pin == nil {
			current := readSpecPin(reg.Root)
			pin = &current
		}
		lintSpecConformance(reg, &rep, features, *pin)
	}
	if !opts.Skip["examples"] {
		lintExamples(reg, &rep, features)
	}
	if !opts.Skip["dependencies"] {
		lintDependencies(reg, &rep, opts.KnownIDs)
	}
	if !opts.Skip["broken-links"] {
		lintBrokenLinks(reg, &rep)
	}
	if !opts.Skip["superseded-by"] {
		lintSupersededBy(reg, &rep, features, byID)
	}
	if !opts.Skip["anchor-roles"] {
		lintAnchorRoles(reg, &rep, features, byID)
	}
	if !opts.Skip["proves"] {
		lintProves(reg, &rep, features)
	}
	if !opts.Skip["verification-targets"] {
		lintVerificationTargets(reg, &rep, features)
	}
	if !opts.Skip["unclaimed-tests"] {
		lintUnclaimedTests(reg, &rep, features, walked, opts.IsTestPath)
	}
	if !opts.Skip["sibling-suffix"] {
		lintSiblingSuffix(reg, &rep, features)
	}
	if !opts.Skip["created-at-history"] {
		// The git pass is inside the guard: skipping the check has to skip its
		// cost too, or '--skip' would buy nothing on the one check that shells
		// out.
		lintCreatedAt(reg, &rep, features, byID, FirstAdded(reg.Root))
	}
	if !opts.Skip["no-scenarios"] {
		lintNoScenarios(reg, &rep, features, byID)
	}
	if !opts.Skip["anchor-proves"] {
		lintAnchorProves(reg, &rep, features, byID)
	}
	if !opts.Skip["test-only-anchors"] {
		lintTestOnlyAnchors(reg, &rep, features, byID, opts)
	}
	if !opts.Skip["enum-divergence"] {
		if entities, err := reg.LoadCategory("data-entities"); err == nil {
			lintEnumDivergence(reg, &rep, entities)
		}
	}
	if !opts.Skip["unanchored-hotspots"] {
		lintUnanchoredHotspots(reg, &rep, walked, opts.IsTestPath)
	}
	if !opts.Skip["undocumented-directories"] {
		lintUndocumentedDirectories(reg, &rep, walked, opts.IsTestPath)
	}
	if !opts.Skip["error-codes"] {
		if err := lintErrorCodeCollisions(reg, &rep, features, opts.ErrorCodePattern); err != nil {
			return rep, err
		}
	}
	if !opts.Skip["lost-anchors"] {
		cs := opts.Changes
		if cs == nil {
			loaded := LoadSourceChangeSet(reg.Root, []Registry{reg}, "")
			cs = &loaded
		}
		lintLostAnchors(reg, &rep, cs, anchors, opts.KnownIDs)
	}

	if !opts.Skip["plugin-lint"] {
		rep.Findings = append(rep.Findings, RunPluginLint(reg, opts.PluginsDir)...)
	}

	for _, cat := range Categories {
		recs, err := reg.LoadCategory(cat)
		if err == nil {
			rep.Checked.Records += len(recs)
		}
	}

	// Every finding this registry produced carries its name, so a combined
	// report over a monorepo says which product is broken rather than only that
	// something is.
	for i := range rep.Findings {
		rep.Findings[i].Registry = reg.Name
	}
	// A nil slice marshals to `null`, and a JSON client that unconditionally
	// iterates `findings` (as the LSP's diagnostics contract already promises
	// it never has to) breaks on the clean-registry case. Same fix as
	// diagnostics: send an empty list, not null.
	if rep.Findings == nil {
		rep.Findings = []Finding{}
	}
	return rep, nil
}

// LintAll runs the checks over every declared registry for facet lint.
// A project declaring none receives exactly one pass over its default registry.
func LintAll(root string, opts LintOptions) (Report, error) {
	cfg, err := LoadConfigWithResolvedSourceView(root, opts.Scan.View)
	if err != nil {
		return Report{}, err
	}
	if opts.Registry != "" {
		if _, err := cfg.Select(opts.Registry); err != nil {
			return Report{}, err
		}
	}
	if !opts.Skip["spec-conformance"] {
		pin := readSpecPin(root)
		opts.specPin = &pin
	}
	registries := cfg.All()
	for i := range registries {
		registries[i].categorySnapshot = make(map[string]categoryLoad)
		if opts.Scan.View != nil {
			registries[i].SourceView = opts.Scan.View
		}
		if err := registries[i].ValidateSourceView(); err != nil {
			return Report{}, err
		}
	}

	// Use the project configuration only when the caller left ErrorCodePattern
	// empty; an explicit option takes precedence.
	if opts.ErrorCodePattern == "" {
		opts.ErrorCodePattern = cfg.ErrorCodePattern()
	}

	known := map[string]bool{}
	for _, reg := range registries {
		if reg.Name != "" {
			known[reg.Name] = true
		}
	}

	// Which ids exist anywhere, and which paths more than one registry claims.
	// Both need every registry in hand, so they are computed before any check
	// runs and handed to each pass.
	owned := map[string]bool{}
	for _, reg := range registries {
		features, err := reg.LoadFeatures()
		if err != nil {
			continue
		}
		for _, rec := range features {
			owned[rec.ID] = true
		}
	}

	// The lineage graph spans categories as well as registries: a feature may
	// depend on a policy, and in a monorepo on another product's record.
	ids := knownIDs(registries)

	set := RegistrySet(registries)
	shared := func(rel string) bool { return len(set.Claimants(rel)) > 1 }

	// One comparison for the whole project, as 'facet drift' makes it: every
	// registry reads the same change set rather than asking git again.
	if opts.Changes == nil && !opts.Skip["lost-anchors"] {
		cs := LoadSourceChangeSet(root, registries, "")
		opts.Changes = &cs
	}

	var combined Report

	// The configuration is a committed document like any other, so it is held to
	// its schema too — and checked once, since it belongs to the project rather
	// than to any one registry.
	if !opts.Skip["schema"] && cfg.Path != "" {
		lintConfig(registries[0], &combined, cfg)
	}
	if !opts.Skip["conventions"] {
		lintConventions(&combined, cfg, registries)
	}

	for _, reg := range registries {
		if opts.Registry != "" && reg.Name != opts.Registry {
			continue
		}
		regOpts := opts
		regOpts.KnownRegistries = known
		regOpts.KnownIDs = ids
		if len(registries) > 1 {
			regOpts.OwnedElsewhere = owned
			regOpts.Shared = shared
		}
		// A declared registry's own `sources` win; the environment override
		// applies only where the project left the choice open.
		if len(reg.Sources) > 0 || reg.SourceView != nil {
			regOpts.Scan = reg.ScanOptions()
		}
		rep, err := lint(reg, regOpts)
		if err != nil {
			return combined, fmt.Errorf("registry %q: %w", registryLabel(reg), err)
		}
		combined.Findings = append(combined.Findings, rep.Findings...)
		combined.Checked.Records += rep.Checked.Records
		combined.Checked.Anchors += rep.Checked.Anchors
		combined.Checked.Registries = append(combined.Checked.Registries, rep.Checked.Registries...)
	}

	combined.Findings = dedupeProjectFindings(combined.Findings)
	if combined.Findings == nil {
		combined.Findings = []Finding{}
	}
	return combined, nil
}

func dedupeProjectFindings(findings []Finding) []Finding {
	seen := map[string]bool{}
	out := findings[:0]
	for _, f := range findings {
		if f.Check == "registry-refs" || (f.Check == "spec-conformance" && f.ID == "") {
			key := f.Check + "\x00" + f.ID + "\x00" + f.Path
			if seen[key] {
				continue
			}
			seen[key] = true
		}
		out = append(out, f)
	}
	return out
}

func registryLabel(reg Registry) string {
	if reg.Name == "" {
		return DefaultRegistryName
	}
	return reg.Name
}

// lintConfig validates .facetrc.yaml, and reports a declared registry whose
// records directory does not exist — a typo there is silent otherwise, since an
// empty registry has no records to find fault with and passes every check.
func lintConfig(reg Registry, rep *Report, cfg *Config) {
	schemaPath := filepath.Join(reg.SchemaDir, "facetrc.schema.json")
	if isFile(schemaPath) {
		if compiled, err := compileSchema(schemaPath); err == nil {
			validateRecord(reg, rep, compiled, Record{
				Path:   cfg.Path,
				Fields: loadYAMLFields(cfg.Path),
			})
		}
	}

	for _, name := range cfg.Names() {
		records := cfg.Registries[name].Records
		if records == "" {
			records = DefaultRecordsDir
		}
		if isDir(absUnder(reg.Root, records)) {
			continue
		}
		rep.add(Finding{
			Severity: SeverityError,
			Check:    "config",
			Registry: name,
			Path:     reg.Rel(cfg.Path),
			Message:  fmt.Sprintf("registry %q points at %q, which does not exist", name, records),
			Hint:     "create the directory, correct the path, or remove the registry",
		})
	}
}

// knownIDs collects every record id the given registries hold.
func knownIDs(registries []Registry) map[string]bool {
	out := map[string]bool{}
	for _, reg := range registries {
		for _, cat := range Categories {
			recs, err := reg.LoadCategory(cat)
			if err != nil {
				continue
			}
			for _, rec := range recs {
				out[rec.ID] = true
			}
		}
	}
	return out
}
