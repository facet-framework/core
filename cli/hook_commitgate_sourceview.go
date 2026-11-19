package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/facet-framework/core/internal/sourceview"
	"github.com/goccy/go-yaml"
)

// commitViewContext freezes local bindings and external commits for both sides
// of the staged comparison. Scratch repositories have their own HEAD and index.
//
// @facet: authoring/guardrails/commit-gate
// @facet-role: related
// @facet: registry/layout/source-views
// @facet-role: related
type commitViewContext struct {
	root      string
	selected  string
	local     *sourceview.LocalConfig
	revisions map[string]string
	external  map[string]string
	scratch   []string
}

func (c *commitViewContext) close() {
	for _, p := range c.scratch {
		os.RemoveAll(p)
	}
}
func (c *commitViewContext) resolve(snapshot, tree string, cfg sourceview.Config) (*sourceview.ResolvedView, error) {
	if c.local == nil {
		tracked, err := commitGit(c.root, "ls-files", "--", sourceview.LocalName)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(tracked) != "" {
			return nil, fmt.Errorf("%s must be ignored, not tracked", sourceview.LocalName)
		}
		ignored := exec.Command("git", "-C", c.root, "check-ignore", "--quiet", "--", sourceview.LocalName).Run()
		if ignored != nil {
			return nil, fmt.Errorf("%s must be ignored", sourceview.LocalName)
		}
		data, err := os.ReadFile(filepath.Join(c.root, sourceview.LocalName))
		if err != nil {
			return nil, err
		}
		var local sourceview.LocalConfig
		if err = yaml.Unmarshal(data, &local); err != nil {
			return nil, err
		}
		c.local = &local
	}
	if c.revisions == nil {
		c.revisions = map[string]string{}
	}
	original, err := sourceview.ResolveCommittedBindings(c.root, cfg, *c.local, c.selected, c.revisions)
	if err != nil {
		return nil, err
	}
	root, err := filepath.EvalSymlinks(c.root)
	if err != nil {
		return nil, err
	}
	local := sourceview.LocalConfig{Bindings: map[string]sourceview.Binding{}}
	// Copy declaration maps before removing refs for scratch resolution. Ref
	// identity was checked against the original checkout above.
	frozenCfg := cfg
	frozenCfg.Views = map[string]sourceview.View{}
	for name, view := range cfg.Views {
		frozenCfg.Views[name] = view
	}
	selections := map[string]sourceview.Selection{}
	for id, selection := range cfg.Views[original.Name].Sources {
		selection.Ref = ""
		selections[id] = selection
	}
	frozenCfg.Views[original.Name] = sourceview.View{Sources: selections}
	for _, source := range original.Ordered() {
		dest := snapshot
		if source.Root != root {
			dest = c.external[source.Root]
			if dest == "" {
				dest, err = os.MkdirTemp("", "facet-commit-source-")
				if err != nil {
					return nil, err
				}
				c.scratch = append(c.scratch, dest)
				if err = materializeCommitTree(source.Root, source.Revision, dest); err != nil {
					return nil, err
				}
				if err = initializeCommitSnapshot(dest, source.Root, source.Revision); err != nil {
					return nil, err
				}
				c.external[source.Root] = dest
			}
		}
		local.Bindings[source.Binding] = sourceview.Binding{Repository: source.Repository, Checkout: dest}
	}
	if err = initializeCommitSnapshot(snapshot, c.root, tree); err != nil {
		return nil, err
	}
	result, err := sourceview.ResolveWithBindings(snapshot, frozenCfg, local, original.Name)
	if err != nil {
		return nil, err
	}
	for id, source := range result.Sources {
		source.Ref = original.Sources[id].Ref
		result.Sources[id] = source
	}
	return result, nil
}
func initializeCommitSnapshot(root, original, tree string) error {
	// An alternate object store is read-only input. The new repository owns its
	// index and synthetic HEAD; no checkout/clean filter ever handles its blobs.
	cmd := exec.Command("git", "-C", root, "-c", "core.hooksPath=/dev/null", "-c", "init.templateDir=", "init", "--quiet")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("initialize snapshot: %w (%s)", err, out)
	}
	// Local attributes override both committed and global conversion rules.
	// SourceView's consistency checks use git status; it must compare raw blobs
	// without invoking a configured clean/process filter or normalizing bytes.
	if err := os.MkdirAll(filepath.Join(root, ".git/info"), 0700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, ".git/info/attributes"), []byte("* -filter -text -ident -working-tree-encoding\n"), 0600); err != nil {
		return err
	}
	if _, err := commitGit(root, "config", "core.hooksPath", "/dev/null"); err != nil {
		return err
	}
	if _, err := commitGit(root, "config", "core.fsmonitor", "false"); err != nil {
		return err
	}
	objects, err := commitGit(original, "rev-parse", "--git-path", "objects")
	if err != nil {
		return err
	}
	objects = strings.TrimSpace(objects)
	if !filepath.IsAbs(objects) {
		objects = filepath.Join(original, objects)
	}
	if err = os.MkdirAll(filepath.Join(root, ".git/objects/info"), 0700); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(root, ".git/objects/info/alternates"), []byte(objects+"\n"), 0600); err != nil {
		return err
	}
	if _, err = commitGit(root, "read-tree", tree); err != nil {
		return err
	}
	commit := exec.Command("git", "-C", root, "-c", "user.name=FACET", "-c", "user.email=facet@localhost", "-c", "commit.gpgsign=false", "commit-tree", tree+"^{tree}", "-m", "immutable commit snapshot")
	raw, err := commit.CombinedOutput()
	if err != nil {
		return fmt.Errorf("create snapshot HEAD: %w (%s)", err, raw)
	}
	_, err = commitGit(root, "-c", "core.hooksPath=/dev/null", "update-ref", "HEAD", strings.TrimSpace(string(raw)))
	return err
}
