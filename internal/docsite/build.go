// @facet: navigation/documentation/guided-documentation
// @facet-role: primary
package docsite

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"fmt"
	"github.com/facet-framework/core/registry"
	"html/template"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed assets/*
var assets embed.FS

type Page struct {
	URL          string
	Kind         string
	Topics       []TopicView
	Features     []FeatureView
	AllArticles  []Article
	Related      []FeatureView
	FeatureCount int
	Site         Config
	Title        string
	Intro        string
	Audience     *Audience
	Article      *Article
	Audiences    []Audience
	Articles     []Article
	Records      []Evidence
	Plan         Plan
}
type Lock struct {
	Engine          string   `json:"engine"`
	Version         int      `json:"version"`
	InventoryDigest string   `json:"inventory_digest"`
	ConfigDigest    string   `json:"config_digest"`
	Files           []Source `json:"files"`
}

// Build refuses existing destinations. Render in a sibling temporary directory,
// then rename only after every page and the lock are complete.
func Build(root, destination string, c Config) (err error) {
	inv, err := Collect(root)
	if err != nil {
		return err
	}
	p, err := MakePlan(inv, c)
	if err != nil {
		return err
	}
	if c.Title == "" || len(c.Audiences) == 0 {
		return fmt.Errorf("site needs title and at least one audience")
	}
	dest, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	if _, err = os.Lstat(dest); err == nil {
		return fmt.Errorf("destination exists: %s (build to a new directory)", dest)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(dest), ".facet-docs-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err = os.Chmod(stage, 0755); err != nil {
		return err
	}
	catalog := featureCatalog(inv, c)
	topics := topicViews(c, catalog)
	funcs := template.FuncMap{"t": func(key string) string { return translate(c.Language, key) }, "featureAnchor": featureAnchor, "firstFeatures": func(items []FeatureView) []FeatureView {
		if len(items) > 8 {
			return items[:8]
		}
		return items
	}, "firstArticles": func(items []Article) []Article {
		if len(items) > 4 {
			return items[:4]
		}
		return items
	}, "lang": func() string {
		if c.Language == "" {
			return "en"
		}
		return c.Language
	}, "join": strings.Join, "recordLink": recordLink, "pretty": pretty, "answer": answer, "team": team, "scenarioCount": scenarioCount, "provenanceCount": provenanceCount, "metrics": func(r Evidence) []any { return array(object(r.Fields, "observability"), "metrics_emitted") }, "scenarios": func(r Evidence) []any { return array(object(r.Fields, "contract"), "scenarios") }, "tests": func(r Evidence) []facet.Provenance { return (facet.Record{Fields: r.Fields}).Provenances() }, "productHeight": func(d *Diagram) int {
		h := 80
		for _, n := range d.Nodes {
			if n.Y+80 > h {
				h = n.Y + 80
			}
		}
		return h
	}, "nodeLabel": func(d *Diagram, id string) string {
		for _, n := range d.Nodes {
			if n.ID == id {
				return n.Label
			}
		}
		return id
	}, "edgeX": func(d *Diagram, id string) int {
		for _, n := range d.Nodes {
			if n.ID == id {
				return n.X + 110
			}
		}
		return 0
	}, "edgeY": func(d *Diagram, id string) int {
		for _, n := range d.Nodes {
			if n.ID == id {
				return n.Y
			}
		}
		return 0
	}, "edgeBottom": func(d *Diagram, id string) int {
		for _, n := range d.Nodes {
			if n.ID == id {
				return n.Y + 52
			}
		}
		return 0
	}, "lifecycle": func(r Evidence) string {
		if r.Lifecycle != "" {
			return r.Lifecycle
		}
		if r.Category == "features" {
			if _, ok := r.Fields["variants"]; ok {
				return "variant-specific"
			}
			return "lifecycle not declared"
		}
		return r.Category
	}}
	tpl, err := template.New("page.html").Funcs(funcs).ParseFS(assets, "assets/page.html")
	if err != nil {
		return err
	}
	writePage := func(name string, page Page) error {
		page.URL = name
		page.Site = c
		page.Topics = topics
		page.Features = catalog
		page.AllArticles = c.Articles
		page.FeatureCount = len(catalog)
		if page.Article != nil {
			for _, f := range catalog {
				if f.Topic == page.Article.Topic {
					page.Related = append(page.Related, f)
				}
			}
		}
		var buf bytes.Buffer
		if e := tpl.Execute(&buf, page); e != nil {
			return e
		}
		return os.WriteFile(filepath.Join(stage, name), bytes.ReplaceAll(buf.Bytes(), []byte("@"), []byte("&#64;")), 0644)
	}

	base := Page{Site: c, Title: c.Title, Intro: c.Description, Kind: "home", Audiences: c.Audiences, Articles: c.Articles, Plan: p}
	if err = writePage("index.html", base); err != nil {
		return err
	}
	if c.Mode != "reference" {
		for _, aud := range c.Audiences {
			a := aud
			page := Page{Site: c, Title: a.Title, Intro: a.Description, Audience: &a, Plan: p, Articles: []Article{}}
			for _, article := range c.Articles {
				if article.Audience == a.ID {
					page.Articles = append(page.Articles, article)
				}
			}
			sort.SliceStable(page.Articles, func(i, j int) bool {
				if page.Articles[i].Order == page.Articles[j].Order {
					return page.Articles[i].ID < page.Articles[j].ID
				}
				return page.Articles[i].Order < page.Articles[j].Order
			})
			if err = writePage("audience-"+a.ID+".html", page); err != nil {
				return err
			}
		}
	}
	if err = writePage("features.html", Page{Kind: "catalog", Title: translate(c.Language, "features"), Intro: translate(c.Language, "complete")}); err != nil {
		return err
	}
	displayInv := publicInventory(inv, c)
	byID := map[string]Evidence{}
	for _, r := range displayInv.Records {
		byID[r.ID] = r
	}
	for _, article := range c.Articles {
		a := article
		page := Page{Site: c, Title: a.Title, Intro: a.Description, Kind: "article", Article: &a, Plan: p}
		for _, id := range a.Features {
			page.Records = append(page.Records, byID[id])
		}
		if err = writePage("article-"+a.ID+".html", page); err != nil {
			return err
		}
	}
	if err = writePage("sources.html", Page{Site: c, Kind: "sources", Title: translate(c.Language, "sources"), Intro: translate(c.Language, "sourceNote"), Records: displayInv.Records, Plan: p}); err != nil {
		return err
	}
	for _, name := range []string{"style.css", "search.js", "theme.js"} {
		b, e := assets.ReadFile("assets/" + name)
		if e != nil {
			return e
		}
		if e = os.WriteFile(filepath.Join(stage, name), b, 0644); e != nil {
			return e
		}
	}
	for name, v := range map[string]any{"inventory.json": displayInv, "plan.json": publishedPlan(p, c), "search.json": c.Articles} {
		if err = writeJSON(filepath.Join(stage, name), v); err != nil {
			return err
		}
	}
	indexBytes, e := json.Marshal(buildSearch(c, topics, catalog))
	if e != nil {
		return e
	}
	indexBytes = bytes.ReplaceAll(indexBytes, []byte("@"), []byte(`\u0040`))
	indexScript := append([]byte("window.FACET_DOCS_SEARCH = "), indexBytes...)
	indexScript = append(indexScript, []byte(";\n")...)
	if e = os.WriteFile(filepath.Join(stage, "search-index.js"), indexScript, 0644); e != nil {
		return e
	}

	invDigest, e := jsonDigest(inv)
	if e != nil {
		return e
	}
	configDigest, e := jsonDigest(c)
	if e != nil {
		return e
	}
	lock := Lock{Engine: engineDigest(), Version: Version, InventoryDigest: invDigest, ConfigDigest: configDigest, Files: []Source{}}
	entries, err := os.ReadDir(stage)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		sum, e := digest(filepath.Join(stage, entry.Name()))
		if e != nil {
			return e
		}
		lock.Files = append(lock.Files, Source{entry.Name(), sum})
	}
	lockPath := dest + ".lock.json"
	if _, e := os.Lstat(lockPath); !os.IsNotExist(e) {
		return fmt.Errorf("lock destination exists: %s", lockPath)
	}
	lockBytes, e := json.MarshalIndent(lock, "", "  ")
	if e != nil {
		return e
	}
	lockFile, e := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	_, writeErr := lockFile.Write(append(bytes.ReplaceAll(lockBytes, []byte("@"), []byte(`\u0040`)), '\n'))
	closeErr := lockFile.Close()
	if writeErr != nil {
		os.Remove(lockPath)
		return writeErr
	}
	if closeErr != nil {
		os.Remove(lockPath)
		return closeErr
	}
	defer func() {
		if err != nil {
			os.Remove(lockPath)
		}
	}()
	// A second existence check protects against normal competing writers. Rename
	// is atomic on the same filesystem; it never merges directory contents.
	if _, e := os.Lstat(dest); !os.IsNotExist(e) {
		return fmt.Errorf("destination appeared during build: %s", dest)
	}
	return os.Rename(stage, dest)
}
func writeJSON(path string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	return os.WriteFile(path, append(bytes.ReplaceAll(b, []byte("@"), []byte(`\u0040`)), '\n'), 0644)
}
func recordLink(id string) string { return fmt.Sprintf("source-%x", []byte(id)) }
func pretty(v any) string         { b, _ := json.MarshalIndent(v, "", "  "); return string(b) }
func answer(r Evidence, slot string) string {
	return text(object(object(r.Fields, "knowledge"), slot), "answer")
}
func team(r Evidence) string       { return text(object(object(r.Fields, "lineage"), "owner"), "team") }
func scenarioCount(r Evidence) int { return len(array(object(r.Fields, "contract"), "scenarios")) }
func provenanceCount(r Evidence) int {
	n := 0
	for _, kind := range []string{"unit_tests", "property_tests", "integration_tests", "e2e_tests"} {
		n += len(array(object(r.Fields, "verification"), kind))
	}
	return n
}

// Check compares the complete current corpus (including added/removed records
// and anchors), editorial configuration, and rendered file digests.
func Check(root, destination string, c Config) error {
	var pathErr error
	destination, pathErr = filepath.Abs(destination)
	if pathErr != nil {
		return pathErr
	}
	b, e := os.ReadFile(destination + ".lock.json")
	if e != nil {
		return e
	}
	var lock Lock
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.UseNumber()
	if e = decoder.Decode(&lock); e != nil {
		return e
	}
	if lock.Engine != engineDigest() {
		return fmt.Errorf("documentation stale: renderer changed")
	}
	if lock.Version != Version {
		return fmt.Errorf("unsupported lock version")
	}
	inv, e := Collect(root)
	if e != nil {
		return e
	}
	current, e := jsonDigest(inv)
	if e != nil {
		return e
	}
	if current != lock.InventoryDigest {
		return fmt.Errorf("documentation stale: record or anchor inventory changed")
	}
	configDigest, e := jsonDigest(c)
	if e != nil {
		return e
	}
	if configDigest != lock.ConfigDigest {
		return fmt.Errorf("documentation stale: editorial configuration changed")
	}

	entries, e := os.ReadDir(destination)
	if e != nil {
		return e
	}
	if len(entries) != len(lock.Files) {
		return fmt.Errorf("documentation modified: file set changed")
	}
	for _, s := range lock.Files {
		if filepath.Base(s.Path) != s.Path || s.Path == "." || s.Path == ".." {
			return fmt.Errorf("unsafe lock path")
		}
		sum, e := digest(filepath.Join(destination, s.Path))
		if e != nil {
			return e
		}
		if sum != s.Digest {
			return fmt.Errorf("documentation modified: %s", s.Path)
		}
	}

	// Compare a fresh deterministic render as well: the lock records provenance,
	// but is not a signature and must not be the sole authority for output bytes.
	temp, e := os.MkdirTemp("", "facet-docs-check-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(temp)
	expected := filepath.Join(temp, "site")
	if e = Build(root, expected, c); e != nil {
		return e
	}
	expectedFiles, e := os.ReadDir(expected)
	if e != nil {
		return e
	}
	if len(expectedFiles) != len(entries) {
		return fmt.Errorf("documentation modified: rendered file set differs")
	}
	for _, f := range expectedFiles {
		want, e := digest(filepath.Join(expected, f.Name()))
		if e != nil {
			return e
		}
		got, e := digest(filepath.Join(destination, f.Name()))
		if e != nil {
			return e
		}
		if want != got {
			return fmt.Errorf("documentation needs rendering: %s", f.Name())
		}
	}
	return nil
}

func engineDigest() string {
	h := sha256.New()
	h.Write([]byte("docs-site-v1.1"))
	for _, name := range []string{"page.html", "style.css", "search.js"} {
		b, _ := assets.ReadFile("assets/" + name)
		h.Write(b)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func publicInventory(inv Inventory, c Config) Inventory {
	selected := map[string]bool{}
	for _, a := range c.Audiences {
		for _, id := range a.Features {
			selected[id] = true
		}
	}
	out := Inventory{Version: inv.Version, Records: []Evidence{}}
	for _, r := range inv.Records {
		if c.Catalog && r.Category == "features" {
			selected[r.ID] = true
		}
	}
	for _, a := range c.Articles {
		for _, id := range a.Features {
			selected[id] = true
		}
	}
	for _, r := range inv.Records {
		if !selected[r.ID] || (c.Visibility != "internal" && r.Category == "features" && r.Lifecycle != "active") {
			continue
		}
		if c.Visibility != "internal" {
			r.Sources = nil
			r.Anchors = nil
			r.Gaps = nil
			r.Fields = map[string]any{"intent": map[string]any{"summary": r.Summary}}
		}
		out.Records = append(out.Records, r)
	}
	return out
}

func publishedPlan(p Plan, c Config) Plan {
	if c.Visibility == "internal" {
		return p
	}
	p.Gaps = nil
	p.Unassigned = nil
	return p
}

func jsonDigest(v any) (string, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return "", e
	}
	h := sha256.Sum256(b)
	return fmt.Sprintf("%x", h), nil
}
