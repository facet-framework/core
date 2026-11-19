// @facet: navigation/interfaces/product-services
// @facet-role: test
package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/facet-framework/core/pkg/productplugin"
	"github.com/facet-framework/core/registry"
)

func TestProductWarningAndSessionDispatch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture; native transport cross-compiles separately")
	}
	t.Setenv("FACET_PRODUCTS_DIR", t.TempDir())
	capture := filepath.Join(t.TempDir(), "request.json")
	t.Setenv("PRODUCT_TEST_INPUT", capture)
	stage := t.TempDir()
	script := []byte("#!/bin/sh\ncat > \"$PRODUCT_TEST_INPUT\"\ncase \"$2\" in\n observe-warnings) printf '%s' '{\"protocol\":1,\"result\":{\"notes\":[\" first\",\" second\"]}}';;\n observe-session) printf '%s' '{\"protocol\":1,\"result\":{}}';;\nesac\n")
	digest := sha256.Sum256(script)
	manifest := productplugin.Manifest{Kind: "product", ID: "observer", Version: "1.0.0", Protocol: 1, Platform: runtime.GOOS + "-" + runtime.GOARCH, Executable: "run", Commands: []string{"observer"}, Services: []string{"observe-warnings", "observe-session"}, Env: []string{"PATH", "PRODUCT_TEST_INPUT"}, Files: map[string]string{"run": hex.EncodeToString(digest[:])}}
	raw, _ := json.Marshal(manifest)
	for name, body := range map[string][]byte{"plugin.json": raw, "run": script} {
		if err := os.WriteFile(filepath.Join(stage, name), body, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := productStore().Install(context.Background(), stage, true, false); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	regs := []facet.Registry{{Root: root, Name: "a"}}
	drift := []facet.Drift{{ID: "demo/thing/first", Registry: "a"}, {ID: "demo/thing/second", Registry: "a"}}
	notes := recordProductWarnings(root, regs, drift, "baseline")
	if len(notes) != 2 || notes[0] != " first" || notes[1] != " second" {
		t.Fatalf("product notes: %v", notes)
	}
	read := func() json.RawMessage {
		t.Helper()
		raw, err := os.ReadFile(capture)
		if err != nil {
			t.Fatal(err)
		}
		var request productplugin.Request
		if err = json.Unmarshal(raw, &request); err != nil {
			t.Fatal(err)
		}
		return request.Payload
	}
	var warning struct {
		Drift    []facet.Drift
		Baseline string
	}
	if err := json.Unmarshal(read(), &warning); err != nil || len(warning.Drift) != 2 || warning.Baseline != "baseline" {
		t.Fatalf("batched warning request: %+v %v", warning, err)
	}
	recordProductSession(hookContext{root: root, registries: regs}, "session", "session_end")
	var boundary struct {
		Registries    []string
		Session, Kind string
	}
	if err := json.Unmarshal(read(), &boundary); err != nil || len(boundary.Registries) != 1 || boundary.Registries[0] != "a" || boundary.Session != "session" || boundary.Kind != "session_end" {
		t.Fatalf("session request: %+v %v", boundary, err)
	}
}
