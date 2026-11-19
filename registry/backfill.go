// @facet: registry/history/defect-ledger
// @facet-role: primary

package facet

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

// DefaultFixPattern is the heuristic that decides what counts as a bugfix.
const DefaultFixPattern = `(?i)^(fix|bugfix|hotfix)(\([^)]*\))?!?:`

// issueRefPattern picks an issue reference out of a subject line.
var issueRefPattern = regexp.MustCompile(`#[0-9]+`)

// Commit is one candidate bugfix commit and what it changed.
type Commit struct {
	SHA     string   `json:"sha"`
	Date    string   `json:"date"`
	Subject string   `json:"subject"`
	Issue   string   `json:"issue,omitempty"`
	Files   []string `json:"files"`
}

// BackfillOptions bounds and steers a pass over the history.
type BackfillOptions struct {
	// Match overrides DefaultFixPattern. A repository that does not use
	// Conventional Commits needs its own rule, and one baked into the binary
	// would be a rule about somebody else's project.
	Match string
	// Since and Until are passed to git as authored-date bounds.
	Since string
	Until string
	// Limit caps the commits walked, not the fixes found. Bounding the walk is
	// what makes a run predictable in cost; bounding the result would make it
	// unpredictable, because the density of fixes is not known in advance.
	Limit int
	// Rev is the revision to walk back from. Empty means HEAD.
	Rev string
	// DryRun reports what would be written without writing it.
	DryRun bool
}

func (o BackfillOptions) pattern() (*regexp.Regexp, error) {
	expr := o.Match
	if expr == "" {
		expr = DefaultFixPattern
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		// An invalid pattern is an error rather than a default or empty match;
		// an empty match would incorrectly report that no bugfix commits exist.
		return nil, fmt.Errorf("--match %q is not a valid regular expression: %w", expr, err)
	}
	return re, nil
}

// BugfixCommits walks the history and returns the commits whose subject the
// heuristic accepts, newest first.
func BugfixCommits(root string, opts BackfillOptions) ([]Commit, error) {
	re, err := opts.pattern()
	if err != nil {
		return nil, err
	}

	// NUL separates commits and unit-separator separates the header fields, so
	// a subject containing any amount of punctuation cannot be misparsed.
	args := []string{
		"log", "--no-merges", "--date=short", "--name-only",
		"--pretty=format:%x00%H%x1f%ad%x1f%s",
	}
	if opts.Since != "" {
		args = append(args, "--since="+opts.Since)
	}
	if opts.Until != "" {
		args = append(args, "--until="+opts.Until)
	}
	if opts.Limit > 0 {
		args = append(args, fmt.Sprintf("-n%d", opts.Limit))
	}
	rev := opts.Rev
	if rev == "" {
		rev = "HEAD"
	}
	args = append(args, rev)

	cmd := exec.Command("git", args...)
	cmd.Dir = root
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git log: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	var commits []Commit
	for _, chunk := range strings.Split(string(out), "\x00") {
		chunk = strings.TrimLeft(chunk, "\n")
		if chunk == "" {
			continue
		}
		lines := strings.Split(chunk, "\n")
		head := strings.SplitN(lines[0], "\x1f", 3)
		if len(head) != 3 {
			continue
		}
		c := Commit{SHA: strings.ToLower(head[0]), Date: head[1], Subject: head[2]}
		if !re.MatchString(c.Subject) {
			continue
		}
		if m := issueRefPattern.FindString(c.Subject); m != "" {
			c.Issue = m
		}
		for _, p := range lines[1:] {
			if p = strings.TrimSpace(p); p != "" {
				c.Files = append(c.Files, p)
			}
		}
		commits = append(commits, c)
	}
	return commits, nil
}

// BackfillChange is one record the pass would touch, and what it would add.
type BackfillChange struct {
	Feature string `json:"feature"`
	Path    string `json:"path"`
	Added   []Fix  `json:"added"`
}

