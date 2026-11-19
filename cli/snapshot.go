package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/facet-framework/core/corpus"
	"github.com/spf13/cobra"
)

// snapshotFormat names the envelope, not the corpus inside it. The corpus is
// whatever corpus.Build produces; the envelope only adds where it came from,
// so a consumer can refuse an envelope it does not understand without having
// to guess from the shape of the payload.
const snapshotFormat = "facet-snapshot/1"

// snapshotOrigin says where a frozen corpus came from and when.
type snapshotOrigin struct {
	GeneratedAt     string `json:"generatedAt"`
	Commit          string `json:"commit,omitempty"`
	Branch          string `json:"branch,omitempty"`
	Remote          string `json:"remote,omitempty"`
	Registry        string `json:"registry,omitempty"`
	FacetVersion    string `json:"facetVersion"`
	ExcerptsOmitted bool   `json:"excerptsOmitted,omitempty"`
}

// snapshotEnvelope is what 'facet snapshot' writes and what a consumer of the
// snapshot ingests.
type snapshotEnvelope struct {
	Format string         `json:"format"`
	Origin snapshotOrigin `json:"origin"`
	Corpus *corpus.Corpus `json:"corpus"`
}

// newSnapshotCmd freezes the registry's corpus into one file.
//
// @facet: delivery/distribution/registry-snapshot
// @facet-role: primary
func newSnapshotCmd(version string) *cobra.Command {
	var out, registry string
	var noExcerpts bool

	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: "Freeze this repository's registry corpus into one JSON file",
		Long: `Builds the registry corpus — records, anchors, taxonomy, glossary,
calibration — and writes it to one file, wrapped in an envelope that names
the commit, branch and remote it was taken from.

The file is meant for a pipeline: a consumer that ingests it sees the
registry as of this commit.

A snapshot is not source-free. Every anchor carries the first source line
below it, and the scan lists every file it read. '--no-excerpts' drops the
source lines; the file list stays, because a consumer's coverage figures are
built from it. The local checkout path is never written.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			root := ResolveRoot(cmd)
			cfg, err := configForCommand(cmd)
			if err != nil {
				return err
			}
			reg, err := cfg.Select(registry)
			if err != nil {
				return err
			}
			if !reg.Adopted() {
				return fmt.Errorf("this project has no FACET records under %s — add records there or use --root to select a project with records", reg.Rel(reg.RecordsDir))
			}

			corpus, err := corpus.Build(reg, nil)
			if err != nil {
				return err
			}
			// The scan root is an absolute path on the machine that built the
			// snapshot. It means nothing anywhere else and says something about
			// that machine nobody asked to publish.
			corpus.Scan.Root = ""
			if noExcerpts {
				for id, anchors := range corpus.Scan.Anchors {
					for i := range anchors {
						anchors[i].Excerpt = ""
					}
					corpus.Scan.Anchors[id] = anchors
				}
			}

			head := gitOutput(root, "rev-parse", "HEAD")
			env := snapshotEnvelope{
				Format: snapshotFormat,
				Origin: snapshotOrigin{
					GeneratedAt:     time.Now().UTC().Format(time.RFC3339),
					Commit:          head,
					Branch:          gitOutput(root, "rev-parse", "--abbrev-ref", "HEAD"),
					Remote:          redactRemote(gitOutput(root, "remote", "get-url", "origin")),
					Registry:        reg.Name,
					FacetVersion:    version,
					ExcerptsOmitted: noExcerpts,
				},
				Corpus: corpus,
			}

			body, err := json.Marshal(env)
			if err != nil {
				return err
			}
			if out == "" || out == "-" {
				_, err = cmd.OutOrStdout().Write(body)
				return err
			}
			if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(out, body, 0o644); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "wrote %s (%d bytes, %d records, commit %s)\n",
				out, len(body), len(corpus.Records), shortSHA(head))
			return nil
		},
	}
	cmd.Flags().StringVarP(&out, "out", "o", "", "file to write ('-' or empty for stdout)")
	cmd.Flags().StringVar(&registry, "registry", "", "registry to freeze, in a repository that declares several")
	cmd.Flags().BoolVar(&noExcerpts, "no-excerpts", false, "drop the source line quoted under every anchor")
	return cmd
}

// gitOutput runs one git query in root and returns its trimmed output, or ""
// when git is absent or the root is not a checkout. Absent is reported as
// absent: the envelope then has no commit, which is true.
func gitOutput(root string, args ...string) string {
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// redactRemote drops credentials an https remote may carry
// (https://user:token@host/…). A snapshot is uploaded to a third party; a
// token in the remote URL would travel with it.
func redactRemote(remote string) string {
	if i := strings.Index(remote, "://"); i >= 0 {
		rest := remote[i+3:]
		if at := strings.Index(rest, "@"); at >= 0 && at < strings.Index(rest+"/", "/") {
			return remote[:i+3] + rest[at+1:]
		}
	}
	return remote
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	if sha == "" {
		return "unknown"
	}
	return sha
}
