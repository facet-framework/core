package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/facet-framework/core/registry"
	"github.com/spf13/cobra"
)

func newLintCmd() *cobra.Command {
	var asJSON bool
	var skip []string
	var today string
	var registry string
	var staged bool

	cmd := &cobra.Command{
		Use:   "lint",
		Short: "Check records against their schema and against the code they anchor",
		Long: `Runs the registry's integrity checks:

  schema          every record satisfies its JSON Schema
  orphan-ids      every @facet: tag resolves to a record
  orphan-records  every active record is anchored in source — per variant when
                  the project declares one, where only code exclusive to a
                  client proves that client can reach the feature
  variants        the variant map covers every anchored file, and no record
                  claims a variant the map does not declare
  taxonomy        every record's id names a domain and a capability the
                  project's taxonomy.yaml declares, and (warning) every
                  declared node has a record written under it — silent in a
                  project that declares no taxonomy
  glossary        the project's glossary.yaml can be read at all — silent in a
                  project that declares none
  duplicate-glossary-term
                  the same term defined twice in glossary.yaml; case-sensitive,
                  so 'Consignee' and 'consignee' are two terms and not one
                  duplicate
  glossary-near-miss
                  (warning) a word in intent.summary or a contract statement
                  that is not a defined glossary term but is very nearly one —
                  the same letters in a different case, or a small edit away.
                  Such a spelling matches no term for any consumer and nothing else
                  would say so. Knowledge answers are deliberately outside it
  duplicate-glossary-category
                  the same category id declared twice in glossary.yaml's
                  'categories' list — two headings under one name
  undeclared-glossary-category
                  a term naming a category the glossary does not declare. The
                  correct answers are in the same file, so this is a typo or a
                  category somebody deleted, never a judgement call. A term
                  naming no category at all is silent: absent means nobody
                  decided, not a group called 'Other'
  empty-glossary-category
                  (warning) a declared category no term names. Declaring one
                  before writing its first term is the intended order, so this
                  cannot be an error; what it catches is the half-finished
                  rename
  sizing          (warning) an active record with a single contract scenario,
                  or with more than twelve — gate 3 of the sizing test
                  (FACET 2.1.1), the one an author most often gets wrong in
                  the same direction for a whole repository
  registry-refs   every registry-qualified @facet: tag names a declared registry
  flags           every declared feature-flag key still exists in source
  id-path         every record's id matches its file path
  knowledge       active records with Knowledge slots awaiting their owner
  backing         a scenario declared unbacked whose named unblocker has since
                  gone active, and per-variant backing naming a variant the
                  variant map does not declare
  deviation       (warning) a scenario declaring a documented defect whose
                  'tracked_as' names a record that has since gone active — the
                  fix may have shipped while the defect stayed written down.
                  Declaring the defect is never itself a finding, and nothing
                  here judges the scenario's wording: whether an id names the
                  observed behaviour rather than the expected one is a review
                  obligation (FACET 3.2.2), not something a check can decide
  broken-links    (warning) intent.decision_records and
                  observation.alerts[].runbook name a repository-relative
                  path that exists in the checkout; http(s):// values are
                  never fetched, so a linked URL is not checked
  dependencies    every lineage.depends_on, depended_on_by, and superseded_by
                  reference names a record the project has
  superseded-by   an active record that also declares superseded_by, and a
                  superseded record still anchored in source after the
                  successor it names has gone active
  proves          every verification.*[].proves id names a scenario the same
                  record actually declares under contract.scenarios, and
                  (warning) every scenario has at least one proof
  verification-targets
                  every verification.*[].id that names a path points at a file
                  the project has — the other half of the line 'proves'
                  checks. An id in a form that names no file (a module path,
                  a CI job) is left alone
  unclaimed-tests (warning) a test file no record names, grouped by
                  directory — the mirror of verification-targets, asking
                  whether an existing test is claimed rather than whether a
                  named one exists. Not every unclaimed test is a defect
  sibling-suffix  (warning) a record 'x-<word>' beside a record 'x' in the
                  same capability — the shape one feature takes when it is
                  documented twice. Asks, never states: only a reader of both
                  summaries can say
  created-at-history
                  (warning) a lineage.created_at the history contradicts:
                  later than the day the record's own file arrived, or more
                  than 14 days before the oldest code the record anchors. Two
                  rules, because a date guessed too early looks exactly like a
                  correct one. Silent without history — which means not
                  examined, not in order
  no-scenarios    (warning) a record anchored in source that declares zero
                  contract.scenarios — real code with nothing it could be
                  checked against
  anchor-proves   every @facet-proves: id on an anchor names a scenario the
                  same record actually declares under contract.scenarios —
                  the code-side mirror of proves
  anchor-roles    an active record whose anchors never mark one '@facet-role: primary'
  test-only-anchors  an active record's primary anchor found, by a naive
                  cross-language text search, no caller outside test code —
                  files matching *_test.*, *.test.*, *.spec.*, or under a
                  test/tests/spec/__tests__/testdata directory; --skip it on a
                  project whose tests live somewhere else, until that is
                  configurable
  enum-divergence a data-entity field's enum-shaped constraint (any of four
                  recognized spellings) carries a different value set under
                  the same field name on another data-entity — a warning
                  naming both entities and the difference
  plugin-lint     findings from the facet.lint tool of an installed
                  plugin's sidecar; the tool must be granted (an ungranted
                  one is a warning naming the grant), the binary's checksum
                  is verified before it runs, and one that fails, answers
                  nonsense or takes longer than 30 seconds is an error of
                  that plugin, never a silent pass
  error-codes     (warning) the same identifier (default pattern E\d{4},
                  configurable via .facetrc.yaml's error_codes.pattern)
                  appears in records from more than one domain — the
                  collision a banded range exists to prevent. Same-domain
                  reuse and a record's own repeated mentions are not
                  findings; see 'facet errors' for the whole register

A project declaring several registries in .facetrc.yaml is checked once per
registry, and findings say which one they came from.

Errors fail the run. Warnings never do — an unanswered Knowledge slot is work
for a feature owner, not a broken build.

With --staged only the index is judged: the staged tree is linted, HEAD is
linted as the baseline, and only the errors the index introduces are reported.
An error already in HEAD stops no one who did not touch it, and an unstaged
change — broken or repaired — changes nothing. This is the check the plugin's
commit gate runs, so a pre-commit hook and CI can run the same one. On an
unborn branch the baseline is empty; a damaged HEAD is an error, never a clean
report. Without --staged the whole repository is checked.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			root := ResolveRoot(cmd)

			if staged {
				// The staged comparison always lints every registry with the
				// default options; a flag that narrows the run would make
				// "clean" mean something different from what the gate means.
				if registry != "" || len(skip) > 0 || today != "" {
					return fmt.Errorf("--staged cannot be combined with --registry, --skip or --today: it runs the same check as the commit gate")
				}
				top := gitToplevel(root)
				if top == "" {
					return fmt.Errorf("--staged needs a git repository: there is no index to compare with HEAD in %s", root)
				}
				report, err := stagedLintWithSourceView(top, sourceViewFlag(cmd))
				if err != nil {
					return err
				}
				return emitLintReport(report, asJSON)
			}

			opts := facet.LintOptions{
				Scan:  facet.ScanOptionsFromEnv(),
				Skip:  map[string]bool{},
				Today: today,
				// The same directory `facet mcp` reads, so a
				// plugin added with `facet plugin add` lints with no setting.
				PluginsDir: PluginsDir(),
			}
			opts.Scan.View = registryForCommand(cmd).SourceView
			if opts.Today == "" {
				opts.Today = time.Now().UTC().Format("2006-01-02")
			}
			for _, s := range skip {
				opts.Skip[s] = true
			}

			// '--registry' narrows the findings, never the project the registry
			// is judged in: shared code and cross-registry lineage need the other
			// registries read, or every anchor of another product reads as
			// dangling.
			opts.Registry = registry
			report, err := facet.LintAll(root, opts)
			if err != nil {
				return err
			}

			return emitLintReport(report, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit findings as JSON")
	cmd.Flags().BoolVar(&staged, "staged", false, "check only the index: report the errors it introduces compared with HEAD (what the commit gate runs)")
	cmd.Flags().StringSliceVar(&skip, "skip", nil, "checks to skip (schema, orphan-ids, orphan-records, variants, taxonomy, glossary, duplicate-glossary-term, glossary-near-miss, duplicate-glossary-category, undeclared-glossary-category, empty-glossary-category, sizing, registry-refs, flags, knowledge, backing, deviation, examples, spec-conformance, broken-links, dependencies, superseded-by, proves, verification-targets, unclaimed-tests, sibling-suffix, created-at-history, no-scenarios, anchor-proves, anchor-roles, test-only-anchors, enum-divergence, unanchored-hotspots, undocumented-directories, error-codes, lost-anchors, plugin-lint)")
	cmd.Flags().StringVar(&today, "today", "", "ISO date used for flag retirement checks (default: today, UTC)")
	cmd.Flags().StringVar(&registry, "registry", "", "check only this registry (default: all declared in "+facet.ConfigName+")")
	return cmd
}

// emitLintReport prints a lint report as text or JSON and exits 1 when it
// holds an error. 'facet lint' and 'facet lint --staged' share it, so the two
// emit one shape.
func emitLintReport(report facet.Report, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return err
		}
	} else {
		printReport(report)
	}
	if report.Failed() {
		os.Exit(1)
	}
	return nil
}

func printReport(r facet.Report) {
	var errs, warns int
	for _, f := range r.Findings {
		marker := "warning"
		if f.Severity == facet.SeverityError {
			marker = "error"
			errs++
		} else {
			warns++
		}
		loc := f.Path
		if loc == "" {
			loc = f.ID
		}
		if f.Registry != "" {
			loc = "[" + f.Registry + "] " + loc
		}
		fmt.Printf("%s: %s: %s\n", marker, loc, f.Message)
		if f.Hint != "" {
			fmt.Printf("       %s\n", f.Hint)
		}
	}
	scope := ""
	if len(r.Checked.Registries) > 0 {
		scope = fmt.Sprintf(" across %d registr%s (%s)",
			len(r.Checked.Registries), plural(len(r.Checked.Registries), "y", "ies"),
			strings.Join(r.Checked.Registries, ", "))
	}
	fmt.Printf("\n%d record(s), %d anchor(s) checked%s — %d error(s), %d warning(s)%s\n",
		r.Checked.Records, r.Checked.Anchors, scope, errs, warns, warningBreakdown(r))
}

// warningBreakdown names which checks the warnings came from, largest first.
// @facet: registry/integrity/knowledge-gaps
// @facet-role: related
func warningBreakdown(r facet.Report) string {
	counts := map[string]int{}
	for _, f := range r.Findings {
		if f.Severity != facet.SeverityError {
			counts[f.Check]++
		}
	}
	if len(counts) == 0 {
		return ""
	}
	checks := make([]string, 0, len(counts))
	for c := range counts {
		checks = append(checks, c)
	}
	sort.Slice(checks, func(i, j int) bool {
		if counts[checks[i]] != counts[checks[j]] {
			return counts[checks[i]] > counts[checks[j]]
		}
		return checks[i] < checks[j]
	})
	parts := make([]string, 0, len(checks))
	for _, c := range checks {
		parts = append(parts, fmt.Sprintf("%s %d", c, counts[c]))
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