// BackfillReport is what a pass did, or would have done.
type BackfillReport struct {
	// Candidates is how many commits the heuristic accepted.
	Candidates int `json:"candidates"`
	// Recorded is how many (commit, feature) attributions were appended.
	Recorded int `json:"recorded"`
	// Skipped is how many attributions a record had already judged.
	Skipped int `json:"skipped"`
	// Unresolved are candidates that touched no anchored file, listed rather
	// than counted: they are the pass's blind spot made visible, and anyone
	// deciding whether the ledger is trustworthy needs to see them.
	Unresolved []Commit         `json:"unresolved,omitempty"`
	Changes    []BackfillChange `json:"changes,omitempty"`
	DryRun     bool             `json:"dry_run,omitempty"`
}

// Backfill attributes candidate commits to records and appends what is new.
func Backfill(reg Registry, records []Record, anchors []Anchor, commits []Commit, opts BackfillOptions) (BackfillReport, error) {
	rep := BackfillReport{Candidates: len(commits), DryRun: opts.DryRun}

	// Repo-relative path -> the features anchored in it. Anchors carry
	// absolute paths; commits name paths relative to the root.
	owners := map[string][]string{}
	for _, a := range anchors {
		if !a.BelongsTo(reg.Name) {
			continue
		}
		rel := reg.Rel(a.Path)
		if !contains(owners[rel], a.ID) {
			owners[rel] = append(owners[rel], a.ID)
		}
	}

	byID := map[string]*Record{}
	for i := range records {
		byID[records[i].ID] = &records[i]
	}

	// feature -> the fixes to append to it, in the order the walk found them.
	pending := map[string][]Fix{}
	var order []string

	for _, c := range commits {
		// Which features does this commit touch, and through which files?
		touched := map[string][]string{}
		for _, f := range c.Files {
			for _, id := range owners[f] {
				touched[id] = append(touched[id], f)
			}
		}
		if len(touched) == 0 {
			rep.Unresolved = append(rep.Unresolved, c)
			continue
		}

		for id, files := range touched {
			rec, ok := byID[id]
			if !ok {
				// An anchor pointing at a record that does not exist is the
				// orphan-ids check's finding, not this pass's to report.
				continue
			}
			if rec.KnowsCommit(c.SHA) || knownInPending(pending[id], c.SHA) {
				rep.Skipped++
				continue
			}
			sort.Strings(files)
			fix := Fix{
				Feature: id, Path: rec.Path,
				Commit: c.SHA, Date: c.Date, Files: dedupe(files),
				Subject: c.Subject, Issue: c.Issue,
				RecordedBy: RecordedByGitHistory,
			}
			if _, seen := pending[id]; !seen {
				order = append(order, id)
			}
			pending[id] = append(pending[id], fix)
			rep.Recorded++
		}
	}

	sort.Strings(order)
	for _, id := range order {
		rec := byID[id]
		rep.Changes = append(rep.Changes, BackfillChange{
			Feature: id, Path: reg.Rel(rec.Path), Added: pending[id],
		})
		if opts.DryRun {
			continue
		}
		if err := writeDefects(rec.Path, append(rec.Fixes(), pending[id]...), rec.ExcludedCommits()); err != nil {
			return rep, fmt.Errorf("%s: %w", reg.Rel(rec.Path), err)
		}
	}
	return rep, nil
}

