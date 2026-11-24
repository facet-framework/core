package facet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The schema must accept the embedding settings LoadConfig already reads,
// while refusing inline credentials, unknown keys and incorrectly typed values.
// Removing embedding from the shipped schema makes the valid cases fail.
func TestEmbeddingConfigPassesTheRealSchema(t *testing.T) {
	root := project(t, "")
	copySchemas(t, root)
	compiled, err := compileSchema(filepath.Join(root, "docs/facets/schema/facetrc.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		embedding map[string]any
		valid     bool
	}{
		{"openrouter", map[string]any{"provider": "openai", "base_url": "https://openrouter.ai/api/v1", "model": "qwen/qwen3-embedding-8b", "api_key_env": "OPENROUTER_API_KEY"}, true},
		{"defaults", map[string]any{}, true},
		{"local", map[string]any{"provider": "local", "dimensions": 256, "batch": 32, "index": "cache/vectors.bin"}, true},
		{"inline credential", map[string]any{"api_key": "example"}, false},
		{"unknown key", map[string]any{"modle": "example"}, false},
		{"invalid dimensions", map[string]any{"dimensions": "256"}, false},
		{"invalid batch", map[string]any{"batch": 1.5}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := compiled.Validate(map[string]any{"embedding": tc.embedding})
			if (err == nil) != tc.valid {
				t.Fatalf("schema validation error = %v, want valid = %v", err, tc.valid)
			}
		})
	}
}

// writeFile creates a file and every directory above it.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// project builds a temporary repository, optionally with a .facetrc.yaml.
func project(t *testing.T, config string) string {
	t.Helper()
	root := t.TempDir()
	if config != "" {
		writeFile(t, ConfigPath(root), config)
	}
	return root
}

func TestNoConfigKeepsThePreviousDefaults(t *testing.T) {
	root := project(t, "")

	cfg, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MultiRegistry() {
		t.Fatal("a project with no config must not report registries")
	}

	all := cfg.All()
	if len(all) != 1 {
		t.Fatalf("want exactly one implicit registry, got %d", len(all))
	}
	if all[0].Name != "" {
		t.Errorf("the implicit registry must stay unnamed, got %q", all[0].Name)
	}
	if want := filepath.Join(root, "docs", "facets"); all[0].RecordsDir != want {
		t.Errorf("records = %q, want %q", all[0].RecordsDir, want)
	}
}

func TestConfigMovesTheRecordsDirectory(t *testing.T) {
	root := project(t, "schema_version: \"0.1\"\nrecords: documentation/facet\n")

	reg := Open(root)
	if want := filepath.Join(root, "documentation", "facet"); reg.RecordsDir != want {
		t.Errorf("records = %q, want %q", reg.RecordsDir, want)
	}
}

// The environment overrides exist for CI jobs and migration scripts pointing the
// linter at a scratch tree, which a committed file cannot express.
func TestEnvironmentStillOverridesTheConfig(t *testing.T) {
	root := project(t, "records: documentation/facet\n")
	t.Setenv("FACET_RECORDS_DIR", "elsewhere/facets")

	reg := Open(root)
	if want := filepath.Join(root, "elsewhere", "facets"); reg.RecordsDir != want {
		t.Errorf("records = %q, want %q", reg.RecordsDir, want)
	}
}

func TestRegistriesEachGetTheirOwnRecordsAndSources(t *testing.T) {
	root := project(t, `
schema_version: "0.1"
registries:
  storefront:
    records: apps/storefront/docs/facets
    sources: [apps/storefront, packages/shared]
  admin:
    records: apps/admin/docs/facets
    sources: [apps/admin, packages/shared]
`)

	cfg, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.MultiRegistry() {
		t.Fatal("want a multi-registry project")
	}

	all := cfg.All()
	if len(all) != 2 {
		t.Fatalf("want 2 registries, got %d", len(all))
	}
	// Sorted, so a report over a monorepo reads the same on every machine.
	if all[0].Name != "admin" || all[1].Name != "storefront" {
		t.Fatalf("registries must be sorted, got %q then %q", all[0].Name, all[1].Name)
	}
	if want := filepath.Join(root, "apps", "admin", "docs", "facets"); all[0].RecordsDir != want {
		t.Errorf("admin records = %q, want %q", all[0].RecordsDir, want)
	}
	if got := all[0].ScanOptions().Dirs; len(got) != 2 || got[0] != "apps/admin" {
		t.Errorf("admin sources = %v", got)
	}
}

