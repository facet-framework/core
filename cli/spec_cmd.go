package cli

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/facet-framework/core/internal/spec"
	"github.com/spf13/cobra"
)

type specLintReport struct {
	Checked  int            `json:"checked"`
	Skipped  int            `json:"skipped"`
	Findings []spec.Finding `json:"findings"`
}

func newSpecCmd() *cobra.Command {
	parent := &cobra.Command{Use: "spec", Short: "Work with specification files"}
	var asJSON bool
	lint := &cobra.Command{
		Use:   "lint [path...]",
		Short: "Validate local specification files",
		RunE: func(cmd *cobra.Command, paths []string) error {
			if len(paths) == 0 {
				root, _ := cmd.Flags().GetString("root")
				if root == "" {
					root = "."
				}
				paths = []string{root}
			}
			report := lintSpecPaths(paths)
			out := cmd.OutOrStdout()
			if asJSON {
				if err := json.NewEncoder(out).Encode(report); err != nil {
					return err
				}
			} else {
				for _, finding := range report.Findings {
					fmt.Fprintln(out, finding)
				}
				fmt.Fprintf(out, "checked %d spec files, skipped %d markdown files\n", report.Checked, report.Skipped)
			}
			if len(report.Findings) > 0 {
				return errSilent
			}
			return nil
		},
	}
	lint.Flags().BoolVar(&asJSON, "json", false, "print findings and file counts as JSON")
	parent.AddCommand(lint, newSpecUpdateCmd(), newSpecDriftCmd(), newSpecCheckLockCmd())
	return parent
}

func lintSpecPaths(paths []string) specLintReport {
	report := specLintReport{Findings: []spec.Finding{}}
	seen := make(map[string]bool)
	for _, path := range paths {
		root, err := filepath.Abs(path)
		if err != nil {
			report.Findings = append(report.Findings, spec.Finding{File: path, Line: 1, Message: err.Error(), Binding: spec.UnknownBinding})
			continue
		}
		_ = filepath.WalkDir(path, func(file string, entry fs.DirEntry, err error) error {
			if err != nil {
				report.Findings = append(report.Findings, spec.Finding{File: file, Line: 1, Message: err.Error(), Binding: spec.UnknownBinding})
				return nil
			}
			if entry.IsDir() && (entry.Name() == ".git" || spec.InOpenSpecChanges(file) || spec.InOpenSpecChanges(root)) {
				return filepath.SkipDir
			}
			ext := filepath.Ext(file)
			if !entry.Type().IsRegular() || (ext != ".md" && ext != ".feature" && ext != ".spec") {
				return nil
			}
			absoluteFile, err := filepath.Abs(file)
			if err != nil {
				report.Findings = append(report.Findings, spec.Finding{File: file, Line: 1, Message: err.Error(), Binding: spec.UnknownBinding})
				return nil
			}
			sourceRoot := root
			if absoluteFile == root {
				sourceRoot = filepath.Dir(root)
			}
			if seen[absoluteFile] {
				return nil
			}
			seen[absoluteFile] = true
			doc, findings := spec.ReadFile(sourceRoot, file)
			if doc.Skipped {
				report.Skipped++
			} else {
				report.Checked++
			}
			report.Findings = append(report.Findings, findings...)
			return nil
		})
	}
	return report
}
