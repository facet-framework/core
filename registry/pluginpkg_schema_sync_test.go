package facet

import (
	"os"
	"testing"
)

// go:embed cannot reach outside its package. Keep the embedded manifest schema
// identical to the root copy so the two validators enforce the same contract.
func TestEmbeddedPluginManifestSchemaMatchesTheRepoRootCopy(t *testing.T) {
	embedded, err := pluginManifestSchemaFS.ReadFile(pluginManifestSchemaEmbedPath)
	if err != nil {
		t.Fatalf("reading the embedded schema: %v", err)
	}

	rootCopy := SourceTestPath(t, "schema", "plugin-manifest.schema.json")
	onDisk, err := os.ReadFile(rootCopy)
	if err != nil {
		t.Fatalf("reading %s: %v", rootCopy, err)
	}

	if string(embedded) != string(onDisk) {
		t.Fatalf("embedded plugin-manifest.schema.json has drifted from schema/plugin-manifest.schema.json — copy the root file over the embedded one")
	}
}
