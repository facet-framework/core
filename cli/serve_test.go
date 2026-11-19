package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/facet-framework/core/internal/remote"
)

func TestServeKeygenScopedAndExpiring(t *testing.T) {
	cmd := newServeCmd()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetArgs([]string{"keygen", "--expires", time.Now().Add(time.Hour).UTC().Format(time.RFC3339), "--scopes", remote.RecordsScope, "--transports", "rest"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var value struct {
		Secret string     `json:"secret"`
		Config remote.Key `json:"config"`
	}
	if err := json.Unmarshal(output.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	auth := remote.Authenticator{Keys: []remote.Key{value.Config}}
	if _, err := auth.Verify(t.Context(), value.Secret, "rest"); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Verify(t.Context(), value.Secret, "mcp"); err == nil {
		t.Fatal("REST key granted MCP")
	}
}
func TestServeConfigBoundaryAndStrictFields(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	file := filepath.Join(outside, "server.json")
	if err := os.WriteFile(file, []byte(`{"unknown":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	var cfg remote.Config
	if readServeJSON(file, &cfg) == nil {
		t.Fatal("unknown config accepted")
	}
	link := filepath.Join(outside, "in-root")
	if err := os.Symlink(root, link); err != nil {
		t.Skip(err)
	}
	if !insideRoot(root, link) || insideRoot(root, file) {
		t.Fatal("operator boundary mismatch")
	}
	cmd := newServeCmd()
	cmd.SetArgs([]string{"keygen", "--expires", "2000-01-01T00:00:00Z"})
	if cmd.Execute() == nil {
		t.Fatal("expired key issued")
	}
}

func TestServeRequiresExplicitRootWithoutRepositoryDiscovery(t *testing.T) {
	cmd := newRootCmd()
	cmd.SetArgs([]string{"serve", "--config", "/unused/config", "--manifest", "/unused/manifest"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "explicit --root") {
		t.Fatal("implicit root discovery", err)
	}
}

func TestServePublicationIndexRoundTrip(t *testing.T) {
	root := t.TempDir()
	operator := t.TempDir()
	record := []byte("id: navigation/interfaces/example\nintent:\n  summary: Published example\ncontract:\n  scenarios: []\n")
	if err := os.WriteFile(filepath.Join(root, "record.yaml"), record, 0600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(record)
	pub := remote.Publication{SchemaVersion: 1, Files: []remote.PublishedFile{{Path: "record.yaml", SHA256: hex.EncodeToString(hash[:]), Kind: "features"}}}
	raw, _ := json.Marshal(pub)
	manifest := filepath.Join(operator, "publication.json")
	config := filepath.Join(operator, "server.json")
	output := filepath.Join(operator, "new-index")
	if err := os.WriteFile(manifest, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte(`{"rag":{"provider":"local","model":"facet-lexical-v1"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := newRootCmd()
	var result bytes.Buffer
	cmd.SetOut(&result)
	cmd.SetArgs([]string{"serve", "index-publication", "--root", root, "--config", config, "--manifest", manifest, "--output", output})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var metadata map[string]string
	if err := json.Unmarshal(result.Bytes(), &metadata); err != nil {
		t.Fatal(err)
	}
	if len(metadata["snapshot"]) != 64 || len(metadata["index_sha256"]) != 64 {
		t.Fatal("missing provenance", metadata)
	}
	for _, name := range []string{"records.facetvec", "provenance.json"} {
		if _, err := os.Stat(filepath.Join(output, name)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestServeOutputParentSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(outside, "link")
	if err := os.Symlink(root, link); err != nil {
		t.Skip(err)
	}
	if !insideRoot(root, filepath.Join(link, "new-index")) {
		t.Fatal("output under published root accepted through symlink")
	}
	if insideRoot(root, filepath.Join(outside, "new-index")) {
		t.Fatal("private output refused")
	}
}
