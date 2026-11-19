package productplugin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == "--facet-product-command" {
		switch os.Args[2] {
		case "echo":
			raw, _ := io.ReadAll(os.Stdin)
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"args": os.Args[4:], "input": string(raw), "allowed": os.Getenv("PRODUCT_TEST_ALLOWED"), "secret": os.Getenv("PRODUCT_TEST_SECRET"), "context": os.Getenv("FACET_PRODUCT_CONTEXT")})
		case "fail":
			fmt.Fprint(os.Stderr, "product failure")
			os.Exit(23)
		}
		os.Exit(0)
	}
	if len(os.Args) > 2 && os.Args[1] == "--facet-product-service" {
		switch os.Args[2] {
		case "echo":
			var r Request
			_ = json.NewDecoder(os.Stdin).Decode(&r)
			_ = json.NewEncoder(os.Stdout).Encode(Response{Protocol: Protocol, Result: r.Payload})
		case "bad":
			fmt.Print(`{"protocol":2,"result":{}}`)
		case "empty":
		case "large":
			fmt.Print(strings.Repeat("x", (4<<20)+1))
		case "sleep":
			time.Sleep(time.Minute)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func fixture(t *testing.T) (Store, string, Manifest) {
	t.Helper()
	root := t.TempDir()
	staged := filepath.Join(root, "stage")
	if err := os.Mkdir(staged, 0700); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	name := "product"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	raw, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if err = os.WriteFile(filepath.Join(staged, name), raw, 0700); err != nil {
		t.Fatal(err)
	}
	m := Manifest{Kind: "product", ID: "fixture", Version: "1.0.0", Protocol: Protocol, Platform: runtime.GOOS + "-" + runtime.GOARCH, Executable: name, Commands: []string{"echo", "fail"}, Services: []string{"echo", "bad", "empty", "large", "sleep"}, Env: []string{"PRODUCT_TEST_ALLOWED"}, Files: map[string]string{name: hex.EncodeToString(sum[:])}}
	writeManifest(t, staged, m)
	return Store{Dir: filepath.Join(root, "installed")}, staged, m
}
func writeManifest(t *testing.T, dir string, m Manifest) {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestInstallRunAndRemove(t *testing.T) {
	store, stage, _ := fixture(t)
	ctx := context.Background()
	if _, err := store.Install(ctx, stage, false, false); err == nil {
		t.Fatal("missing native grant accepted")
	}
	if _, err := store.Install(ctx, stage, true, false); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Install(ctx, stage, true, false); err == nil {
		t.Fatal("implicit replacement accepted")
	}
	t.Setenv("PRODUCT_TEST_ALLOWED", "yes")
	t.Setenv("PRODUCT_TEST_SECRET", "hidden")
	args := []string{"a b", "$(touch nope)", "--flag", "ümlaut"}
	var out, stderr strings.Builder
	inv := Invocation{Root: t.TempDir(), SourceView: "declared", HostExecutable: "host"}
	if err := store.Run(ctx, "fixture", "echo", args, inv, Streams{strings.NewReader("stdin"), &out, &stderr}); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Args                            []string
		Input, Allowed, Secret, Context string
	}
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Args, args) || got.Input != "stdin" || got.Allowed != "yes" || got.Secret != "" {
		t.Fatalf("streams or environment changed: %+v", got)
	}
	var received Invocation
	if err := json.Unmarshal([]byte(got.Context), &received); err != nil || !reflect.DeepEqual(received, inv) {
		t.Fatalf("context lost: %s", got.Context)
	}
	err := store.Run(ctx, "fixture", "fail", nil, inv, Streams{nil, io.Discard, &stderr})
	var exit *ExitError
	if !errors.As(err, &exit) || exit.Code != 23 || stderr.String() != "product failure" {
		t.Fatalf("exit contract: %v %s", err, stderr.String())
	}
	if err = store.Run(ctx, "fixture", "unknown", nil, inv, Streams{}); err == nil {
		t.Fatal("undeclared command accepted")
	}
	list, err := store.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %v", list, err)
	}
	if err = store.Remove(ctx, "fixture"); err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.Resolve("fixture"); !os.IsNotExist(err) {
		t.Fatalf("removed product still resolves: %v", err)
	}
}
func TestServiceProtocolAndBounds(t *testing.T) {
	store, stage, _ := fixture(t)
	ctx := context.Background()
	if _, err := store.Install(ctx, stage, true, false); err != nil {
		t.Fatal(err)
	}
	inv := Invocation{Root: t.TempDir()}
	result, err := store.Call(ctx, "fixture", "echo", json.RawMessage(`{"value":42}`), inv)
	if err != nil || string(result) != `{"value":42}` {
		t.Fatalf("round trip: %s %v", result, err)
	}
	for _, service := range []string{"bad", "empty", "large", "unknown"} {
		t.Run(service, func(t *testing.T) {
			if _, err := store.Call(ctx, "fixture", service, json.RawMessage(`{}`), inv); err == nil {
				t.Fatal("invalid service response accepted")
			}
		})
	}
	// JSON encoding expands HTML-sensitive characters; bound the wire request too.
	expanded := json.RawMessage(`"` + strings.Repeat("<", 1<<20) + `"`)
	if _, err = store.Call(ctx, "fixture", "echo", expanded, inv); err == nil || !strings.Contains(err.Error(), "encoded product request") {
		t.Fatalf("encoded request limit: %v", err)
	}
	deadline, cancel := context.WithTimeout(ctx, 80*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err = store.Call(deadline, "fixture", "sleep", json.RawMessage(`{}`), inv); err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("service cancellation: %v", err)
	}
}
func TestPayloadAndManifestRemainBoundToGrant(t *testing.T) {
	store, stage, m := fixture(t)
	ctx := context.Background()
	if _, err := store.Install(ctx, stage, true, false); err != nil {
		t.Fatal(err)
	}
	_, dir, err := store.Resolve(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, m.Executable))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, m.Executable), []byte("tampered"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.Resolve(m.ID); err == nil {
		t.Fatal("tampered payload accepted")
	}
	if err = os.WriteFile(filepath.Join(dir, m.Executable), raw, 0700); err != nil {
		t.Fatal(err)
	}
	m.Env = append(m.Env, "PRODUCT_TEST_SECRET")
	writeManifest(t, dir, m)
	if _, _, err = store.Resolve(m.ID); err == nil {
		t.Fatal("changed manifest accepted with previous grant")
	}
}
func TestRejectIncompatibleAndUnsafePackages(t *testing.T) {
	_, stage, base := fixture(t)
	cases := map[string]func(*Manifest){"protocol": func(m *Manifest) { m.Protocol++ }, "platform": func(m *Manifest) { m.Platform = "unknown" }, "escape": func(m *Manifest) {
		m.Executable = "../escape"
		m.Files = map[string]string{"../escape": strings.Repeat("0", 64)}
	}, "duplicate-command": func(m *Manifest) { m.Commands = []string{"echo", "echo"} }, "reserved-env": func(m *Manifest) { m.Env = []string{"FACET_PRODUCT_CONTEXT"} }, "version": func(m *Manifest) { m.Version = "../1" }}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			m := base
			change(&m)
			writeManifest(t, stage, m)
			if _, _, err := Verify(stage); err == nil {
				t.Fatal("unsafe manifest accepted")
			}
		})
	}
	writeManifest(t, stage, base)
	extra := filepath.Join(stage, "extra")
	if err := os.WriteFile(extra, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Verify(stage); err == nil {
		t.Fatal("undeclared file accepted")
	}
	if err := os.Remove(extra); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(base.Executable, extra); err != nil {
			t.Fatal(err)
		}
		if _, _, err := Verify(stage); err == nil {
			t.Fatal("symlink accepted")
		}
	}
}

