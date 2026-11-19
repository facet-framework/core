package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/facet-framework/core/registry"
	"github.com/spf13/cobra"
)

func newDocsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "docs",
		Short: "Generate documentation sites and track their source freshness",
		Long: `Build audience-guided documentation and keep it connected to its evidence.

'site inventory' collects records, anchors and Knowledge gaps.
'site plan' validates audience paths and editorial content.
'site compose' creates conservative article seeds from populated answers.
'site build' produces a standalone static website; 'site check' checks its inputs
and compares it with an independent render.

For older per-article workflows, 'record' stores the generation inputs and
'check' reports artifacts whose sources have moved.`,
	}
	cmd.AddCommand(newDocsCheckCmd(), newDocsRecordCmd(), newDocsCLICmd(), newDocsSiteCmd())
	return cmd
}

func newDocsCheckCmd() *cobra.Command {
	var asJSON bool
	var strict bool

	cmd := &cobra.Command{
		Use:   "check",
		Short: "Report generated artifacts whose inputs have changed",
		RunE: func(cmd *cobra.Command, args []string) error {
			reg := registryForCommand(cmd)
			manifest, err := facet.LoadManifest(reg.ManifestPath())
			if err != nil {
				return err
			}
			if len(manifest.Artifacts) == 0 {
				if !asJSON {
					fmt.Println("No generated artifacts recorded — nothing to check.")
				} else {
					fmt.Println("[]")
				}
				return nil
			}

			anchors, err := facet.ScanAnchors(reg.Root, facet.ScanOptionsFromEnv())
			if err != nil {
				return err
			}
			statuses := facet.CheckManifest(reg, manifest, anchors)

			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				if err := enc.Encode(statuses); err != nil {
					return err
				}
			} else {
				printStatuses(statuses)
			}

			// Stale documentation is a maintenance signal, not a broken build,
			// so it only fails the run when the caller asks for that.
			if strict {
				for _, s := range statuses {
					if s.State != facet.StateCurrent {
						os.Exit(1)
					}
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit statuses as JSON")
	cmd.Flags().BoolVar(&strict, "strict", false, "exit non-zero when any artifact is not current")
	return cmd
}

func printStatuses(statuses []facet.Status) {
	var stale int
	for _, s := range statuses {
		if s.State == facet.StateCurrent {
			continue
		}
		stale++
		fmt.Printf("%s: %s (%s)\n", s.State, s.Path, s.Feature)
		for _, r := range s.Reasons {
			fmt.Printf("    %s\n", r)
		}
	}
	fmt.Printf("\n%d artifact(s) checked — %d need regeneration\n", len(statuses), stale)
	if stale > 0 {
		fmt.Println("\nA changed input does not always mean the article is wrong. Review the")
		fmt.Println("diff before regenerating; a refactor that preserves behaviour needs nothing.")
	}
}

func newDocsRecordCmd() *cobra.Command {
	var feature, artifact, model, promptVersion, pipeline, generatedAt string
	var slots []string

	cmd := &cobra.Command{
		Use:   "record",
		Short: "Record what produced a generated artifact",
		RunE: func(cmd *cobra.Command, args []string) error {
			if feature == "" || artifact == "" {
				return fmt.Errorf("--feature and --artifact are both required")
			}
			reg := registryForCommand(cmd)

			anchors, err := facet.ScanAnchors(reg.Root, facet.ScanOptionsFromEnv())
			if err != nil {
				return err
			}
			gen := facet.Generator{Pipeline: pipeline, Model: model, PromptVersion: promptVersion}
			entry, err := facet.BuildArtifact(reg, feature, artifact, gen, slots, anchors)
			if err != nil {
				return err
			}
			entry.GeneratedAt = generatedAt

			manifest, err := facet.LoadManifest(reg.ManifestPath())
			if err != nil {
				return err
			}
			manifest.Upsert(entry)
			if err := manifest.Save(reg.ManifestPath()); err != nil {
				return err
			}
			fmt.Printf("Recorded %s (%d input(s)) in %s\n",
				entry.Subject.Path, len(entry.Inputs), reg.Rel(reg.ManifestPath()))
			return nil
		},
	}
	cmd.Flags().StringVar(&feature, "feature", "", "feature id the artifact was generated from")
	cmd.Flags().StringVar(&artifact, "artifact", "", "path to the generated file, relative to the repo root")
	cmd.Flags().StringSliceVar(&slots, "slots", nil, "Knowledge slots the artifact consumed")
	cmd.Flags().StringVar(&pipeline, "pipeline", "facet-knowledge", "generator pipeline name")
	cmd.Flags().StringVar(&model, "model", "", "model that generated the artifact")
	cmd.Flags().StringVar(&promptVersion, "prompt-version", "", "version of the generation prompt")
	cmd.Flags().StringVar(&generatedAt, "generated-at", "", "ISO date of generation")
	return cmd
}
