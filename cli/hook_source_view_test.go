package cli

import (
	"github.com/facet-framework/core/registry"
	"github.com/spf13/cobra"
	"testing"
)

func TestHookContextKeepsExplicitFrozenSourceView(t *testing.T) {
	f := sourceViewIntegrationSetup(t)
	probe := &cobra.Command{Use: "hook-view-probe", RunE: func(cmd *cobra.Command, _ []string) error {
		set := RegistrySetForCommand(cmd)
		ctx, ok := resolveHookContext(cmd, hookInput{CWD: f.registry})
		if !ok {
			t.Fatal("adopted registry not resolved")
		}
		reg := ctx.primary()
		if reg.SourceView != set.Primary().SourceView {
			t.Fatal("hook lost invocation snapshot")
		}
		anchors, err := facet.ScanAnchors(reg.Root, reg.ScanOptions())
		if err != nil {
			return err
		}
		found := false
		for _, anchor := range anchors {
			if anchor.ID == "sample/core/backend-old" {
				t.Fatal("hook merged unselected backend")
			}
			if anchor.ID == "sample/core/backend-new" {
				found = true
			}
		}
		if !found {
			t.Fatal("hook did not scan selected backend")
		}
		return nil
	}}
	cmd := NewCommand(Options{Commands: []*cobra.Command{probe}})
	cmd.SetArgs([]string{"--root", f.registry, "--source-view", "selected", "hook-view-probe"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
}
