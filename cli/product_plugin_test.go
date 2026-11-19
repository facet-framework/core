package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/facet-framework/core/pkg/productplugin"
)

func TestProductCLIInstallDispatchAndRemove(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture; native runner is tested separately")
	}
	t.Setenv("FACET_PRODUCTS_DIR", filepath.Join(t.TempDir(), "products"))
	stage := t.TempDir()
	body := []byte("#!/bin/sh\nprintf '%s\\n' \"$FACET_PRODUCT_CONTEXT\" \"$@\"\n")
	sum := sha256.Sum256(body)
	manifest := productplugin.Manifest{Kind: "product", ID: "fixture-product", Version: "1.0.0", Protocol: 1, Platform: runtime.GOOS + "-" + runtime.GOARCH, Executable: "run", Commands: []string{"fixture-product"}, Files: map[string]string{"run": hex.EncodeToString(sum[:])}}
	raw, _ := json.Marshal(manifest)
	for name, data := range map[string][]byte{"plugin.json": raw, "run": body} {
		if err := os.WriteFile(filepath.Join(stage, name), data, 0700); err != nil {
			t.Fatal(err)
		}
	}
	execute := func(args ...string) (string, error) {
		t.Helper()
		cmd := newRootCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs(args)
		err := cmd.Execute()
		return out.String(), err
	}
	if _, err := execute("plugin", "product", "add", stage); err == nil {
		t.Fatal("install without grant succeeded")
	}
	if _, err := execute("plugin", "product", "add", stage, "--grant-native"); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	out, err := execute("fixture-product", "--root", root, "a b", "$(literal)", "--help")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var inv productplugin.Invocation
	if len(lines) < 6 {
		t.Fatalf("missing forwarded arguments: %q", out)
	}
	if err = json.Unmarshal([]byte(lines[0]), &inv); err != nil {
		t.Fatal(err)
	}
	if inv.Root != root || inv.HostExecutable == "" {
		t.Fatalf("wrong host selection: %+v", inv)
	}
	if strings.Join(lines[1:], "|") != "--facet-product-command|fixture-product|--|a b|$(literal)|--help" {
		t.Fatalf("arguments changed: %q", out)
	}

	viewFixture := sourceViewIntegrationSetup(t)
	t.Setenv("FACET_SOURCE_VIEW", "selected")
	out, err = execute("--root", viewFixture.registry, "fixture-product")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(strings.Split(out, "\n")[0]), &inv); err != nil {
		t.Fatal(err)
	}
	if inv.SourceView != "selected" || inv.Root != viewFixture.registry {
		t.Fatalf("environment view lost: %+v", inv)
	}
	t.Setenv("FACET_SOURCE_VIEW", "")
	if _, err := execute("plugin", "product", "remove", "fixture-product"); err != nil {
		t.Fatal(err)
	}
	if _, err := execute("fixture-product"); err == nil {
		t.Fatal("uninstalled command is still registered")
	}
	// Reusing Core's name must fail before installation and leave Core intact.
	manifest.Commands = []string{"drift"}
	raw, _ = json.Marshal(manifest)
	if err = os.WriteFile(filepath.Join(stage, "plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := execute("plugin", "product", "add", stage, "--grant-native"); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("Core collision accepted: %v", err)
	}
}
