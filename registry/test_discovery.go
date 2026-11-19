// @facet: registry/integrity/runner-test-discovery
// @facet-role: primary
package facet

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
)

// Runner declares commands; discovery never executes Command or reads reports.
type Runner struct {
	Repository string `yaml:"repository"`
	Cwd        string `yaml:"cwd"`
	Command    string `yaml:"command"`
	List       string `yaml:"list"`
	ListFormat string `yaml:"list_format"`
	Report     string `yaml:"report"`
	ReportFile string `yaml:"report_file"`
}

// Existing configuration sections retain their established tolerant decoding;
// runner declarations reject unknown fields so a misspelled list cannot hide.
func validateRunnerFields(data []byte) error {
	var fields map[string]any
	if err := yaml.Unmarshal(data, &fields); err != nil {
		return err
	}
	blocks := []any{fields["runners"]}
	registries, _ := fields["registries"].(map[string]any)
	for _, value := range registries {
		entry, _ := value.(map[string]any)
		blocks = append(blocks, entry["runners"])
	}
	for _, block := range blocks {
		if block == nil {
			continue
		}
		encoded, err := yaml.Marshal(block)
		if err != nil {
			return err
		}
		var runners map[string]Runner
		if err = yaml.UnmarshalWithOptions(encoded, &runners, yaml.Strict()); err != nil {
			return err
		}
	}
	return nil
}

func validateRunners(runners map[string]Runner) error {
	for name, r := range runners {
		if !validRegistryName(name) || strings.TrimSpace(r.Command) == "" {
			return fmt.Errorf("runner %q requires a valid name and nonempty command", name)
		}
		if r.ListFormat != "" && r.ListFormat != "lines" && r.ListFormat != "json" {
			return fmt.Errorf("runner %q: unsupported list_format %q; use lines or json", name, r.ListFormat)
		}
		if r.ListFormat != "" && strings.TrimSpace(r.List) == "" {
			return fmt.Errorf("runner %q: list_format requires a list command", name)
		}
		for _, p := range []string{r.Cwd, r.ReportFile} {
			if p != "" && (filepath.IsAbs(p) || filepath.Clean(p) == ".." || strings.HasPrefix(filepath.Clean(p), ".."+string(filepath.Separator))) {
				return fmt.Errorf("runner %q path %q must stay within repository", name, p)
			}
		}
		if r.Report != "" && r.Report != "junit" && r.Report != "go-json" && r.Report != "cucumber-json" {
			return fmt.Errorf("runner %q: unsupported report %q", name, r.Report)
		}
		if (r.Report == "") != (r.ReportFile == "") {
			return fmt.Errorf("runner %q: report and report_file must be declared together", name)
		}
	}
	return nil
}

// TestFinding is discovery drift, never a test result.
type TestFinding struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Runner   string `json:"runner,omitempty"`
	Path     string `json:"path,omitempty"`
	Message  string `json:"message"`
}

// DiscoveredRunner names a runner and its selected repository-relative test
// files. Measured is true only after valid, nonempty discovery succeeds.
type DiscoveredRunner struct {
	Name     string   `json:"name"`
	Measured bool     `json:"measured"`
	Files    []string `json:"files"`
}

// TestDiscovery combines one registry's runner selections and discovery
// findings. It records file discovery, without claiming tests passed.
type TestDiscovery struct {
	applyBaseline   func() error
	FeaturePrefixes []string           `json:"feature_prefixes,omitempty"`
	FeaturePrefix   string             `json:"feature_prefix,omitempty"`
	Registry        string             `json:"registry,omitempty"`
	Runners         []DiscoveredRunner `json:"runners"`
	Findings        []TestFinding      `json:"findings"`
}

// Failed reports whether discovery produced an error finding.
func (r TestDiscovery) Failed() bool {
	for _, f := range r.Findings {
		if f.Severity == "error" {
			return true
		}
	}
	return false
}

type testBaseline struct {
	Runners map[string][]string `yaml:"runners"`
}

