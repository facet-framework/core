//go:build !windows

// Whether a process still runs is asked with signal 0, which only Unix-like
// systems offer; the other broker cases are in plugin_broker_test.go.
package facet_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"

	"github.com/facet-framework/core/registry"
)

func TestStopAllEndsTheProcessItStarted(t *testing.T) {
	t.Setenv(helperEnv, "echo")
	marker := filepath.Join(t.TempDir(), "pid")
	t.Setenv(markerEnv, marker)
	b := facet.NewPluginBroker(pluginDir(t, []string{"confluence.pull"}, granted("confluence.pull")))

	if _, err := b.Call(context.Background(), "wiki", "confluence.pull", nil); err != nil {
		t.Fatal(err)
	}
	// The precondition: there is a process, and this is its id. A case that
	// claims something was ended has to know it was there.
	raw, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("the sidecar never ran, so there is nothing to end: %v", err)
	}
	pid, err := strconv.Atoi(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("the sidecar was already gone before StopAll: %v", err)
	}

	b.StopAll()

	// Empty StopAll's body and this goes red: without it the child outlives the
	// host that started it, reparented to init, holding every variable the host
	// had passed it.
	if err := syscall.Kill(pid, 0); err == nil {
		t.Errorf("the sidecar %d is still running after StopAll", pid)
	}
}
