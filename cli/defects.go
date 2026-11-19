package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/facet-framework/core/registry"
	"github.com/spf13/cobra"
)

// newDefectsCmd reads and writes the defect ledger.
//
// @facet: registry/history/defect-ledger
// @facet-role: related
func newDefectsCmd() *cobra.Command {
	var asJSON bool
	var registry string
	var byFile bool
	var top int

	cmd := &cobra.Command{
		Use:   "defects",
		Short: "Rank features and files by how often they have been fixed",
		Long: `Prints the features whose anchored code has needed fixing, most fixed first,
with the files inside each that carry the count.

The ledger is evidence, not a verdict. A feature high in this list is where
bugs have been *found and fixed*, which is not the same as where bugs are: a
feature nobody uses and nobody tests sits at zero for the same reason a correct
one does. Read it next to the test coverage and the traffic, and treat it as a
question about where an extra reviewer or a rewrite would pay for itself.

Records with no ledger are absent rather than listed at zero. Nothing has
looked at them, and reporting that as "has never broken" is a stronger claim
than this registry can make.

'facet defects backfill' fills the ledger from git history.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			registries, err := selectRegistries(cmd, registry)
			if err != nil {
				return err
			}

			var spots []facet.Hotspot
			var files []facet.FileCount
			perFile := map[string]int{}
			for _, reg := range registries {
				records, err := reg.LoadFeatures()
				if err != nil {
					return err
				}
				for _, h := range facet.Hotspots(records) {
					h.Path = reg.Rel(h.Path)
					spots = append(spots, h)
					for _, f := range h.Files {
						perFile[f.Path] += f.Count
					}
				}
			}
			for p, n := range perFile {
				files = append(files, facet.FileCount{Path: p, Count: n})
			}
			sortFileCounts(files)

			if top > 0 && len(spots) > top {
				spots = spots[:top]
			}

			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				if byFile {
					if files == nil {
						files = []facet.FileCount{}
					}
					return enc.Encode(files)
				}
				if spots == nil {
					spots = []facet.Hotspot{}
				}
				return enc.Encode(spots)
			}
			if byFile {
				printDefectFiles(files, top)
				return nil
			}
			printDefects(spots)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the ranking as JSON")
	cmd.Flags().BoolVar(&byFile, "by-file", false, "rank files instead of features")
	cmd.Flags().IntVar(&top, "top", 0, "show only the first N entries (default: all)")
	cmd.Flags().StringVar(&registry, "registry", "", "only this registry (default: all declared in "+facet.ConfigName+")")
	cmd.AddCommand(newDefectsBackfillCmd())
	return cmd
}

func sortFileCounts(files []facet.FileCount) {
	sort.Slice(files, func(i, j int) bool {
		if files[i].Count != files[j].Count {
			return files[i].Count > files[j].Count
		}
		return files[i].Path < files[j].Path
	})
}

func printDefects(spots []facet.Hotspot) {
	if len(spots) == 0 {
		fmt.Println("No record carries a defect ledger. Run 'facet defects backfill' to fill one from git history.")
		return
	}
	total := 0
	for _, h := range spots {
		total += h.Total
		fmt.Printf("%-6d %s\n", h.Total, h.Feature)
		fmt.Printf("       last fix %s\n", h.LastFix)
		for _, f := range h.Files {
			fmt.Printf("       %-4d %s\n", f.Count, f.Path)
		}
	}
	fmt.Printf("\n%d recorded fix(es) across %d feature(s)\n", total, len(spots))
}

func printDefectFiles(files []facet.FileCount, top int) {
	if len(files) == 0 {
		fmt.Println("No record carries a defect ledger. Run 'facet defects backfill' to fill one from git history.")
		return
	}
	if top > 0 && len(files) > top {
		files = files[:top]
	}
	for _, f := range files {
		fmt.Printf("%-6d %s\n", f.Count, f.Path)
	}
	fmt.Printf("\n%d file(s) with a recorded fix\n", len(files))
}

func newDefectsBackfillCmd() *cobra.Command {
	var opts facet.BackfillOptions
	var registry string
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "backfill",
		Short: "Fill the defect ledger from git history",
		Long: `Walks the history, picks out the commits that look like bugfixes, resolves
the files each one changed to the features anchored in them, and appends what
it finds to those records.

WHAT IT CATCHES. By default, commits whose subject follows Conventional
Commits — 'fix:', 'fix(scope):', 'hotfix:', with or without a '!'. That is the
one signal in a commit message that is a stated convention rather than a guess.

WHAT IT MISSES, and this is not a small list:

  - a fix whose message does not say 'fix' — "oops", "typo", "address review",
    "make the test pass". Nothing in the message distinguishes those from any
    other commit, and no heuristic over prose will find them;
  - a fix to a file that carries no '@facet:' anchor. Those are reported as
    unresolved rather than dropped silently, so the size of the blind spot is
    visible on every run;
  - a fix to a file that has since been renamed or deleted. Attribution is by
    the path as the commit wrote it, matched against the anchors in the working
    tree, so history before a rename resolves to nothing;
  - which part of a commit was the fix. A commit touching six files records all
    six anchored ones, because the alternative is a diff-reading heuristic that
    would be confidently wrong instead of coarsely right.

Recall is therefore bounded by the discipline of the log. --match takes any
regular expression, so a project with its own convention can state it:

  facet defects backfill --match '^(fix|bug)[:(]|#[0-9]+'

RUNNING IT. Re-running is safe: a commit a record has already recorded, or
listed under 'defects.excluded', is skipped, so overlapping ranges cost the
walk and nothing else. Start bounded — '--since=90.days' or '--limit=500' —
and read the output before widening. A first pass over a long history is a
deliberate operation, not a routine one: the walk is linear in commits and the
whole history of a large repository can take hours.

Only the 'defects' block of a record is rewritten. Comments, key order and
formatting elsewhere in the file are left exactly as they were.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			root := ResolveRoot(cmd)
			registries, err := selectRegistries(cmd, registry)
			if err != nil {
				return err
			}

			commits, err := facet.BugfixCommits(root, opts)
			if err != nil {
				return err
			}

			combined := facet.BackfillReport{Candidates: len(commits), DryRun: opts.DryRun}
			for _, reg := range registries {
				records, err := reg.LoadFeatures()
				if err != nil {
					return err
				}
				anchors, err := facet.ScanAnchors(reg.Root, reg.ScanOptions())
				if err != nil {
					return err
				}
				rep, err := facet.Backfill(reg, records, anchors, commits, opts)
				if err != nil {
					return err
				}
				combined.Recorded += rep.Recorded
				combined.Skipped += rep.Skipped
				combined.Changes = append(combined.Changes, rep.Changes...)
				// Unresolved is per-registry by construction; with one registry
				// — the overwhelmingly common case — it is exactly the report.
				if len(registries) == 1 {
					combined.Unresolved = rep.Unresolved
				}
			}

			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(combined)
			}
			printBackfill(combined)
			return nil
		},
	}
	cmd.Flags().StringVar(&opts.Match, "match", "",
		"regular expression a commit subject must match (default: "+facet.DefaultFixPattern+")")
	cmd.Flags().StringVar(&opts.Since, "since", "", "only commits authored after this date (git syntax, e.g. '90.days' or '2026-01-01')")
	cmd.Flags().StringVar(&opts.Until, "until", "", "only commits authored before this date")
	cmd.Flags().IntVar(&opts.Limit, "limit", 0, "walk at most this many commits back from the starting revision")
	cmd.Flags().StringVar(&opts.Rev, "rev", "", "revision to walk back from (default: HEAD)")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "report what would be written without writing it")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the report as JSON")
	cmd.Flags().StringVar(&registry, "registry", "", "only this registry (default: all declared in "+facet.ConfigName+")")
	return cmd
}