func readTestBaseline(path string) (testBaseline, error) {
	b := testBaseline{Runners: map[string][]string{}}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return b, nil
	}
	if err != nil {
		return b, err
	}
	err = yaml.UnmarshalWithOptions(data, &b, yaml.Strict())
	if b.Runners == nil {
		b.Runners = map[string][]string{}
	}
	return b, err
}

// normalizedTestPath rejects escapes, directories and symlinks outside root.
func normalizedTestPath(root, base, path string) (string, error) {
	if strings.ContainsAny(path, "\x00\r\n") {
		return "", fmt.Errorf("invalid test path %q", path)
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	full := path
	if !filepath.IsAbs(full) {
		full = filepath.Join(base, full)
	}
	full, err = filepath.Abs(full)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes repository", path)
	}
	resolved, err := filepath.EvalSymlinks(full)
	if err != nil {
		return "", err
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	realRel, err := filepath.Rel(realRoot, resolved)
	if err != nil || realRel == ".." || strings.HasPrefix(realRel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q resolves outside repository", path)
	}
	info, err := os.Stat(full)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("path %q is not a regular test file", path)
	}
	return filepath.ToSlash(rel), nil
}

// CheckTests executes list only. Missing discovery makes absence unmeasured;
// known new unclaimed files still fail, even in a partially measured registry.
func CheckTests(ctx context.Context, cfg *Config, reg Registry, selected string, update bool) (TestDiscovery, error) {
	return checkTests(ctx, cfg, reg, selected, update, false)
}
func checkTests(ctx context.Context, cfg *Config, reg Registry, selected string, update, deferBaseline bool) (TestDiscovery, error) {
	result := TestDiscovery{FeaturePrefixes: cfg.TestFeaturePrefixes, FeaturePrefix: cfg.TestFeaturePrefix, Registry: reg.Name, Runners: []DiscoveredRunner{}, Findings: []TestFinding{}}
	runners := cfg.Runners
	if cfg.MultiRegistry() {
		runners = cfg.Registries[reg.Name].Runners
	}
	if selected != "" {
		r, ok := runners[selected]
		if !ok {
			return result, fmt.Errorf("unknown runner %q in registry %q", selected, reg.Name)
		}
		runners = map[string]Runner{selected: r}
	}
	if len(runners) == 0 {
		return result, fmt.Errorf("registry %q declares no runners", reg.Name)
	}
	records, err := testScopeFeatures(cfg, reg)
	if err != nil {
		return result, err
	}
	scoped := map[string]bool{}
	for _, rec := range records {
		scoped[rec.ID] = true
	}
	allRecords, err := reg.LoadFeatures()
	if err != nil {
		return result, err
	}
	claimed := map[string]bool{}
	knownClaimed := map[string]bool{}
	claims := map[string]map[string]bool{}
	for _, rec := range allRecords {
		verification, _ := rec.Fields["verification"].(map[string]any)
		for _, kind := range verificationKinds {
			entries, _ := verification[kind].([]any)
			for _, entry := range entries {
				ref, _ := entry.(map[string]any)
				id, _ := ref["id"].(string)
				if path, ok := verificationPath(id); ok {
					if rel, e := selectedTestReference(reg, path); e == nil {
						knownClaimed[rel] = true
						if scoped[rec.ID] {
							claimed[rel] = true
						}
					}
				}
			}
		}
		for _, p := range rec.Provenances() {
			if path, ok := verificationPath(p.TestID); ok {
				rel, e := selectedTestReference(reg, path)
				if e != nil {
					continue
				}
				knownClaimed[rel] = true
				if scoped[rec.ID] {
					claimed[rel] = true
				}
				key := rec.ID + "\x00" + p.Scenario
				if claims[key] == nil {
					claims[key] = map[string]bool{}
				}
				claims[key][rel] = true
			}
		}
	}
	baselinePath := filepath.Join(reg.RecordsDir, "test-baseline.yaml")
	if update {
		unlock, e := lockTestBaseline(ctx, baselinePath)
		if e != nil {
			return result, e
		}
		defer unlock()
	}
	baselineBytes, _ := os.ReadFile(baselinePath)
	baseline, err := readTestBaseline(baselinePath)
	if err != nil {
		return result, err
	}
	// Validate canonical baseline paths lexically, including files since deleted.
	for name, paths := range baseline.Runners {
		if !validRegistryName(name) {
			return result, fmt.Errorf("invalid baseline runner %q", name)
		}
		for _, p := range paths {
			if p == "" || filepath.IsAbs(p) || filepath.ToSlash(filepath.Clean(p)) != p || p == ".." || strings.HasPrefix(p, "../") {
				return result, fmt.Errorf("invalid baseline path %q", p)
			}
		}
	}
	names := make([]string, 0, len(runners))
	for n := range runners {
		names = append(names, n)
	}
	sort.Strings(names)
	all := map[string]bool{}
	complete := selected == ""
	add := func(severity, code, name, path, message string) {
		result.Findings = append(result.Findings, TestFinding{severity, code, name, path, message})
	}
	changed := false
	for _, name := range names {
		r := runners[name]
		runnerRoot, repository, e := selectedRunnerRoot(reg, r)
		if e != nil {
			return result, e
		}
		discovery := DiscoveredRunner{Name: name, Files: []string{}}
		if strings.TrimSpace(r.List) == "" {
			complete = false
			add("warning", "unmeasured", name, "", "discovery not measured: runner has no list command")
			result.Runners = append(result.Runners, discovery)
			continue
		}
		cwd := filepath.Join(runnerRoot, r.Cwd)
		resolved, e := filepath.EvalSymlinks(cwd)
		if e != nil {
			return result, e
		}
		realRoot, e := filepath.EvalSymlinks(runnerRoot)
		if e != nil {
			return result, e
		}
		rel, e := filepath.Rel(realRoot, resolved)
		if e != nil || rel == ".." || strings.HasPrefix(rel, "../") {
			return result, fmt.Errorf("runner %q cwd escapes repository", name)
		}
		command := testListCommand(ctx, r.List)
		command.Dir = cwd
		var stderr strings.Builder
		command.Stderr = &stderr
		output, e := command.Output()
		if e != nil {
			complete = false
			add("error", "discovery-failed", name, "", fmt.Sprintf("list failed: %v: %s", e, strings.TrimSpace(stderr.String())))
			result.Runners = append(result.Runners, discovery)
			continue
		}
		files := map[string]bool{}
		valid := true
		paths, e := discoveryPaths(output, r.ListFormat)
		if e != nil {
			complete = false
			add("error", "invalid-discovery", name, "", e.Error())
			result.Runners = append(result.Runners, discovery)
			continue
		}
		for _, line := range paths {
			line = strings.TrimSuffix(line, "\r")
			if line == "" {
				continue
			}
			p, e := normalizedTestPath(runnerRoot, cwd, line)
			if e != nil {
				valid = false
				add("error", "invalid-path", name, line, e.Error())
				continue
			}
			if repository != "" {
				p, e = reg.SourceView.Qualify(repository, p)
				if e != nil {
					return result, e
				}
			}
			files[p] = true
		}
		if len(files) == 0 {
			valid = false
			add("error", "empty-discovery", name, "", "list discovered no test files")
		}
		if !valid {
			complete = false
			result.Runners = append(result.Runners, discovery)
			continue
		}
		discovery.Measured = true
		for p := range files {
			discovery.Files = append(discovery.Files, p)
			all[p] = true
		}
		sort.Strings(discovery.Files)
		old := map[string]bool{}
		for _, p := range baseline.Runners[name] {
			old[p] = true
		}
		for _, p := range discovery.Files {
			if !knownClaimed[p] && !old[p] {
				add("error", "new-unclaimed", name, p, "discovered test is unclaimed and absent from baseline; read its body before adding proves")
			}
		}
		kept := []string{}
		for _, p := range baseline.Runners[name] {
			if files[p] && !knownClaimed[p] {
				kept = append(kept, p)
			} else {
				add("warning", "stale-baseline", name, p, "baseline entry is claimed or no longer discovered")
				changed = true
			}
		}
		if update {
			baseline.Runners[name] = kept
		}
		result.Runners = append(result.Runners, discovery)
	}
	if complete {
		paths := []string{}
		for p := range claimed {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			if !all[p] {
				add("error", "not-discovered", "", p, "existing verification file is absent from all runner discovery")
			}
		}
	} else {
		add("warning", "absence-unmeasured", "", "", "verification absence not measured: discovery incomplete or runner selection partial")
	}
	// Scan only the discovered files: unknown extensions still carry anchors.
	for _, d := range result.Runners {
		for _, p := range d.Files {
			data, e := readSelectedTest(reg, p)
			if e != nil {
				return result, e
			}
			for _, a := range ScanContent(p, data) {
				if a.Registry != "" && a.Registry != reg.Name {
					continue
				}
				for _, scenario := range a.Proves {
					if !claims[a.ID+"\x00"+scenario][p] {
						add("warning", "proves-mismatch", d.Name, p, fmt.Sprintf("anchor %s/%s has no matching verification claim", a.ID, scenario))
					}
				}
			}
		}
	}
	if update && changed {
		data, e := yaml.Marshal(baseline)
		if e != nil {
			return result, e
		}
		if deferBaseline {
			result.applyBaseline = func() error {
				unlock, e := lockTestBaseline(ctx, baselinePath)
				if e != nil {
					return e
				}
				defer unlock()
				current, _ := os.ReadFile(baselinePath)
				if !bytes.Equal(current, baselineBytes) {
					return fmt.Errorf("test baseline changed during execution")
				}
				return writeTestBaseline(baselinePath, data)
			}
		} else if e = writeTestBaseline(baselinePath, data); e != nil {
			return result, e
		}
	}
	return result, nil
}

