// @facet: registry/integrity/runner-test-discovery
// @facet-role: related
package cli

import (
	"encoding/json"
	"fmt"
	"github.com/facet-framework/core/registry"
	"github.com/spf13/cobra"
	"io"
	"os"
	"os/signal"
	"sort"
	"syscall"
)

// @facet: registry/integrity/runner-test-execution
// @facet-role: primary
func newTestCmd() *cobra.Command {
	var list, check, update, asJSON, failUnmeasured bool
	var runner, registry, only, featurePrefix string
	cmd := &cobra.Command{Use: "test", Short: "Run declared test commands and report per-file verification results", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			cmd.SetContext(ctx)
			if failUnmeasured && (list || check) {
				return fmt.Errorf("--fail-unmeasured requires execution; omit --list and --check")
			}
			if only != "" && only != "facet" && only != "runner" {
				return fmt.Errorf("--only must be facet or runner")
			}
			if asJSON && only == "runner" {
				return fmt.Errorf("--json cannot be combined with --only runner; use --only runner for unchanged runner streams")
			}
			if (list || check) && only != "" {
				return fmt.Errorf("--only selects execution reports; omit it with --list or --check")
			}
			cfg, err := configForCommand(cmd)
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("feature-prefix") {
				cfg.TestFeaturePrefix = featurePrefix
				cfg.TestFeaturePrefixes = nil
			}
			regs := cfg.All()
			if registry != "" {
				reg, e := cfg.Select(registry)
				if e != nil {
					return e
				}
				regs = []facet.Registry{reg}
			}
			if !list && !check {
				executions := []facet.TestExecution{}
				stdout, stderr := cmd.OutOrStdout(), cmd.ErrOrStderr()
				if only == "facet" {
					stdout, stderr = io.Discard, io.Discard
				} else if asJSON {
					stdout = cmd.ErrOrStderr()
				}
				for _, reg := range regs {
					execution, e := facet.ExecuteTests(cmd.Context(), cfg, reg, runner, update, stdout, stderr)
					if e != nil {
						return e
					}
					executions = append(executions, execution)
				}
				exitCode := testExecutionsExitCode(executions, failUnmeasured)
				if only != "runner" {
					if asJSON {
						enc := json.NewEncoder(cmd.OutOrStdout())
						enc.SetIndent("", "  ")
						if err := enc.Encode(executions); err != nil {
							return err
						}
					} else {
						for _, execution := range executions {
							if execution.Discovery.Registry != "" {
								fmt.Fprintf(cmd.OutOrStdout(), "\nregistry %s\n", execution.Discovery.Registry)
							}
							for _, r := range execution.Runners {
								fmt.Fprintf(cmd.OutOrStdout(), "\nrunner %s: exit %d\n", r.Name, r.Exit)
								if r.ReportProblem != "" {
									fmt.Fprintln(cmd.OutOrStdout(), "  "+r.ReportProblem)
								}
							}
							counts := map[string]int{}
							for _, feature := range execution.Features {
								counts[feature.State]++
								fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", feature.State, feature.ID)
								if feature.State != facet.TestPassed {
									paths := make([]string, 0, len(feature.Files))
									for path := range feature.Files {
										paths = append(paths, path)
									}
									sort.Strings(paths)
									for _, path := range paths {
										fmt.Fprintf(cmd.OutOrStdout(), "  %s %s\n", feature.Files[path], path)
									}
									if len(paths) == 0 {
										fmt.Fprintln(cmd.OutOrStdout(), "  no verification file references")
									}
								}
							}
							for _, f := range execution.Discovery.Findings {
								fmt.Fprintf(cmd.OutOrStdout(), "%s %s %s: %s\n", f.Severity, f.Code, f.Path, f.Message)
							}
							fmt.Fprintf(cmd.OutOrStdout(), "Features: %d passed, %d failed, %d unmeasured (%d total)\n", counts[facet.TestPassed], counts[facet.TestFailed], counts[facet.TestUnmeasured], len(execution.Features))
						}
					}
				}
				if exitCode != 0 {
					return testExitError{code: exitCode, silent: only == "runner"}
				}
				return nil
			}
			reports := []facet.TestDiscovery{}
			failed := false
			for _, reg := range regs {
				report, e := facet.CheckTests(cmd.Context(), cfg, reg, runner, update)
				if e != nil {
					return e
				}
				reports = append(reports, report)
				failed = failed || report.Failed()
			}
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				if err := enc.Encode(reports); err != nil {
					return err
				}
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "Discovery only: test commands were not executed; no test outcomes measured.")
				for _, r := range reports {
					for _, d := range r.Runners {
						fmt.Fprintf(cmd.OutOrStdout(), "%s: discovery measured=%t, %d file(s)\n", d.Name, d.Measured, len(d.Files))
						for _, p := range d.Files {
							fmt.Fprintln(cmd.OutOrStdout(), "  "+p)
						}
					}
					for _, f := range r.Findings {
						fmt.Fprintf(cmd.OutOrStdout(), "%s %s %s %s: %s\n", f.Severity, f.Code, f.Runner, f.Path, f.Message)
					}
				}
			}
			if failed {
				return fmt.Errorf("test discovery drift check failed")
			}
			return nil
		}}
	cmd.Flags().BoolVar(&check, "check", false, "check discovery and baseline drift without executing tests")
	cmd.Flags().BoolVar(&list, "list", false, "list discovered files and check drift without executing tests")
	cmd.Flags().BoolVar(&update, "update-baseline", false, "remove stale baseline entries; never add paths")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit reports as JSON; during execution runner output goes to stderr")
	cmd.Flags().BoolVar(&failUnmeasured, "fail-unmeasured", false, "exit 1 on incomplete feature or file evidence; preserve nonzero runner exits")
	cmd.Flags().StringVar(&runner, "runner", "", "select one declared runner; absence across other runners is unmeasured")
	cmd.Flags().StringVar(&registry, "registry", "", "select one declared registry")
	cmd.Flags().StringVar(&only, "only", "", "show only facet or runner output (execution only)")
	cmd.Flags().StringVar(&featurePrefix, "feature-prefix", "", "limit feature evidence to this ID prefix; overrides test_feature_prefix")
	return cmd
}

type testExitError struct {
	code   int
	silent bool
}

func (e testExitError) Error() string { return fmt.Sprintf("test run failed (exit %d)", e.code) }
func (e testExitError) ExitCode() int { return e.code }

func (e testExitError) Silent() bool { return e.silent }

// Real runner failures take precedence over drift and strict-evidence policy,
// including failures in a later registry. Preserve stable execution order.
func testExecutionsExitCode(executions []facet.TestExecution, failUnmeasured bool) int {
	for _, execution := range executions {
		for _, runner := range execution.Runners {
			if runner.Exit != 0 {
				return runner.Exit
			}
		}
	}
	for _, execution := range executions {
		if code := execution.ExitCode(); code != 0 {
			return code
		}
	}
	if failUnmeasured {
		for _, execution := range executions {
			if execution.HasUnmeasured() {
				return 1
			}
		}
	}
	return 0
}