func TestReplacementAndConflicts(t *testing.T) {
	store, stage, m := fixture(t)
	ctx := context.Background()
	original, err := store.Install(ctx, stage, true, false)
	if err != nil {
		t.Fatal(err)
	}
	changed := m
	changed.Version = "1.1.0"
	writeManifest(t, stage, changed)
	if _, err = store.Install(ctx, stage, false, true); err == nil {
		t.Fatal("replacement reused native grant")
	}
	current, _, err := store.Resolve(m.ID)
	if err != nil || current.ManifestSHA256 != original.ManifestSHA256 {
		t.Fatal("refused replacement changed selected version")
	}
	if _, err = store.Install(ctx, stage, true, true); err != nil {
		t.Fatal(err)
	}
	current, _, err = store.Resolve(m.ID)
	if err != nil || current.Manifest.Version != "1.1.0" {
		t.Fatal("replacement not selected")
	}
	changed.ID = "another"
	writeManifest(t, stage, changed)
	if _, err = store.Install(ctx, stage, true, false); err == nil {
		t.Fatal("duplicate command installed")
	}
	_, dir, err := store.Resolve(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, m.Executable), []byte("broken"), 0700); err != nil {
		t.Fatal(err)
	}
	changed.Commands = []string{"fresh"}
	writeManifest(t, stage, changed)
	if _, err = store.Install(ctx, stage, true, false); err == nil {
		t.Fatal("corrupt installed declarations ignored")
	}
	if err = store.Remove(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(store.Dir, "packages"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("uninstall left generations: %v %v", entries, err)
	}
}

func TestConcurrentInstallCannotClaimSameCommand(t *testing.T) {
	store, first, one := fixture(t)
	_, second, two := fixture(t)
	two.ID = "second"
	writeManifest(t, second, two)
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, stage := range []string{first, second} {
		go func(stage string) {
			<-start
			_, err := store.Install(context.Background(), stage, true, false)
			results <- err
		}(stage)
	}
	close(start)
	successes := 0
	for i := 0; i < 2; i++ {
		if err := <-results; err == nil {
			successes++
		} else if !strings.Contains(err.Error(), "conflicts") {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatalf("%d products claimed %s", successes, one.Commands[0])
	}
	list, err := store.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("ambiguous installed commands: %v %v", list, err)
	}
}

func TestHostRequirementsAreExplicit(t *testing.T) {
	raw := []byte(`{"kind":"product","id":"fixture","version":"1.0.0","protocol":1,"platform":"` + runtime.GOOS + `-` + runtime.GOARCH + `","executable":"run","commands":["fixture"],"files":{"run":"` + strings.Repeat("0", 64) + `"},"requires":["product-comparisons-v1","product-mcp-v1"]}`)
	if _, err := Decode(raw); err != nil {
		t.Fatal(err)
	}
	unsupported := bytes.Replace(raw, []byte("product-mcp-v1"), []byte("future-host-capability"), 1)
	if _, err := Decode(unsupported); err == nil {
		t.Fatal("unsupported host requirement accepted")
	}
}
