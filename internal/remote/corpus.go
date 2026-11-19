// Package remote implements the published, read-only integration boundary.
//
// @facet: navigation/interfaces/remote-read-access
// @facet-role: primary
package remote

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/facet-framework/core/registry"
	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
)

const MaxFileBytes = 4 << 20
const MaxCorpusBytes = 64 << 20

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*/[a-z][a-z0-9-]*/[a-z][a-z0-9-]*$`)
var registryPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
var categories = map[string]bool{"features": true, "assumptions": true, "policies": true, "data-entities": true}

type Publication struct {
	SchemaVersion int             `json:"schema_version"`
	GitRevision   string          `json:"git_revision,omitempty"`
	Files         []PublishedFile `json:"files"`
}
type PublishedFile struct {
	Path     string `json:"path"`
	SHA256   string `json:"sha256"`
	Kind     string `json:"kind"`
	Registry string `json:"registry"`
}
type Record struct {
	Snapshot string `json:"snapshot"`
	ID       string `json:"id"`
	Registry string `json:"registry"`
	Category string `json:"category"`
	Path     string `json:"path"`
	YAML     string `json:"yaml"`
	summary  string
	sections []recordSection
}
type Corpus struct {
	Snapshot string
	records  []Record
	sources  map[string]string
	files    map[string]PublishedFile
	anchors  []facet.Anchor
}

// LoadPublication accepts configuration, not an automatically discovered manifest.
func LoadPublication(r io.Reader) (Publication, error) {
	var p Publication
	raw, err := io.ReadAll(io.LimitReader(r, 1<<20+1))
	if err != nil || len(raw) > 1<<20 {
		return p, errors.New("oversized publication manifest")
	}
	if _, err := strictJSON(raw); err != nil {
		return p, errors.New("invalid publication JSON")
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return p, errors.New("invalid publication manifest")
	}
	if dec.Decode(new(any)) != io.EOF {
		return p, errors.New("multiple manifest values")
	}
	return p, nil
}

// LoadCorpus takes a content-addressed snapshot. Only these verified bytes are
// subsequently read; repository settings and environment overrides are ignored.
func LoadCorpus(root string, publication Publication) (*Corpus, error) {
	if publication.SchemaVersion != 1 || len(publication.Files) == 0 || len(publication.Files) > 10000 {
		return nil, errors.New("invalid publication version or file count")
	}
	entries := append([]PublishedFile(nil), publication.Files...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	canonical, _ := json.Marshal(struct {
		Version int             `json:"schema_version"`
		Files   []PublishedFile `json:"files"`
	}{1, entries})
	hash := sha256.Sum256(canonical)
	c := &Corpus{Snapshot: hex.EncodeToString(hash[:]), sources: map[string]string{}, files: map[string]PublishedFile{}}
	confined, err := os.OpenRoot(root)
	if err != nil {
		return nil, errors.New("cannot open publication root")
	}
	defer confined.Close()
	total := 0
	identities := map[string]bool{}
	for _, entry := range entries {
		if !validPath(entry.Path) || (entry.Registry != "" && !registryPattern.MatchString(entry.Registry)) || (!categories[entry.Kind] && entry.Kind != "source") {
			return nil, errors.New("invalid publication entry")
		}
		if _, ok := c.files[entry.Path]; ok {
			return nil, errors.New("duplicate publication path")
		}
		digest, err := hex.DecodeString(entry.SHA256)
		if err != nil || len(digest) != sha256.Size || strings.ToLower(entry.SHA256) != entry.SHA256 {
			return nil, errors.New("invalid publication hash")
		}
		parts := strings.Split(entry.Path, "/")
		for i := range parts {
			info, err := confined.Lstat(strings.Join(parts[:i+1], "/"))
			if err != nil || info.Mode()&os.ModeSymlink != 0 {
				return nil, errors.New("publication path unavailable or symlink")
			}
			if i == len(parts)-1 && !info.Mode().IsRegular() {
				return nil, errors.New("publication entry is not a regular file")
			}
		}
		f, err := confined.OpenFile(entry.Path, readFlags, 0)
		if err != nil {
			return nil, errors.New("cannot read publication entry")
		}
		info, statErr := f.Stat()
		if statErr != nil || !info.Mode().IsRegular() || info.Size() > MaxFileBytes {
			f.Close()
			return nil, errors.New("publication entry invalid or too large")
		}
		data, readErr := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
		f.Close()
		if readErr != nil || len(data) > MaxFileBytes {
			return nil, errors.New("publication entry read failed or too large")
		}
		total += len(data)
		if total > MaxCorpusBytes {
			return nil, errors.New("publication exceeds corpus limit")
		}
		actual := sha256.Sum256(data)
		if hex.EncodeToString(actual[:]) != entry.SHA256 {
			return nil, errors.New("publication hash mismatch")
		}
		if !utf8.Valid(data) || strings.ContainsRune(string(data), 0) || strings.HasPrefix(string(data), "\ufeff") {
			return nil, errors.New("publication requires UTF-8 text without NUL or BOM")
		}
		c.files[entry.Path] = entry
		if entry.Kind == "source" {
			c.sources[entry.Path] = string(data)
			for _, a := range facet.ScanContent(entry.Path, data) {
				if a.Registry == "" {
					a.Registry = entry.Registry
				}
				c.anchors = append(c.anchors, a)
			}
			continue
		}
		fields, err := recordFields(data)
		if err != nil {
			return nil, err
		}
		id, _ := fields["id"].(string)
		if !idPattern.MatchString(id) {
			return nil, errors.New("invalid published record id")
		}
		if entry.Kind == "assumptions" && !strings.HasPrefix(id, "assumption/") {
			return nil, errors.New("invalid assumption id")
		}
		key := entry.Registry + ":" + entry.Kind + ":" + id
		if identities[key] {
			return nil, errors.New("duplicate published record identity")
		}
		identities[key] = true
		summary := ""
		if intent, ok := fields["intent"].(map[string]any); ok {
			summary, _ = intent["summary"].(string)
		}
		sections, err := splitRecordSections(string(data))
		if err != nil {
			return nil, err
		}
		c.records = append(c.records, Record{sections: sections, Snapshot: c.Snapshot, ID: id, Registry: entry.Registry, Category: entry.Kind, Path: entry.Path, YAML: string(data), summary: strings.TrimSpace(summary)})
	}
	sort.Slice(c.records, func(i, j int) bool {
		a, b := c.records[i], c.records[j]
		return a.Registry+":"+a.Category+":"+a.ID < b.Registry+":"+b.Category+":"+b.ID
	})
	return c, nil
}

func validPath(p string) bool {
	if !fs.ValidPath(p) || p == "." || strings.ContainsAny(p, "\\\x00%") || path.Clean(p) != p {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		lower := strings.ToLower(part)
		if strings.HasPrefix(lower, ".") || strings.HasSuffix(lower, ".pem") || strings.HasSuffix(lower, ".key") || strings.HasSuffix(lower, ".p12") || strings.HasSuffix(lower, ".pfx") || lower == "credentials" || lower == "secrets" || slices.Contains([]string{"id_rsa", "id_ed25519", "id_ecdsa", "credentials.json", "kubeconfig"}, lower) || slices.Contains([]string{".crt", ".cer", ".der", ".jks", ".keystore", ".ppk", ".gpg", ".asc", ".tfstate"}, path.Ext(lower)) {
			return false
		}
	}
	return true
}

type yamlGuard struct{ err error }

func (g *yamlGuard) Visit(n ast.Node) ast.Visitor {
	if g.err != nil {
		return nil
	}
	switch v := n.(type) {
	case *ast.AnchorNode, *ast.AliasNode, *ast.TagNode, *ast.DirectiveNode, *ast.MergeKeyNode:
		g.err = errors.New("unsupported YAML alias, tag or directive")
		return nil
	case *ast.MappingValueNode:
		if _, ok := v.Key.(*ast.StringNode); !ok {
			g.err = errors.New("record mapping keys must be strings")
			return nil
		}
	}
	return g
}
func recordFields(data []byte) (map[string]any, error) {
	tree, err := parser.ParseBytes(data, 0)
	if err != nil || len(tree.Docs) != 1 {
		return nil, errors.New("invalid or multiple YAML documents")
	}
	guard := &yamlGuard{}
	ast.Walk(guard, tree.Docs[0])
	if guard.err != nil {
		return nil, guard.err
	}
	var fields map[string]any
	if yaml.Unmarshal(data, &fields) != nil || fields == nil {
		return nil, errors.New("invalid YAML record")
	}
	return fields, nil
}

func (c *Corpus) record(id, kind string) (Record, error) {
	if kind == "" {
		kind = "features"
	}
	if !categories[kind] {
		return Record{}, problem(422, "invalid_selection")
	}
	registry, bare, qualified := "", id, false
	if i := strings.IndexByte(id, ':'); i >= 0 {
		registry, bare, qualified = id[:i], id[i+1:], true
		if registry != "" && !registryPattern.MatchString(registry) {
			return Record{}, problem(422, "invalid_selection")
		}
	}
	if !idPattern.MatchString(bare) {
		return Record{}, problem(422, "invalid_selection")
	}
	var found *Record
	for i := range c.records {
		r := &c.records[i]
		if r.ID != bare || r.Category != kind || (qualified && r.Registry != registry) {
			continue
		}
		if found != nil {
			return Record{}, problem(422, "ambiguous_id")
		}
		found = r
	}
	if found == nil {
		return Record{}, problem(404, "not_found")
	}
	return *found, nil
}

type Problem struct {
	Status    int    `json:"status"`
	Code      string `json:"code"`
	Title     string `json:"title"`
	Type      string `json:"type"`
	RequestID string `json:"request_id"`
	Hint      string `json:"hint,omitempty"`
}

func (p *Problem) Error() string { return p.Code }
func problem(status int, code string) *Problem {
	return &Problem{Status: status, Code: code, Title: strings.ReplaceAll(code, "_", " "), Type: "urn:facet:problem:" + code}
}
func qualified(r Record) string {
	if r.Registry == "" {
		return ":" + r.ID
	}
	return fmt.Sprintf("%s:%s", r.Registry, r.ID)
}
