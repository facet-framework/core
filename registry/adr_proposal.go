// @facet: authoring/guardrails/adr-status-proposal
// @facet-role: primary
package facet

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

// ADRReadDirectory is the default ADR directory when none is configured.
const ADRReadDirectory = "docs/adr"

var adrStatusHeader = regexp.MustCompile(`^(\*\*Status:\*\*[ \t]*)([^ \t,(]+)(.*)$`)

// The four ADR statuses. Writers write only these English values.
const (
	ADRStatusProposed   = "proposed"
	ADRStatusAccepted   = "accepted"
	ADRStatusSuperseded = "superseded"
	ADRStatusRejected   = "rejected"
)

var adrStatusWords = map[string]bool{ADRStatusProposed: true, ADRStatusAccepted: true, ADRStatusSuperseded: true, ADRStatusRejected: true}

// ADRStatusAliases maps the German status values ADRs were written in before
// rc.1 to their English value. A reader accepts them so an existing ADR keeps
// its status; nothing writes them.
var ADRStatusAliases = map[string]string{
	"vorgeschlagen": ADRStatusProposed,
	"angenommen":    ADRStatusAccepted,
	"abgelöst":      ADRStatusSuperseded,
	"verworfen":     ADRStatusRejected,
}

// NormalizeADRStatus lower-cases a status and maps a German alias to its
// English value. An unknown word comes back lower-cased and unchanged, so the
// caller can still name it in a refusal.
func NormalizeADRStatus(status string) string {
	status = strings.ToLower(strings.TrimSpace(status))
	if english, ok := ADRStatusAliases[status]; ok {
		return english
	}
	return status
}

// ADRStatusProposal is an inspection artifact, never permission to write.
// Successor is metadata only; this feature invents no successor line format.
type ADRStatusProposal struct {
	Path      string `json:"path"`
	Current   string `json:"current"`
	Target    string `json:"target"`
	Reason    string `json:"reason,omitempty"`
	Successor string `json:"successor,omitempty"`
	Before    string `json:"before,omitempty"`
	After     string `json:"after,omitempty"`
	Diff      string `json:"diff"`
	Achieved  bool   `json:"achieved"`
}

func adrRelativePath(path, directory string) string { return strings.TrimPrefix(path, directory+"/") }

// ProposeADRStatus reads only the ADR plugin's existing scope. A missing or
// ambiguous status is refused; dates and other suffix prose stay opaque.
func ProposeADRStatus(root, path, target, reason, successor string) (ADRStatusProposal, error) {
	requested := target
	target = NormalizeADRStatus(target)
	proposal := ADRStatusProposal{Target: target, Reason: strings.TrimSpace(reason)}
	if !adrStatusWords[target] {
		return proposal, fmt.Errorf("unsupported ADR status %q: proposed, accepted, superseded or rejected", requested)
	}
	cfg, err := LoadConfig(root)
	if err != nil {
		return proposal, err
	}
	directory := cfg.ADRDirectory()
	dir, err := ResolveReadDir(root, directory)
	if err != nil {
		return proposal, err
	}
	path = adrRelativePath(path, directory)
	body, err := ReadRepoFile(dir, path, ".md")
	if err != nil {
		return proposal, err
	}
	if !utf8.ValidString(body) || strings.ContainsRune(body, 0) {
		return proposal, fmt.Errorf("ADR must be UTF-8 Markdown without NUL bytes")
	}
	proposal.Path = directory + "/" + path
	lines := strings.SplitAfter(body, "\n")
	index := -1
	var parts []string
	for i, line := range lines {
		text := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if strings.TrimSpace(text) == "---" || strings.HasPrefix(text, "## ") {
			break
		}
		trimmed := strings.TrimSpace(text)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") || strings.Contains(text, "<!--") {
			return proposal, fmt.Errorf("ADR header contains a code fence or HTML comment; status metadata is not unambiguous")
		}

		if strings.HasPrefix(text, "**Status:**") {
			if index >= 0 {
				return proposal, fmt.Errorf("ADR has multiple status header lines")
			}
			index = i
			parts = adrStatusHeader.FindStringSubmatch(text)
		}
	}
	if index < 0 || len(parts) == 0 {
		return proposal, fmt.Errorf("ADR has no unambiguous status header before its body")
	}
	// A German status on disk is read as its English value. The proposal
	// still replaces the word as written, so the diff rewrites it in English.
	proposal.Current = NormalizeADRStatus(parts[2])
	if !adrStatusWords[proposal.Current] {
		return proposal, fmt.Errorf("ADR status %q is not one of the four known statuses: proposed, accepted, superseded or rejected", parts[2])
	}
	proposal.Before = parts[0]
	if proposal.Current == target {
		proposal.Achieved = true
		return proposal, nil
	}
	if proposal.Reason == "" {
		return proposal, fmt.Errorf("a status proposal requires a reason")
	}
	if target == ADRStatusSuperseded {
		if successor == "" {
			return proposal, fmt.Errorf("superseded requires an existing different successor ADR")
		}
		successor = adrRelativePath(successor, directory)
		if _, err := ReadRepoFile(dir, successor, ".md"); err != nil {
			return proposal, fmt.Errorf("successor: %w", err)
		}
		sourceInfo, err := os.Stat(filepath.Join(dir, filepath.FromSlash(path)))
		if err != nil {
			return proposal, err
		}
		nextInfo, err := os.Stat(filepath.Join(dir, filepath.FromSlash(successor)))
		if err != nil {
			return proposal, err
		}
		if os.SameFile(sourceInfo, nextInfo) {
			return proposal, fmt.Errorf("the successor must be a different ADR")
		}
		proposal.Successor = directory + "/" + successor
	} else if successor != "" {
		return proposal, fmt.Errorf("a successor is only metadata for a superseded proposal")
	}
	proposal.After = parts[1] + target + parts[3]
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	start, end := index-3, index+4
	if start < 0 {
		start = 0
	}
	if end > len(lines) {
		end = len(lines)
	}
	var diff strings.Builder
	fmt.Fprintf(&diff, "--- a/%s\n+++ b/%s\n@@ -%d,%d +%d,%d @@\n", proposal.Path, proposal.Path, start+1, end-start, start+1, end-start)
	emitLine := func(prefix, line string) {
		diff.WriteString(prefix + line)
		if !strings.HasSuffix(line, "\n") {
			diff.WriteString("\n\\ No newline at end of file\n")
		}
	}
	for i := start; i < end; i++ {
		if i != index {
			emitLine(" ", lines[i])
			continue
		}
		emitLine("-", lines[i])
		ending := strings.TrimPrefix(lines[i], proposal.Before)
		emitLine("+", proposal.After+ending)
	}
	proposal.Diff = diff.String()
	return proposal, nil
}
