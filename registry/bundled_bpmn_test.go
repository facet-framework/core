// White-box tests for the BPMN plugin's bundled entry: its table row agrees
// with the manifest a reviewer reads, its sidecar gets no environment, and a
// measured sum for one bundled sidecar never vouches for another.
package facet

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The bundled BPMN entry honors the reviewed, versioned producer contract.
// The snapshot is the reviewed producer contract; these tests consume it.
func TestTheBundledBpmnTableAgreesWithPluginJSON(t *testing.T) {
	raw := BundledPluginContract(t, "bpmn")
	var real struct {
		Tools   []string   `json:"tools"`
		Reads   []ReadDecl `json:"reads"`
		Sidecar struct {
			Bin string   `json:"bin"`
			Env []string `json:"env"`
		} `json:"sidecar"`
	}
	if err := json.Unmarshal(raw, &real); err != nil {
		t.Fatal(err)
	}
	if len(real.Tools) == 0 || real.Sidecar.Bin == "" {
		t.Fatalf("fixture precondition: plugin.json names tools %v and sidecar %q", real.Tools, real.Sidecar.Bin)
	}
	m, ok := bundledManifest("bpmn")
	if !ok {
		t.Fatal("no bundled bpmn manifest")
	}
	if strings.Join(m.Tools, ",") != strings.Join(real.Tools, ",") {
		t.Errorf("tools: table %v, plugin.json %v", m.Tools, real.Tools)
	}
	// The right is the directory AND its extensions: compared through what
	// the host actually grants (ReadExtsFor), not through the entry's form.
	for _, reads := range [][]ReadDecl{m.Reads, real.Reads} {
		if len(reads) != 1 || reads[0].Dir != "docs/bpmn" {
			t.Errorf("reads = %v, want exactly one entry for docs/bpmn", reads)
			continue
		}
		exts, err := ReadExtsFor(reads, "docs/bpmn")
		if err != nil || strings.Join(exts, ",") != ".bpmn" {
			t.Errorf("reads %v grants %v (%v), want exactly .bpmn", reads, exts, err)
		}
	}
	if m.Sidecar == nil || m.Sidecar.Bin != real.Sidecar.Bin {
		t.Errorf("sidecar bin: table %+v, plugin.json %q", m.Sidecar, real.Sidecar.Bin)
	}
	// The sidecar is a pure transformation: no variable reaches it, and the
	// table and the file agree on that too.
	if len(m.Sidecar.Env) != 0 || len(real.Sidecar.Env) != 0 {
		t.Errorf("the bpmn sidecar asks for environment: table %v, plugin.json %v", m.Sidecar.Env, real.Sidecar.Env)
	}
	for _, tool := range m.Tools {
		if strings.HasPrefix(tool, "repo.") && tool != "repo.list" && tool != "repo.read" {
			t.Errorf("the bpmn plugin declares %s, a tool that writes", tool)
		}
	}
}

// A sum measured for one bundled sidecar never vouches for another. With
// only confluence measured, the bpmn sidecar is refused as unmeasured —
// with one sum per platform, any second sidecar would have been checked
// against confluence's.
func TestOneSidecarsSumDoesNotVouchForAnother(t *testing.T) {
	fixtureDir := t.TempDir()
	_, confluenceSum := installBundledFixture(t, fixtureDir, "confluence")
	installBundledFixture(t, fixtureDir, "bpmn")
	fakeExe := filepath.Join(fixtureDir, "facet")
	if err := os.WriteFile(fakeExe, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	restoreExe, restoreSums := bundledSidecarExecutable, bundledSidecarSums
	bundledSidecarExecutable = func() (string, error) { return fakeExe, nil }
	bundledSidecarSums = map[string]string{"confluence/" + currentPlatform(): confluenceSum}
	t.Cleanup(func() { bundledSidecarExecutable, bundledSidecarSums = restoreExe, restoreSums })

	b := NewPluginBroker(t.TempDir())
	defer b.StopAll()

	_, err := b.Call(context.Background(), "bpmn", "bpmn.diagram", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("the bpmn sidecar ran without a sum of its own")
	}
	if errors.Is(err, ErrSidecarChecksum) || !strings.Contains(err.Error(), "no measured checksum for its bundled sidecar bpmn") {
		t.Fatalf("want the unmeasured refusal naming bpmn, got: %v", err)
	}
}

// The env helper returns its own environment. Pass FACET_SIDECAR_HELPER to start it;
// assert that the child receives the helper variable and no host secret.
func TestTheBundledBpmnSidecarGetsNoEnvironment(t *testing.T) {
	fixtureDir := t.TempDir()
	_, sum := installBundledFixture(t, fixtureDir, "bpmn")
	fakeExe := filepath.Join(fixtureDir, "facet")
	if err := os.WriteFile(fakeExe, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	restoreExe, restoreSums := bundledSidecarExecutable, bundledSidecarSums
	bundledSidecarExecutable = func() (string, error) { return fakeExe, nil }
	bundledSidecarSums = map[string]string{"bpmn/" + currentPlatform(): sum}
	t.Cleanup(func() { bundledSidecarExecutable, bundledSidecarSums = restoreExe, restoreSums })

	// Precondition: the real manifest asks for no variable at all; the one
	// name added below is the test harness's, not a right of the plugin.
	original := bundledManifests["bpmn"]
	if len(original.Sidecar.Env) != 0 {
		t.Fatalf("bundledManifests[\"bpmn\"].Sidecar.Env = %v, want none", original.Sidecar.Env)
	}
	widened := original
	widened.Sidecar = &ManifestSidecar{Bin: original.Sidecar.Bin, Env: []string{bundledTestHelperEnv}}
	bundledManifests["bpmn"] = widened
	t.Cleanup(func() { bundledManifests["bpmn"] = original })

	t.Setenv(bundledTestHelperEnv, "env")
	const secret = "FACET_BPMN_TEST_SECRET"
	t.Setenv(secret, "must-not-arrive")

	b := NewPluginBroker(t.TempDir())
	defer b.StopAll()
	rep, err := b.Call(context.Background(), "bpmn", "bpmn.diagram", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("the env helper did not answer: %v", err)
	}
	var out struct {
		Env []string `json:"env"`
	}
	if err := json.Unmarshal(rep.Result, &out); err != nil {
		t.Fatalf("the helper's answer is not the env dump: %v (%s)", err, rep.Result)
	}
	// Precondition: the dump is real — the harness variable it needed is in it.
	if !contains(out.Env, bundledTestHelperEnv+"=env") {
		t.Fatalf("the child's environment lacks %s, so this is not its dump: %v", bundledTestHelperEnv, out.Env)
	}
	for _, kv := range out.Env {
		name, _, _ := strings.Cut(kv, "=")
		if name != bundledTestHelperEnv {
			t.Errorf("the bpmn sidecar received %s, which its manifest never named", name)
		}
	}
}
