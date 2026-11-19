// Lint rules an installed plugin brings with it, run by `facet lint`.
package facet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

// PluginLintTool is the reserved tool name a plugin declares to contribute
// lint rules.
const PluginLintTool = "facet.lint"

// pluginLintTimeout is the budget one plugin's lint call gets. A variable so
// a test can shrink it; nothing else assigns it.
var pluginLintTimeout = 30 * time.Second

// Limits on what is handed to a plugin as text. Chosen, not measured. They
// bound the request the host writes; SidecarMaxFrame separately bounds the
// reply it reads, so a plugin's findings must fit in 8 MiB.
const (
	pluginLintMaxFileBytes  = RepoReadMaxBytes // per file, the same cap repo.read has
	pluginLintMaxTotalBytes = 32 << 20
	pluginLintMaxFiles      = 5000
)

// pluginLintAnswer is what the lint tool returns as its result.
type pluginLintAnswer struct {
	Findings []struct {
		FID      string `json:"fid"`
		Category string `json:"category"`
		Severity string `json:"severity"`
		Message  string `json:"message"`
	} `json:"findings"`
}

// RunPluginLint calls the lint tool of every plugin installed under
// pluginsDir that declares it, against reg, and returns their findings. An
// empty pluginsDir, a missing directory and a directory of plugins without
// "facet.lint" all return nil — `facet lint` then behaves exactly as it did before.
//
// @facet: registry/integrity/plugin-lint
// @facet-role: primary
func RunPluginLint(reg Registry, pluginsDir string) []Finding {
	if pluginsDir == "" {
		return nil
	}
	plugins, err := InstalledPlugins(pluginsDir)
	if err != nil {
		return []Finding{{
			Severity: SeverityError,
			Check:    "plugin-lint",
			Path:     pluginsDir,
			Message:  fmt.Sprintf("the plugin directory could not be read, so no plugin lint ran: %v", err),
		}}
	}
	broker := NewPluginBroker(pluginsDir)
	defer broker.StopAll()

	var out []Finding
	for _, p := range plugins {
		if p.VerifyError != nil {
			// VerifyPackage returns a zero manifest on any failure, so the
			// tools are read from the file itself: a plugin that does not
			// verify must still be reported if it would have linted.
			if !declaresLintTool(filepath.Join(pluginsDir, p.ID)) {
				continue
			}
			out = append(out, pluginFinding(p.ID, SeverityError, "", "",
				fmt.Sprintf("lint did not run, the package does not verify: %v", p.VerifyError)))
			continue
		}
		if !names(p.Manifest.Tools, PluginLintTool) {
			continue
		}
		out = append(out, runOnePluginLint(reg, broker, p)...)
	}
	return out
}

// declaresLintTool reads only the "tools" field of a plugin's plugin.json.
func declaresLintTool(dir string) bool {
	root := dir
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		root = resolved
	}
	raw, err := os.ReadFile(filepath.Join(root, "plugin.json"))
	if err != nil {
		return false
	}
	var m struct {
		Tools []string `json:"tools"`
	}
	if json.Unmarshal(raw, &m) != nil {
		return false
	}
	return names(m.Tools, PluginLintTool)
}

func pluginFinding(pluginID string, sev Severity, fid, category, msg string) Finding {
	label := "[plugin " + pluginID + "] "
	if category != "" {
		label += category + ": "
	}
	return Finding{
		Severity: sev,
		Check:    "plugin-lint:" + pluginID,
		ID:       fid,
		Message:  label + msg,
	}
}

func runOnePluginLint(reg Registry, broker *PluginBroker, p InstalledPlugin) []Finding {
	fail := func(format string, args ...any) []Finding {
		return []Finding{pluginFinding(p.ID, SeverityError, "", "", fmt.Sprintf(format, args...))}
	}

	// Asked before anything is read or started: an ungranted tool is the
	// user's decision, said as a Warning rather than an Error or silence.
	if err := broker.Allowed(p.ID, PluginLintTool); err != nil {
		if errors.Is(err, ErrToolNotGranted) {
			return []Finding{pluginFinding(p.ID, SeverityWarning, "", "",
				"declares lint rules, but the tool \""+PluginLintTool+"\" is not granted, so they did not run (grant it with `facet plugin add --replace --grant tools="+PluginLintTool+"`)")}
		}
		return fail("lint did not run: %v", err)
	}

	request, notes, err := buildPluginLintRequest(reg, p)
	if err != nil {
		return fail("the lint request could not be built: %v", err)
	}
	var out []Finding
	for _, n := range notes {
		out = append(out, pluginFinding(p.ID, SeverityWarning, "", "", n))
	}

	answer, err := callPluginLint(broker, p.ID, request)
	if err != nil {
		return append(out, fail("%v", err)...)
	}
	for _, f := range answer.Findings {
		sev := SeverityError
		msg := f.Message
		switch f.Severity {
		case "Error":
		case "Warning":
			sev = SeverityWarning
		case "Info":
			// The report knows errors and warnings only. An Info is shown as a
			// warning that says so, rather than being dropped.
			sev = SeverityWarning
			msg = "(info) " + msg
		default:
			// An unknown severity is the plugin's fault, not a reason to
			// downgrade the finding or to lose it.
			msg = fmt.Sprintf("(the plugin gave the unknown severity %q) %s", f.Severity, msg)
		}
		out = append(out, pluginFinding(p.ID, sev, f.FID, f.Category, msg))
	}
	return out
}

