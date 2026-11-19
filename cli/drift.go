package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/facet-framework/core/registry"
	"github.com/spf13/cobra"
)

// newDriftCmd puts the registry library's drift detection on the command
// surface.
//
// @facet: registry/history/drift-cli
// @facet-role: primary
func newDriftCmd(writeReport driftReportWriter) *cobra.Command {
	if writeReport == nil {
		writeReport = writeCoreDriftReport
	}
	var asJSON bool
	var registry string
	var baseline string
	var repository string

	cmd := &cobra.Command{
		Use:   "drift",
		Short: "Report anchored source that changed without its record following",
		Long: `Answers the question FACET spec 12.1 defines, against one explicit baseline:
which anchored source moved while the record it anchors stood still, which
implementation associations a deletion, a rename or a removed anchor moved or
lost, and which records still name a path git has recorded as renamed away.

The comparison is always the working tree (staged, unstaged and untracked)
against one baseline commit. Without --baseline that is this worktree's accepted
baseline when 'facet drift accept' has recorded one, and HEAD otherwise. A clean
result covers that window only; against HEAD it says nothing about whether the
records were ever reviewed against earlier commits.

Independent repositories containing declared sources are compared in their
own git working tree. JSON includes their individual baselines in repositories;
paths in findings remain relative to the registry workspace. Use --repository
with --baseline for a revision belonging to one source repository.

When the baseline cannot be compared — no commit yet, a revision that does not
exist, no git — the report says so instead of reporting no drift.

Nothing here fails a run on drift: it is a signal for a human or an editor to
look at, not a defect a build should block on. A baseline the caller named
explicitly that does not resolve is an error, because that is a typo rather
than a finding.

An installed product that provides the drift-report service adds its own
findings to this report; a product that could not be checked is named under
product_errors rather than reported as clean.` + productDriftHelp(),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := ResolveRoot(cmd)
			registries, err := driftRegistries(root, registry, sourceViewFlag(cmd))
			if err != nil {
				return err
			}

			report, err := facet.ComputeDriftInRepository(root, registries, baseline, repository)
			if err != nil {
				return err
			}

			if err := writeReport(os.Stdout, root, registries, report, asJSON); err != nil {
				return err
			}
			_ = recordProductWarnings(root, registries, report.Drift, report.Baseline.Commit)
			if (baseline != "" || repository != "") && report.Baseline.Status != facet.BaselineOK {
				return fmt.Errorf("drift could not be determined: %s", report.Baseline.Error)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit drift as JSON")
	cmd.Flags().StringVar(&registry, "registry", "", "check only this registry (default: all declared in "+facet.ConfigName+")")
	cmd.Flags().StringVar(&baseline, "baseline", "", "compare against this revision (default: the accepted baseline, else HEAD)")
	cmd.Flags().StringVar(&repository, "repository", "", "compare only this declared source repository (workspace-relative)")
	cmd.AddCommand(newDriftAcceptCmd(), newDriftResetCmd())
	return cmd
}

// driftReportWriter is a composition seam, not a public plugin protocol.
// Product adapters own their report fields; the command owns computation,
// baseline validation and the existing exit contract.
type driftReportWriter func(io.Writer, string, []facet.Registry, facet.DriftReport, bool) error

func writeCoreDriftReport(w io.Writer, _ string, _ []facet.Registry, report facet.DriftReport, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	printDrift(w, report)
	return nil
}

// newDriftAcceptCmd records that the current changes have been reviewed.
//
// @facet: registry/history/drift-review
// @facet-role: primary
func newDriftAcceptCmd() *cobra.Command {
	var outcome string
	var repository string
	var baseline string
	cmd := &cobra.Command{
		Use:   "accept [path...]",
		Short: "Record that the changes since the baseline have been reviewed",
		Long: `Moves this worktree's accepted baseline forward to include the reviewed
changes — every change when no path is named, otherwise only the named paths.

The baseline is a local git snapshot under ` + facet.AcceptedBaselineRef + `.
That ref is private to this worktree: it is not a branch or a tag, no push
or fetch carries it, nothing is written into the working tree or the index,
and no record is touched. A private registry therefore publishes nothing by
being reviewed.

A later edit to an accepted file differs from the snapshot and is reported
again. A change committed after the snapshot stays reported until it is
accepted too.

Outcome is one of: behavior-unchanged (the default), record-updated,
references-repaired. It is kept with the snapshot as a note for the reviewer,
not as a record field.

Declared independent source repositories keep their own review ref. Without
--repository each is accepted; with paths, only the repositories containing
those workspace-relative paths are accepted. Repositories are updated in order;
if one fails, earlier successful acceptances remain and the error says so.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			root := ResolveRoot(cmd)
			registries, err := driftRegistries(root, "", sourceViewFlag(cmd))
			if err != nil {
				return err
			}
			baselines, err := facet.ReviewSourceRepositoriesFromBaseline(root, registries, repository, args, outcome, false, baseline)
			if err != nil {
				return err
			}
			for _, entry := range baselines {
				b := entry.Baseline
				fmt.Printf("Accepted baseline %s (%s) — %s [repository: %s]\n", shortCommit(b.Commit), b.Outcome, b.AcceptedAt, entry.Path)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&outcome, "outcome", facet.ReviewOutcomes[0], "what the review concluded")
	cmd.Flags().StringVar(&repository, "repository", "", "accept only this declared source repository; paths stay workspace-relative")
	cmd.Flags().StringVar(&baseline, "baseline", "", "start this acceptance at the confirmed reviewed revision, keeping other incoming paths unreviewed")
	return cmd
}

// newDriftResetCmd forgets the accepted baseline.
//
// @facet: registry/history/drift-review
// @facet-role: related
func newDriftResetCmd() *cobra.Command {
	var repository string
	cmd := &cobra.Command{
		Use:   "reset",
		Short: "Forget this worktree's accepted baseline and compare against HEAD again",
		RunE: func(cmd *cobra.Command, args []string) error {
			root := ResolveRoot(cmd)
			registries, err := driftRegistries(root, "", sourceViewFlag(cmd))
			if err != nil {
				return err
			}
			if _, err := facet.ReviewSourceRepositories(root, registries, repository, nil, "", true); err != nil {
				return err
			}
			fmt.Println("Accepted baseline removed; drift is measured against HEAD.")
			return nil
		},
	}
	cmd.Flags().StringVar(&repository, "repository", "", "reset only this declared source repository")
	return cmd
}

func driftRegistries(root, registry string, selection ...string) ([]facet.Registry, error) {
	selected := ""
	if len(selection) > 0 {
		selected = selection[0]
	}
	cfg, err := facet.LoadConfigWithSourceView(root, selected)
	if err != nil {
		return nil, err
	}
	if registry == "" {
		return cfg.All(), nil
	}
	reg, err := cfg.Select(registry)
	if err != nil {
		return nil, err
	}
	return []facet.Registry{reg}, nil
}

func shortCommit(c string) string {
	if len(c) > 12 {
		return c[:12]
	}
	return c
}

func qualified(registry, id string) string {
	if registry == "" {
		return id
	}
	return registry + ":" + id
}

// baselineLine says what the comparison was made against, in every output,
// so a quiet result is never read as more than it is.
func baselineLine(b facet.Baseline) string {
	switch b.Status {
	case facet.BaselineOK:
		switch b.Kind {
		case facet.BaselineAccepted:
			return fmt.Sprintf("Baseline: accepted review %s (%s, %s)", shortCommit(b.Commit), b.Outcome, b.AcceptedAt)
		case facet.BaselineExplicit:
			return fmt.Sprintf("Baseline: %s (%s)", b.Ref, shortCommit(b.Commit))
		default:
			return fmt.Sprintf("Baseline: HEAD (%s) — no review recorded; a clean result covers only the uncommitted work", shortCommit(b.Commit))
		}
	case facet.BaselineMissing:
		return "Baseline: " + b.Ref + " does not resolve to a commit — drift could not be determined"
	case facet.BaselineNoRepository:
		return "Baseline: none — not a git repository, so drift could not be determined"
	default:
		return "Baseline: unavailable — drift could not be determined (" + b.Error + ")"
	}
}

func printDrift(w io.Writer, r facet.DriftReport) {
	if len(r.Repositories) > 0 {
		for _, repo := range r.Repositories {
			fmt.Fprintf(w, "Repository: %s — %s\n", repo.Path, baselineLine(repo.Baseline))
		}
	} else {
		fmt.Fprintln(w, baselineLine(r.Baseline))
	}
	if r.Baseline.Status != facet.BaselineOK && r.Baseline.Status != facet.BaselineMissing {
		return
	}
	if len(r.Drift) == 0 && len(r.StaleReferences) == 0 && len(r.ReferenceChanges) == 0 && len(r.Unreadable) == 0 {
		if r.Baseline.Status == facet.BaselineOK {
			fmt.Fprintln(w, "No drift found — every anchored file that changed since the baseline brought its record with it.")
		}
		return
	}
	for _, d := range r.Drift {
		fmt.Fprintf(w, "drift: %s — changed: %s (%s)\n", qualified(d.Registry, d.ID), d.Path, d.ChangedAt)
	}
	for _, c := range r.ReferenceChanges {
		fmt.Fprintf(w, "reference: %s — %s\n", qualified(c.Registry, c.ID), describeReference(c))
	}
	for _, s := range r.StaleReferences {
		fmt.Fprintf(w, "stale: %s still names %s, now at %s\n", qualified(s.Registry, s.ID), s.Path, s.RenamedTo)
	}
	for _, p := range r.Unreadable {
		fmt.Fprintf(w, "unreadable: %s — its content at the baseline could not be read, so what it anchored is unknown\n", p)
	}
	fmt.Fprintf(w, "\n%d drifted anchor(s), %d reference change(s), %d stale reference(s)\n",
		len(r.Drift), len(r.ReferenceChanges), len(r.StaleReferences))
}

// describeReference words one reference change the same way everywhere a
// person reads it: the hook, the CLI and the lint message.
func describeReference(c facet.ReferenceChange) string {
	role := ""
	if c.Role != "" {
		role = " (" + c.Role + ")"
	}
	var what string
	switch c.Kind {
	case facet.RefDeleted:
		what = "deleted: " + c.Path + role
	case facet.RefRenamed:
		what = "renamed: " + c.Path + " → " + c.NewPath + role
	default:
		what = "anchor removed from " + c.Path + role
		if c.NewPath != "" {
			what += ", now " + c.NewPath
		}
	}
	if c.Evidence == facet.EvidencePrivateSidecar {
		what += " [private anchor]"
	}
	if c.Lost() {
		what += fmt.Sprintf("; %d anchor(s) remain", c.Surviving)
	}
	return what
}