// Open is what every hook calls, and a hook must not care whether the project
// has one registry or five.
func TestOpenReturnsTheFirstRegistryOfAMonorepo(t *testing.T) {
	root := project(t, `
registries:
  zebra:
    records: z/facets
  alpha:
    records: a/facets
`)

	if got := Open(root).Name; got != "alpha" {
		t.Errorf("Open picked %q, want the first sorted registry \"alpha\"", got)
	}
}

func TestTopLevelRecordsAlongsideRegistriesIsRejected(t *testing.T) {
	root := project(t, `
records: docs/facets
registries:
  admin:
    records: apps/admin/docs/facets
`)

	_, err := LoadConfig(root)
	if err == nil {
		t.Fatal("want an error: which records directory the linter means is otherwise unanswerable")
	}
	if !strings.Contains(err.Error(), "registries") {
		t.Errorf("the error must point at the conflict, got %v", err)
	}
}

// A registry name is written in front of an id in a qualified anchor, so a name
// the anchor grammar cannot express would produce a tag nothing can parse.
func TestRegistryNamesFollowTheIdGrammar(t *testing.T) {
	for _, name := range []string{"Admin", "admin_app", "2fa", "-admin", ""} {
		root := project(t, "registries:\n  \""+name+"\":\n    records: a/facets\n")
		if _, err := LoadConfig(root); err == nil {
			t.Errorf("registry name %q was accepted, but no anchor can name it", name)
		}
	}
	for _, name := range []string{"admin", "storefront-v2", "a1"} {
		root := project(t, "registries:\n  "+name+":\n    records: a/facets\n")
		if _, err := LoadConfig(root); err != nil {
			t.Errorf("registry name %q was rejected: %v", name, err)
		}
	}
}

func TestSelectNamesTheAlternativesWhenAskedForOneThatDoesNotExist(t *testing.T) {
	root := project(t, `
registries:
  admin:
    records: apps/admin/docs/facets
  storefront:
    records: apps/storefront/docs/facets
`)
	cfg, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := cfg.Select("stroefront"); err == nil {
		t.Fatal("want an error for an undeclared registry")
	} else if !strings.Contains(err.Error(), "admin, storefront") {
		t.Errorf("the error must list what does exist, got %v", err)
	}

	if reg, err := cfg.Select("storefront"); err != nil || reg.Name != "storefront" {
		t.Errorf("Select(storefront) = %q, %v", reg.Name, err)
	}
}

// The default applies whether the project has no config at all, or a config
// that declares nothing under error_codes — the two must behave identically.
func TestErrorCodePatternDefaultsWhenNotDeclared(t *testing.T) {
	for _, config := range []string{"", "schema_version: \"0.1\"\nrecords: docs/facets\n"} {
		root := project(t, config)
		cfg, err := LoadConfig(root)
		if err != nil {
			t.Fatal(err)
		}
		if got := cfg.ErrorCodePattern(); got != DefaultErrorCodePattern {
			t.Errorf("ErrorCodePattern() = %q, want the default %q", got, DefaultErrorCodePattern)
		}
	}
	// A nil Config — the zero value a caller gets without ever calling
	// LoadConfig — must also fall back to the default rather than panic or
	// return an empty pattern nothing can compile.
	var nilCfg *Config
	if got := nilCfg.ErrorCodePattern(); got != DefaultErrorCodePattern {
		t.Errorf("nil Config.ErrorCodePattern() = %q, want the default %q", got, DefaultErrorCodePattern)
	}
}

func TestErrorCodePatternIsConfigurable(t *testing.T) {
	root := project(t, "schema_version: \"0.1\"\nerror_codes:\n  pattern: 'ERR-\\d{3}'\n")
	cfg, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cfg.ErrorCodePattern(), `ERR-\d{3}`; got != want {
		t.Errorf("ErrorCodePattern() = %q, want %q", got, want)
	}
}

// error_codes applies to the whole project, so it must be declared even
// alongside a registries block — unlike the single-registry shorthand
// fields, which conflict with registries because they would leave "which
// registry does this mean" unanswerable.
func TestErrorCodePatternCoexistsWithRegistries(t *testing.T) {
	root := project(t, `
schema_version: "0.1"
error_codes:
  pattern: 'E\d{4}'
registries:
  storefront:
    records: apps/storefront/docs/facets
  admin:
    records: apps/admin/docs/facets
`)
	cfg, err := LoadConfig(root)
	if err != nil {
		t.Fatalf("error_codes alongside registries must be accepted: %v", err)
	}
	if got, want := cfg.ErrorCodePattern(), `E\d{4}`; got != want {
		t.Errorf("ErrorCodePattern() = %q, want %q", got, want)
	}
}

