// @facet-ignore-file
package facet

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Named mutation: remove the transaction lock in CheckTests. The second list
// can read the first snapshot, then reintroduce a path removed by its sibling.
func TestBaselineUpdatesSerializeWholeTransaction(t *testing.T) {
	cfg, reg := discoveryFixture(t, "printf 'tests/a.go\\n'")
	os.MkdirAll(reg.RecordsDir, 0755)
	path := filepath.Join(reg.RecordsDir, "test-baseline.yaml")
	os.WriteFile(path, []byte("runners:\n  first: [tests/a.go, tests/stale-first.go]\n  second: [tests/b.go, tests/stale-second.go]\n"), 0600)
	cfg.Runners = map[string]Runner{
		"first":  {Command: "true", List: `touch first-entered; while [ ! -f release-first ]; do sleep 0.01; done; printf 'tests/a.go\n'`},
		"second": {Command: "true", List: `touch second-entered; printf 'tests/b.go\n'`},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	completed := make(chan error, 2)
	go func() { _, err := CheckTests(ctx, cfg, reg, "first", true); completed <- err }()
	deadline := time.After(time.Second)
	for {
		if _, err := os.Stat(filepath.Join(reg.Root, "first-entered")); err == nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("first runner never entered list")
		case <-time.After(5 * time.Millisecond):
		}
	}
	go func() { _, err := CheckTests(ctx, cfg, reg, "second", true); completed <- err }()
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(reg.Root, "second-entered")); !os.IsNotExist(err) {
		t.Error("second transaction entered discovery before first finished")
	}
	os.WriteFile(filepath.Join(reg.Root, "release-first"), nil, 0644)
	for range 2 {
		if err := <-completed; err != nil {
			t.Fatal(err)
		}
	}
	baseline, err := readTestBaseline(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(baseline.Runners["first"]) != 1 || len(baseline.Runners["second"]) != 1 {
		t.Fatalf("removed entries reintroduced: %+v", baseline)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("baseline permissions changed")
	}
	leftovers, _ := filepath.Glob(filepath.Join(reg.RecordsDir, ".facet-test-baseline-*"))
	if len(leftovers) > 0 {
		t.Fatalf("temporary writes leaked: %v", leftovers)
	}
}

// Named mutation: remove configureTestProcess and WaitDelay. A descendant
// holding stdout delays CommandContext return until its two-second sleep ends.
func TestDiscoveryCancellationKillsDescendantPipeHolders(t *testing.T) {
	cfg, reg := discoveryFixture(t, "sleep 2 & wait")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	report, err := CheckTests(ctx, cfg, reg, "", false)
	if err != nil || !report.Failed() {
		t.Fatalf("cancelled discovery passed: %+v %v", report, err)
	}
	if elapsed := time.Since(started); elapsed > 750*time.Millisecond {
		t.Fatalf("descendant delayed cancellation: %s", elapsed)
	}
}
