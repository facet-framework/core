// Hook fixtures are illustrative source, not claims about this test file.
// @facet-ignore-file

package cli

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/facet-framework/core/registry"
)

func TestConcurrentNoticesClaimDeliveryOnce(t *testing.T) {
	t.Setenv("CLAUDE_PLUGIN_DATA", t.TempDir())
	start := make(chan struct{})
	var delivered atomic.Int32
	var workers sync.WaitGroup
	for i := 0; i < 128; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			if noticeIsNew("same-session", "drift:billing/subscriptions/free-trial") {
				delivered.Add(1)
			}
		}()
	}
	close(start)
	workers.Wait()
	if got := delivered.Load(); got != 1 {
		t.Fatalf("delivered %d times, want once", got)
	}
	if noticeIsNew("same-session", "drift:billing/subscriptions/free-trial") {
		t.Fatal("same session repeated its notice")
	}
	if !noticeIsNew("another-session", "drift:billing/subscriptions/free-trial") {
		t.Fatal("a new session lost its notice")
	}
}

func TestShellConcurrentNoticesClaimDeliveryOnce(t *testing.T) {
	plugin, _ := filepath.Abs(cliModuleRoot(t))
	cmd := exec.Command("sh", "-c", `. "$CLAUDE_PLUGIN_ROOT/scripts/hook-lib.sh"
i=0
while [ "$i" -lt 64 ]; do
  (if facet_notice_is_new 'drift:billing/subscriptions/free-trial' same-session; then printf 'new\n'; fi) &
  i=$((i + 1))
done
wait`)
	cmd.Env = append(cmd.Environ(), "CLAUDE_PLUGIN_ROOT="+plugin, "CLAUDE_PLUGIN_DATA="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("shell notice: %v %s", err, out)
	}
	if got := strings.Count(string(out), "new\n"); got != 1 {
		t.Fatalf("shell delivered %d times: %s", got, out)
	}
}

func TestFourThousandRecordHookRosterFitsBudget(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 4000; i++ {
		id := fmt.Sprintf("billing/subscriptions/trial-%04d", i)
		write(t, filepath.Join(root, "docs/facets/features", id+".yaml"), "schema_version: '0.1'\nid: "+id+"\nlifecycle: active\n")
	}
	for name, index := range map[string]string{"binary fast path": largeRegistryIndex(newHookContext(root)), "shell": runFallbackIndex(t, root)} {
		if len(index) > facet.DefaultIndexBytes {
			t.Fatalf("%s index: %d bytes, budget %d", name, len(index), facet.DefaultIndexBytes)
		}
		if !strings.Contains(index, "all 4000 record(s)") || !strings.Contains(index, "further record(s) are not listed here") {
			t.Fatalf("%s lost total/truncation guidance: %s", name, tail(index))
		}
		for _, line := range strings.Split(index, "\n") {
			if strings.HasPrefix(line, "- ") && (len(line) != len("- billing/subscriptions/trial-0000") || !strings.HasPrefix(line, "- billing/subscriptions/trial-")) {
				t.Fatalf("%s partial roster line: %q", name, line)
			}
		}
		t.Logf("%s: %d bytes, %d complete roster lines", name, len(index), len(rosterLines(index)))
	}
}

func TestShellRosterRemainsBoundedWithoutTemporaryStorage(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 700; i++ {
		id := fmt.Sprintf("billing/subscriptions/trial-%04d", i)
		write(t, filepath.Join(root, "docs/facets/features", id+".yaml"), "id: "+id+"\n")
	}
	plugin, _ := filepath.Abs(cliModuleRoot(t))
	cmd := exec.Command("sh", "-c", `. "$CLAUDE_PLUGIN_ROOT/scripts/hook-lib.sh"; mktemp() { return 1; }; facet_record_index "$RECORD_ROOT"`)
	cmd.Env = append(cmd.Environ(), "CLAUDE_PLUGIN_ROOT="+plugin, "FACET_NO_BINARY=1", "RECORD_ROOT="+root, "FACET_INDEX_BYTES=1024")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("shell roster: %v %s", err, out)
	}
	if len(out) > 1024 || !strings.Contains(string(out), "temporary storage unavailable") || len(rosterLines(string(out))) != 0 {
		t.Fatalf("unbounded or unexplained roster fallback: %d bytes %s", len(out), tail(string(out)))
	}
}