func knownInPending(fixes []Fix, sha string) bool {
	for _, f := range fixes {
		if shaMatches(f.Commit, strings.ToLower(sha)) {
			return true
		}
	}
	return false
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func dedupe(list []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, v := range list {
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// writeDefects rewrites one record's 'defects' block, leaving every other byte
// of the file exactly as the author left it.
func writeDefects(path string, fixes []Fix, excluded []Exclusion) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	block := renderDefectsBlock(fixes, excluded)
	if block == "" {
		return nil
	}
	return writeFileAtomic(path, spliceTopLevelBlock(src, "defects", block))
}

// renderDefectsBlock renders the whole block from the model.
func renderDefectsBlock(fixes []Fix, excluded []Exclusion) string {
	if len(fixes) == 0 && len(excluded) == 0 {
		return ""
	}

	fixes = append([]Fix(nil), fixes...)
	sort.SliceStable(fixes, func(i, j int) bool {
		if fixes[i].Date != fixes[j].Date {
			return fixes[i].Date < fixes[j].Date
		}
		return fixes[i].Commit < fixes[j].Commit
	})
	excluded = append([]Exclusion(nil), excluded...)
	sort.Slice(excluded, func(i, j int) bool { return excluded[i].Commit < excluded[j].Commit })

	var b strings.Builder
	b.WriteString("defects:\n")
	if len(fixes) > 0 {
		b.WriteString("  fixes:\n")
		for _, f := range fixes {
			fmt.Fprintf(&b, "    - commit: %s\n", yamlQuoted(f.Commit))
			fmt.Fprintf(&b, "      date: %s\n", yamlQuoted(f.Date))
			b.WriteString("      files:\n")
			for _, p := range f.Files {
				fmt.Fprintf(&b, "        - %s\n", yamlScalar(p))
			}
			if f.Subject != "" {
				fmt.Fprintf(&b, "      subject: %s\n", yamlQuoted(f.Subject))
			}
			if f.Issue != "" {
				fmt.Fprintf(&b, "      issue: %s\n", yamlQuoted(f.Issue))
			}
			if f.RecordedBy != "" {
				fmt.Fprintf(&b, "      recorded_by: %s\n", f.RecordedBy)
			}
		}
	}
	if len(excluded) > 0 {
		b.WriteString("  excluded:\n")
		for _, e := range excluded {
			fmt.Fprintf(&b, "    - commit: %s\n", yamlQuoted(e.Commit))
			fmt.Fprintf(&b, "      reason: %s\n", yamlQuoted(e.Reason))
		}
	}
	return b.String()
}

// plainScalar matches the strings YAML hands back unchanged and unquoted.
var plainScalar = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_./@+-]*$`)

// yamlScalar quotes only what needs it, so a file path stays readable.
func yamlScalar(s string) string {
	if plainScalar.MatchString(s) {
		switch strings.ToLower(s) {
		case "true", "false", "null", "yes", "no", "on", "off", "y", "n":
			return yamlQuoted(s)
		}
		return s
	}
	return yamlQuoted(s)
}

// yamlQuoted renders a double-quoted YAML scalar.
func yamlQuoted(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// spliceTopLevelBlock replaces a top-level block in a YAML document, or appends
// it when the document has none.
func spliceTopLevelBlock(src []byte, key, block string) []byte {
	text := string(src)
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	if text == "" {
		return []byte(block)
	}

	lines := strings.SplitAfter(text, "\n")
	if last := len(lines) - 1; last >= 0 && lines[last] == "" {
		lines = lines[:last]
	}

	start := -1
	for i, line := range lines {
		// Top-level means column zero: an indented key of the same name belongs
		// to some other mapping and is none of this function's business.
		if strings.HasPrefix(line, key+":") {
			start = i
			break
		}
	}
	if start < 0 {
		return []byte(text + block)
	}

	end := start + 1
	for end < len(lines) {
		bare := strings.TrimRight(lines[end], "\r\n")
		if bare == "" || strings.HasPrefix(bare, " ") || strings.HasPrefix(bare, "\t") {
			end++
			continue
		}
		break
	}
	// Trailing blank lines separate the block from what follows; they belong to
	// the document's layout rather than to the block, so they stay put.
	for end > start+1 {
		if strings.TrimRight(lines[end-1], "\r\n") != "" {
			break
		}
		end--
	}

	var b strings.Builder
	b.WriteString(strings.Join(lines[:start], ""))
	b.WriteString(block)
	b.WriteString(strings.Join(lines[end:], ""))
	return []byte(b.String())
}
