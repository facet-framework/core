// Fixtures below contain example @facet: tags; this file exercises the drift
// hook rather than implementing a feature.
// @facet-ignore-file

package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const plannedRecordPath = "docs/facets/features/tasks/create/quick-add.yaml"

// recordWith builds the fixture record with one lifecycle line. driftFixtureRecord
// hard-codes 'active', which is right for its own cases and is exactly the
// field under examination here.
func recordWith(lifecycle string) string {
	return "schema_version: \"0.1\"\nid: tasks/create/quick-add\nlifecycle: " + lifecycle + "\n"
}

// hookDriftOn commits a record and its source, then edits only the source —
// the shape §12.1 is about — and returns what the Stop hook's drift half would
// say. The record is left untouched on purpose: that is the condition being
// measured, not an oversight.
func hookDriftOn(t *testing.T, record string) []string {
	t.Helper()
	root := driftFixture(t,
		map[string]string{
			plannedRecordPath:  record,
			"src/quick_add.ts": "// @facet: tasks/create/quick-add\nexport function quickAdd() {}\n",
		},
		map[string]string{
			"src/quick_add.ts": "// @facet: tasks/create/quick-add\nexport function quickAdd(input: string) {}\n",
		},
	)

	// The fixture asserts its own premise: a case claiming to measure a planned
	// record while having written something else would be green for the wrong
	// reason, and two of these cases are green precisely when nothing happens.
	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(plannedRecordPath)))
	if err != nil {
		t.Fatalf("the fixture record is unreadable: %v", err)
	}
	if string(body) != record {
		t.Fatalf("the fixture record is not what this case wrote, so it measures something else")
	}

	t.Setenv("CLAUDE_PROJECT_DIR", root)
	var in hookInput
	in.CWD = root
	// A fresh session id per run. The notice markers live on disk and outlive
	// the process, so a fixed id makes every case after the first one silent —
	// which reads exactly like the behaviour two of these cases assert.
	in.SessionID = fmt.Sprintf("planned-%s-%d", t.Name(), time.Now().UnixNano())
	in.ToolInput.FilePath = filepath.Join(root, "src/quick_add.ts")

	ctx, ok := resolveHookContext(newHookCmd(), in)
	if !ok {
		t.Fatal("want a hook context, got none")
	}
	return driftFindings(ctx, in)
}

// An active record is the ordinary case of §12.1: the code moved, the record
// has to follow, and the hook says so. Without this case the change could pass
// by suppressing every notice.
func TestDriftIsReportedForAnActiveRecord(t *testing.T) {
	got := hookDriftOn(t, recordWith("active"))

	if len(got) == 0 {
		t.Fatal("source changed against an active record and nothing was reported")
	}
	if !strings.Contains(got[0], "tasks/create/quick-add") {
		t.Errorf("the finding does not name the record: %q", got[0])
	}
}

// A planned record is the other order. The promise was written first and the
// code is arriving to meet it, so source moving while the record stands still
// is the method working — not drift.
func TestDriftIsSilentForAPlannedRecord(t *testing.T) {
	got := hookDriftOn(t, recordWith("planned"))

	if len(got) != 0 {
		t.Errorf("a planned record is being met by its code, not drifting from it; reported: %v", got)
	}
}

// A record with variants carries no top-level lifecycle — the two are exclusive
// — so the lifecycle reads empty and the record must be treated as active. A
// suppressed real drift costs more than a redundant notice, and this case pins
// that direction rather than leaving it to whichever way the zero value falls.
func TestARecordWithVariantsIsNotTakenForPlanned(t *testing.T) {
	got := hookDriftOn(t, "schema_version: \"0.1\"\nid: tasks/create/quick-add\n"+
		"variants:\n  cloud:\n    lifecycle: planned\n  onprem:\n    lifecycle: active\n")

	if len(got) == 0 {
		t.Error("a record without a top-level lifecycle must not be read as planned: real drift would go unreported")
	}
}