// error_codes is a new key on a schema whose top level is
// additionalProperties: false — without a matching schema update, declaring
// it would fail the run it exists to help.
func TestErrorCodesConfigPassesTheRealSchema(t *testing.T) {
	root := project(t, "schema_version: \"0.1\"\nerror_codes:\n  pattern: 'E\\d{4}'\n")
	copySchemas(t, root)

	rep, err := LintAll(root, LintOptions{Skip: map[string]bool{"knowledge": true}})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range rep.Findings {
		if f.Check == "schema" || f.Check == "config" {
			t.Errorf("error_codes in %s must validate against the shipped schema, got %+v", ConfigName, f)
		}
	}
}

func TestEmbeddingConfigPassesSingleAndMultiRegistryValidation(t *testing.T) {
	for _, layout := range []string{"", "registries:\n  app:\n    records: docs/facets\n"} {
		for _, fields := range []string{"", "  provider: openai\n  base_url: https://example.test/v1\n  model: example\n  api_key_env: EMBEDDING_TOKEN\n  dimensions: 128\n  batch: 32\n  index: .cache/index.bin\n"} {
			config := layout + "embedding: {}\n"
			if fields != "" {
				config = layout + "embedding:\n" + fields
			}
			root := project(t, config)
			copySchemas(t, root)
			rep, err := LintAll(root, LintOptions{})
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range rep.Findings {
				if f.Check == "schema" || f.Check == "config" {
					t.Errorf("valid embedding configuration rejected: %+v", f)
				}
			}
		}
	}
}

func TestEmbeddingSchemaRejectsUnknownFieldsAndWrongTypes(t *testing.T) {
	for _, field := range []string{"api_key: secret", "modle: typo", "dimensions: many", "batch: 1.5", "provider: []", "api_key_env: 123"} {
		t.Run(field, func(t *testing.T) {
			root := project(t, "embedding:\n  "+field+"\n")
			copySchemas(t, root)
			reg := Registry{Root: root, RecordsDir: filepath.Join(root, "docs/facets"), SchemaDir: filepath.Join(root, "docs/facets/schema")}
			var rep Report
			schema, err := compileSchema(filepath.Join(reg.SchemaDir, "facetrc.schema.json"))
			if err != nil {
				t.Fatal(err)
			}
			validateRecord(reg, &rep, schema, Record{Path: ConfigPath(root), Fields: loadYAMLFields(ConfigPath(root))})
			for _, f := range rep.Findings {
				if f.Check == "schema" && f.Severity == SeverityError {
					return
				}
			}
			t.Fatal("invalid embedding configuration passed schema validation")
		})
	}
}

func TestClaimsIsPrefixMatchingOnDirectoryBoundaries(t *testing.T) {
	reg := Registry{Sources: []string{"apps/admin", "packages/shared"}}

	for _, rel := range []string{"apps/admin/main.go", "packages/shared/db/pool.go", "apps/admin"} {
		if !reg.Claims(rel) {
			t.Errorf("%q should be claimed", rel)
		}
	}
	// 'apps/admin-legacy' is a different directory, not a file inside apps/admin.
	for _, rel := range []string{"apps/admin-legacy/main.go", "apps/storefront/main.go"} {
		if reg.Claims(rel) {
			t.Errorf("%q should not be claimed", rel)
		}
	}

	// A registry that declares no sources sees the whole repository, which is
	// what every single-registry project relies on.
	if !(Registry{}).Claims("anywhere/at/all.go") {
		t.Error("a registry with no declared sources must claim everything")
	}
}

func TestEmbeddingConfigIsRead(t *testing.T) {
	root := project(t, `records: docs/facets
embedding:
  provider: openai
  base_url: https://api.openai.com/v1
  model: text-embedding-3-small
  api_key_env: OPENAI_API_KEY
  dimensions: 512
  batch: 32
`)
	cfg, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	e := cfg.EmbeddingSettings()
	if e.Provider != "openai" || e.Model != "text-embedding-3-small" {
		t.Fatalf("embedding block not read: %+v", e)
	}
	if e.APIKeyEnv != "OPENAI_API_KEY" || e.Dimensions != 512 || e.Batch != 32 {
		t.Fatalf("embedding block partially read: %+v", e)
	}
}