func printBackfill(rep facet.BackfillReport) {
	for _, ch := range rep.Changes {
		fmt.Printf("%s (%s)\n", ch.Feature, ch.Path)
		for _, f := range ch.Added {
			fmt.Printf("    %s  %s  %s\n", f.Commit[:min(8, len(f.Commit))], f.Date, collapse(f.Subject))
			for _, p := range f.Files {
				fmt.Printf("        %s\n", p)
			}
		}
	}

	verb := "recorded"
	if rep.DryRun {
		verb = "would record"
	}
	fmt.Printf("\n%d candidate commit(s) — %s %d fix(es), skipped %d already known\n",
		rep.Candidates, verb, rep.Recorded, rep.Skipped)

	if n := len(rep.Unresolved); n > 0 {
		fmt.Printf("\n%d candidate(s) touched no anchored file and were not attributed:\n", n)
		shown := rep.Unresolved
		const maxShown = 10
		if len(shown) > maxShown {
			shown = shown[:maxShown]
		}
		for _, c := range shown {
			fmt.Printf("    %s  %s  %s\n", c.SHA[:min(8, len(c.SHA))], c.Date, collapse(c.Subject))
		}
		if n > len(shown) {
			fmt.Printf("    ... and %d more\n", n-len(shown))
		}
		fmt.Println("\nThose are the ledger's blind spot, not its errors: code that carries no")
		fmt.Println("'@facet:' anchor belongs to no feature, so a fix to it belongs to none either.")
	}
	if rep.DryRun {
		fmt.Println("\nNothing was written. Drop --dry-run to record this.")
	}
}

// selectRegistries resolves the registries a command should act on, honouring
// a --registry flag. Shared by every command that reads the whole registry.
func selectRegistries(cmd *cobra.Command, name string) ([]facet.Registry, error) {
	cfg, err := configForCommand(cmd)
	if err != nil {
		return nil, err
	}
	if name == "" {
		return cfg.All(), nil
	}
	reg, err := cfg.Select(name)
	if err != nil {
		return nil, err
	}
	return []facet.Registry{reg}, nil
}
