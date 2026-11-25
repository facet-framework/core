package cli

import (
	"strings"
	"testing"

	"github.com/facet-framework/core/rag"
	"github.com/spf13/cobra"
)

func TestRAGCommandsExposeDataSpecificOriginApprovals(t *testing.T) {
	for _, tc := range []struct {
		name        string
		cmd         *cobra.Command
		approval    string
		other       string
		description string
	}{
		{"index", newRAGIndexCmd(), "allow-record-egress", "allow-query-egress", "repository record and generated-document text"},
		{"query", newRAGQueryCmd(), "allow-query-egress", "allow-record-egress", "query text"},
		{"mcp", newMCPCmd("test"), "allow-query-egress", "allow-record-egress", "MCP query text"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flag := tc.cmd.Flags().Lookup(tc.approval)
			if flag == nil || flag.Value.Type() != "string" || !strings.Contains(flag.Usage, tc.description) || !strings.Contains(flag.Usage, "exact HTTPS origin") {
				t.Fatalf("%s must require a data-specific exact-origin value, got %#v", tc.approval, flag)
			}
			if tc.cmd.Flags().Lookup(tc.other) != nil {
				t.Fatalf("%s must not authorize the other data class", tc.approval)
			}
		})
	}
}

func TestCredentialSelectorComesFromInvocationOrProcess(t *testing.T) {
	t.Setenv("FACET_EMBEDDING_API_KEY_ENV", "TRUSTED_PROFILE_KEY")
	fromProcess := embeddingRemoteOptions(rag.QueryEgress, "https://api.example", "")
	if fromProcess.APIKeyEnv != "TRUSTED_PROFILE_KEY" {
		t.Fatalf("fixed process selector was not used: %+v", fromProcess)
	}
	fromFlag := embeddingRemoteOptions(rag.RecordEgress, "https://api.example", "ONE_SHOT_KEY")
	if fromFlag.APIKeyEnv != "ONE_SHOT_KEY" {
		t.Fatalf("invocation selector must override the process selector: %+v", fromFlag)
	}
}