// Embedding configuration accepts environment-variable names. An inline
// api_key has no destination configuration field and is not retained,
// keeping credential values out of committed configuration.
func TestEmbeddingConfigHasNowhereToPutAKey(t *testing.T) {
	root := project(t, `embedding:
  provider: openai
  api_key: sk-a-key-somebody-pasted
`)
	cfg, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	rendered := strings.Join([]string{
		cfg.Embedding.Provider, cfg.Embedding.BaseURL, cfg.Embedding.Model,
		cfg.Embedding.APIKeyEnv, cfg.Embedding.Index,
	}, " ")
	if strings.Contains(rendered, "sk-a-key") {
		t.Fatal("a key written into the configuration must land in no field at all")
	}
}

func TestEmbeddingEnvironmentOverridesTheFile(t *testing.T) {
	root := project(t, `embedding:
  provider: openai
  base_url: https://api.openai.com/v1
  model: text-embedding-3-small
`)
	t.Setenv("FACET_EMBEDDING_BASE_URL", "http://localhost:11434/v1")
	t.Setenv("FACET_EMBEDDING_MODEL", "nomic-embed-text")

	cfg, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	e := cfg.EmbeddingSettings()
	if e.BaseURL != "http://localhost:11434/v1" || e.Model != "nomic-embed-text" {
		t.Fatalf("the environment did not win: %+v", e)
	}
	if e.Provider != "openai" {
		t.Fatalf("an unset variable must leave the file's value alone: %+v", e)
	}
}

// The index sits beside the generation manifest, sharing its prefix: both are
// derived from the records, and one .gitignore line covers either.
func TestIndexPathSitsBesideTheManifest(t *testing.T) {
	root := project(t, "")
	cfg, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	reg := cfg.All()[0]
	if filepath.Dir(cfg.IndexPath(reg)) != filepath.Dir(reg.ManifestPath()) {
		t.Fatalf("index at %s, manifest at %s", cfg.IndexPath(reg), reg.ManifestPath())
	}
	if filepath.Base(cfg.IndexPath(reg)) != RAGIndexName {
		t.Fatalf("unexpected index name %q", filepath.Base(cfg.IndexPath(reg)))
	}
}

func TestIndexPathCanBeMovedByConfigAndByEnvironment(t *testing.T) {
	root := project(t, "embedding:\n  index: .cache/facet.bin\n")
	cfg, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	reg := cfg.All()[0]
	if cfg.IndexPath(reg) != filepath.Join(root, ".cache", "facet.bin") {
		t.Fatalf("the configured path was ignored: %s", cfg.IndexPath(reg))
	}

	t.Setenv("FACET_RAG_INDEX", ".tmp/other.bin")
	if cfg.IndexPath(reg) != filepath.Join(root, ".tmp", "other.bin") {
		t.Fatalf("the environment did not win: %s", cfg.IndexPath(reg))
	}
}

// An embedding block must not be mistaken for the single-registry shorthand,
// or declaring one in a monorepo would fail with a message about records.
func TestEmbeddingCoexistsWithARegistriesBlock(t *testing.T) {
	root := project(t, `registries:
  storefront:
    records: apps/shop/facets
embedding:
  provider: local
`)
	cfg, err := LoadConfig(root)
	if err != nil {
		t.Fatalf("a monorepo must be allowed to configure embedding: %v", err)
	}
	if cfg.EmbeddingSettings().Provider != "local" {
		t.Fatal("the embedding block was lost")
	}
}

// A project declares the forge of a host the tooling could not otherwise know.
func TestForgesDeclareAHostThatCannotBeGuessed(t *testing.T) {
	root := project(t, `schema_version: "0.1"
forges:
  git.example.com: gitlab
  code.example.org: github
`)
	cfg, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Forges()["git.example.com"]; got != "gitlab" {
		t.Errorf("git.example.com = %q, want gitlab", got)
	}
	if got := cfg.Forges()["code.example.org"]; got != "github" {
		t.Errorf("code.example.org = %q, want github", got)
	}
}