// Machine-readable discovery uses explicit paths only, never titles or globs.
// String arrays and objects with a file field cover declared JSON-producing
// tools without teaching FACET their configuration or versions.
func discoveryPaths(data []byte, format string) ([]string, error) {
	if format != "json" {
		return strings.Split(string(data), "\n"), nil
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("invalid JSON discovery; expected an array of paths or objects with file: %w", err)
	}
	if entries == nil {
		return nil, fmt.Errorf("JSON discovery must be an array, not null")
	}
	paths := make([]string, 0, len(entries))
	for index, entry := range entries {
		var path string
		if err := json.Unmarshal(entry, &path); err != nil {
			var object struct {
				File string `json:"file"`
			}
			if err := json.Unmarshal(entry, &object); err != nil {
				return nil, fmt.Errorf("JSON discovery entry %d must be a path or an object with file", index+1)
			}
			path = object.File
		}
		if path == "" {
			return nil, fmt.Errorf("JSON discovery entry %d has no explicit file path", index+1)
		}
		paths = append(paths, path)
	}
	return paths, nil
}

// Test scope is explicit configuration, never inferred from file patterns.
func testScopeFeatures(cfg *Config, reg Registry) ([]Record, error) {
	records, err := reg.LoadFeatures()
	prefixes := append([]string{}, cfg.TestFeaturePrefixes...)
	if cfg.TestFeaturePrefix != "" {
		prefixes = append(prefixes, cfg.TestFeaturePrefix)
	}
	if err != nil || len(prefixes) == 0 {
		return records, err
	}
	for _, prefix := range prefixes {
		if strings.TrimSpace(prefix) == "" {
			return nil, fmt.Errorf("test feature prefixes must not contain an empty prefix")
		}
	}
	selected := []Record{}
	matched := map[string]bool{}
	for _, record := range records {
		keep := false
		for _, prefix := range prefixes {
			if strings.HasPrefix(record.ID, prefix) {
				keep = true
				matched[prefix] = true
			}
		}
		if keep {
			selected = append(selected, record)
		}
	}
	for _, prefix := range prefixes {
		if !matched[prefix] {
			return nil, fmt.Errorf("test feature prefix %q matches no features in registry %q", prefix, reg.Name)
		}
	}
	return selected, nil
}
