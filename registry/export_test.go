package facet

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Capture the original package location before tests change the working directory.
// runtime.Caller identifies the source directory in ordinary builds; the initial
// working directory supports test binaries compiled with trimmed source paths.
var sourceTestFile, sourceTestWorkingDirectory, sourceTestWorkingDirectoryError = func() (string, string, error) {
	_, file, _, _ := runtime.Caller(0)
	cwd, err := os.Getwd()
	return file, cwd, err
}()

// PackageTestPath locates test resources beside this package's source, regardless
// of the package's depth in the module.
// It is exported only in test builds for external-package contract tests.
func PackageTestPath(t testing.TB, parts ...string) string {
	t.Helper()
	dir := filepath.Dir(sourceTestFile)
	if !filepath.IsAbs(sourceTestFile) {
		if sourceTestWorkingDirectoryError != nil {
			t.Fatalf("locating the test package: %v", sourceTestWorkingDirectoryError)
		}
		dir = sourceTestWorkingDirectory
	}
	return filepath.Join(append([]string{dir}, parts...)...)
}

// SourceTestPath locates a shipped resource from the nearest Go module root.
// Tests must not assume a fixed package depth or silently skip missing resources.
func SourceTestPath(t testing.TB, parts ...string) string {
	t.Helper()
	for dir := PackageTestPath(t); ; dir = filepath.Dir(dir) {
		info, err := os.Stat(filepath.Join(dir, "go.mod"))
		if err == nil && !info.IsDir() {
			return filepath.Join(append([]string{dir}, parts...)...)
		}
		if err != nil && !os.IsNotExist(err) {
			t.Fatalf("locating the source module at %s: %v", dir, err)
		}
		if parent := filepath.Dir(dir); parent == dir {
			t.Fatalf("no go.mod above test package %s", PackageTestPath(t))
		}
	}
}

// BundledPluginContract reads the reviewed producer contract snapshot these
// consumer tests read; the Core consumer tests always run on their own.
func BundledPluginContract(t testing.TB, id string) []byte {
	t.Helper()
	data, err := os.ReadFile(PackageTestPath(t, "testdata", "plugin-contracts", id+".json"))
	if err != nil {
		t.Fatalf("reading bundled plugin contract %q: %v", id, err)
	}
	return data
}

func TestSourceTestPathsDoNotDependOnWorkingDirectory(t *testing.T) {
	schema := SourceTestPath(t, "schema", "facet.schema.json")
	contract := PackageTestPath(t, "testdata", "plugin-contracts", "adr.json")
	t.Chdir(t.TempDir())
	if got := SourceTestPath(t, "schema", "facet.schema.json"); got != schema {
		t.Fatalf("source path changed with cwd: got %s, want %s", got, schema)
	}
	if got := PackageTestPath(t, "testdata", "plugin-contracts", "adr.json"); got != contract {
		t.Fatalf("package path changed with cwd: got %s, want %s", got, contract)
	}
	if _, err := os.ReadFile(schema); err != nil {
		t.Fatalf("shipped schema unavailable from another working directory: %v", err)
	}
	if len(BundledPluginContract(t, "adr")) == 0 {
		t.Fatal("bundled contract was empty")
	}
}

// The benchmark input is a public synthetic record, not a private project file.
// Keep it schema-valid so performance measurements exercise a representative
// record instead of a parser-only placeholder or an invalid-input shortcut.
func TestRepresentativeRecordFixtureConformsToShippedSchema(t *testing.T) {
	record, err := LoadRecord(PackageTestPath(t, "testdata", "representative-record.yaml"), "features")
	if err != nil {
		t.Fatal(err)
	}
	schema, err := compileSchema(SourceTestPath(t, "schema", "facet.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(record.Fields); err != nil {
		t.Fatalf("representative benchmark record does not match the shipped schema: %v", err)
	}
}