// The key may be absent, and a project without it behaves exactly as before.
// That is the whole compatibility promise: every project written before this
// key existed keeps its meaning.
func TestNoForgesKeyIsNotAnError(t *testing.T) {
	cfg, err := LoadConfig(project(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Forges()) != 0 {
		t.Fatalf("a project declaring none must declare none, got %+v", cfg.Forges())
	}
	// And a nil Config answers the same way rather than panicking: the callers
	// that skipped LoadConfig are the hooks, and a hook must stay silent.
	var missing *Config
	if len(missing.Forges()) != 0 {
		t.Fatal("a nil config must answer with no declarations")
	}
}

// Only github and gitlab are supported forge values; LoadConfig rejects
// other values because the host has no CLI integration for them.
func TestAForgeNobodyCanDriveIsRefused(t *testing.T) {
	_, err := LoadConfig(project(t, "forges:\n  git.example.com: gitea\n"))
	if err == nil {
		t.Fatal("a forge this host cannot drive must be refused")
	}
	if !strings.Contains(err.Error(), "gitea") {
		t.Errorf("the message must name the value it refused: %v", err)
	}
	// And the key has to be a host, not a URL somebody pasted: the lookup is
	// against what remoteHost returns, so a value with a scheme or a path in it
	// would never match anything and would look configured.
	if _, err := LoadConfig(project(t, "forges:\n  https://git.example.com/x: gitlab\n")); err == nil {
		t.Fatal("a key that is not a bare host must be refused")
	}
}

// Mutation: drop the DriftNotice case in LoadConfig. A typo such as "of" is
// then accepted and reads as neither on nor off.
func TestTheDriftNoticeSwitchAcceptsOnlyOnAndOff(t *testing.T) {
	for value, wantOff := range map[string]bool{"off": true, "on": false} {
		cfg, err := LoadConfig(project(t, "hooks:\n  drift_notice: "+value+"\n"))
		if err != nil {
			t.Fatalf("%s: %v", value, err)
		}
		if cfg.DriftNoticeOff() != wantOff {
			t.Fatalf("%s: DriftNoticeOff = %v", value, cfg.DriftNoticeOff())
		}
	}
	if _, err := LoadConfig(project(t, "hooks:\n  drift_notice: of\n")); err == nil || !strings.Contains(err.Error(), "on or off") {
		t.Fatalf("a misspelled switch must be refused, got %v", err)
	}
	cfg, err := LoadConfig(project(t, ""))
	if err != nil || cfg.DriftNoticeOff() {
		t.Fatalf("no configuration must leave the notice on: %v", err)
	}
}

// Mutation: drop the switch case in LoadConfig. A typo such as "of" is then
// accepted and reads as neither on nor off.
func TestTheCommitGateSwitchAcceptsOnlyOnAndOff(t *testing.T) {
	for value, wantOff := range map[string]bool{"off": true, "on": false} {
		cfg, err := LoadConfig(project(t, "hooks:\n  commit_gate: "+value+"\n"))
		if err != nil {
			t.Fatalf("%s: %v", value, err)
		}
		if cfg.CommitGateOff() != wantOff {
			t.Fatalf("%s: CommitGateOff = %v", value, cfg.CommitGateOff())
		}
	}
	if _, err := LoadConfig(project(t, "hooks:\n  commit_gate: of\n")); err == nil || !strings.Contains(err.Error(), "on or off") {
		t.Fatalf("a misspelled switch must be refused, got %v", err)
	}
	cfg, err := LoadConfig(project(t, ""))
	if err != nil || cfg.CommitGateOff() {
		t.Fatalf("no configuration must leave the gate on: %v", err)
	}
}

// A declaration coexists with a registries block, like the embedding one: the
// forge is a property of the repository, and a monorepo has exactly one remote
// to declare it for.
func TestForgesCoexistWithARegistriesBlock(t *testing.T) {
	cfg, err := LoadConfig(project(t, `registries:
  storefront:
    records: apps/shop/facets
forges:
  git.example.com: gitlab
`))
	if err != nil {
		t.Fatalf("a monorepo must be allowed to declare a forge: %v", err)
	}
	if cfg.Forges()["git.example.com"] != "gitlab" {
		t.Fatal("the forges block was lost")
	}
}

func TestADRDirectoryConfiguration(t *testing.T) {
	for _, directory := range []string{"", "architecture/decisions", "../outside", "/tmp/adr", "docs/../secrets", "docs/adr/", "."} {
		t.Run(directory, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, ConfigName), []byte("adr:\n  directory: \""+directory+"\"\n"), 0644); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadConfig(root)
			valid := directory == "" || directory == "architecture/decisions"
			if !valid {
				if err == nil {
					t.Fatal("invalid directory accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := directory
			if want == "" {
				want = ADRReadDirectory
			}
			if cfg.ADRDirectory() != want {
				t.Fatalf("got %q, want %q", cfg.ADRDirectory(), want)
			}
		})
	}
	var cfg *Config
	if cfg.ADRDirectory() != ADRReadDirectory {
		t.Fatal("nil config lost default")
	}
}
