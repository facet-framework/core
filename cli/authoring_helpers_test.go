package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// glossaryCLIProject builds a project with one active record and returns its
// root.
func glossaryCLIProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "docs/facets/features/logistics/booking/release.yaml"),
		`schema_version: "0.1"
id: logistics/booking/release
lifecycle: active
intent:
  summary: |
    Releases a booking.
contract:
  scenarios:
    - id: release-marks-the-booking
      statement: |
        Given a booking
        When it is released
        Then it is marked released
lineage:
  owner:
    team: logistics
  created_at: "2026-09-16"
`)
	return root
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// callAuthoringToolsConcurrently calls one write tool with every input at once
// and fails on any refusal.
func callAuthoringToolsConcurrently(t *testing.T, client *mcp.ClientSession, name string, inputs []any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := make(chan struct{})
	results := make(chan error, len(inputs))
	for _, input := range inputs {
		arguments, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			<-start
			result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: json.RawMessage(arguments)})
			if err == nil && result.IsError {
				err = fmt.Errorf("%s refused a concurrent mutation: %s", name, firstText(result))
			}
			results <- err
		}()
	}
	close(start)
	for range inputs {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
}