// callPluginLint sends one lint call through the broker and decodes the
// answer. Every failure mode — a binary that does not verify or start, a
// reply with an error, a result that is not the expected shape, and the time
// limit — is an error here; the caller turns it into an Error finding.
func callPluginLint(broker *PluginBroker, id string, request json.RawMessage) (pluginLintAnswer, error) {
	var answer pluginLintAnswer
	ctx, cancel := context.WithTimeout(context.Background(), pluginLintTimeout)
	defer cancel()
	reply, err := broker.Call(ctx, id, PluginLintTool, request)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return answer, fmt.Errorf("lint did not finish within %s", pluginLintTimeout)
		}
		return answer, fmt.Errorf("lint did not run: %w", err)
	}
	if reply.Error != "" {
		return answer, fmt.Errorf("lint failed: %s", reply.Error)
	}
	if len(reply.Result) == 0 {
		return answer, errors.New("lint answered with no result")
	}
	if err := json.Unmarshal(reply.Result, &answer); err != nil {
		return answer, fmt.Errorf("lint answered with something that is not {findings: [...]}: %v", err)
	}
	if answer.Findings == nil {
		// Present and empty is a clean run; absent is an answer nobody gave.
		var probe map[string]json.RawMessage
		if json.Unmarshal(reply.Result, &probe) != nil || probe["findings"] == nil {
			return answer, errors.New("lint answered without a findings list")
		}
	}
	return answer, nil
}

// buildPluginLintRequest assembles {records, files}. files holds only what the
// manifest's `reads` directories contain; a file over the caps is left out and
// reported in notes, because handing a rule a quietly incomplete input would
// let it pass for the wrong reason.
func buildPluginLintRequest(reg Registry, p InstalledPlugin) (json.RawMessage, []string, error) {
	records := []any{}
	for _, cat := range Categories {
		recs, err := reg.LoadCategory(cat)
		if err != nil {
			return nil, nil, fmt.Errorf("records of %s could not be loaded: %w", cat, err)
		}
		for _, rec := range recs {
			records = append(records, toJSONValue(rec.Fields))
		}
	}

	files := map[string]string{}
	var notes []string
	var total int64
	for _, decl := range p.Manifest.Reads {
		exts := decl.Exts()
		abs, err := ResolveReadDir(reg.Root, decl.Dir)
		if err != nil {
			return nil, nil, err
		}
		walkErr := filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return nil
				}
				return err
			}
			if strings.HasPrefix(d.Name(), ".") && path != abs {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			// Regular files only: WalkDir does not follow symlinks, and a
			// link is neither a directory entered nor a file read.
			if d.IsDir() || !d.Type().IsRegular() {
				return nil
			}
			rel, err := filepath.Rel(reg.Root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			// Only the extensions declared for this directory — the same
			// files repo.read would give the plugin, never more.
			if !slices.Contains(exts, filepath.Ext(rel)) {
				return nil
			}
			if _, seen := files[rel]; seen {
				return nil
			}
			if len(files) >= pluginLintMaxFiles {
				notes = append(notes, fmt.Sprintf("%s was not given to the plugin: more than %d files are declared under reads", rel, pluginLintMaxFiles))
				return nil
			}
			f, err := openBeneath(reg.Root, rel)
			if err != nil {
				notes = append(notes, fmt.Sprintf("%s was not given to the plugin: %v", rel, err))
				return nil
			}
			defer f.Close()
			body, err := io.ReadAll(io.LimitReader(f, pluginLintMaxFileBytes+1))
			if err != nil {
				return err
			}
			if len(body) > pluginLintMaxFileBytes {
				notes = append(notes, fmt.Sprintf("%s was not given to the plugin: it is above %d bytes", rel, pluginLintMaxFileBytes))
				return nil
			}
			if total+int64(len(body)) > pluginLintMaxTotalBytes {
				notes = append(notes, fmt.Sprintf("%s was not given to the plugin: the files exceed %d bytes together", rel, pluginLintMaxTotalBytes))
				return nil
			}
			total += int64(len(body))
			files[rel] = string(body)
			return nil
		})
		if walkErr != nil {
			return nil, nil, walkErr
		}
	}
	sort.Strings(notes)

	raw, err := json.Marshal(map[string]any{"records": records, "files": files})
	if err != nil {
		return nil, nil, err
	}
	return raw, notes, nil
}
