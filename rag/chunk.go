// @facet: navigation/retrieval/registry-index
// @facet-role: related
package rag

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/facet-framework/core/registry"
	"github.com/goccy/go-yaml"
)

// RecordFacets are the top-level facets chunked out of a record, in the order
// they are indexed.
var RecordFacets = []string{
	"intent",
	"contract",
	"verification",
	"observation",
	"observability",
	"knowledge",
	"lineage",
	"flags",
	"variants",
	"metadata",
}

// MaxChunkChars is the soft size budget for one chunk.
const MaxChunkChars = 1400

// overlapLines is how many lines the next part repeats from the previous one,
// so a promise that straddles a split is whole in at least one chunk.
const overlapLines = 2

// ConfigHint names the configuration file in error messages.
const ConfigHint = facet.ConfigName

// Chunks builds every chunk for one registry: its records, then the generated
// documents its manifest claims.
func Chunks(reg facet.Registry, multi bool) ([]Chunk, error) {
	var out []Chunk

	for _, category := range facet.Categories {
		records, err := reg.LoadCategory(category)
		if err != nil {
			return nil, err
		}
		for _, rec := range records {
			out = append(out, recordChunks(reg, rec, multi)...)
		}
	}

	docs, err := documentChunks(reg)
	if err != nil {
		return nil, err
	}
	return append(out, docs...), nil
}

// recordChunks slices one record into its facets.
func recordChunks(reg facet.Registry, rec facet.Record, multi bool) []Chunk {
	name := rec.ID
	if multi && reg.Name != "" {
		name = reg.Name + ":" + rec.ID
	}
	summary := firstLine(rec.Intent())
	path := reg.Rel(rec.Path)

	var out []Chunk
	for _, facetName := range RecordFacets {
		value, ok := rec.Fields[facetName]
		if !ok || value == nil {
			continue
		}
		body := renderFacet(value)
		if strings.TrimSpace(body) == "" {
			continue
		}

		head := chunkHeader(name, rec.Lifecycle, summary, facetName)
		title := fmt.Sprintf("%s · %s", name, facetName)

		for part, text := range split(head, body) {
			id := fmt.Sprintf("%s#%s", name, facetName)
			if part > 0 {
				id = fmt.Sprintf("%s/%d", id, part)
			}
			out = append(out, Chunk{
				ID:       id,
				Registry: reg.Name,
				Feature:  rec.ID,
				Kind:     "record",
				Facet:    facetName,
				Path:     path,
				Part:     part,
				Title:    title,
				Text:     text,
			})
		}
	}
	return out
}

// chunkHeader is what every chunk starts with.
func chunkHeader(id, lifecycle, summary, facetName string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "feature: %s\n", id)
	if lifecycle != "" {
		fmt.Fprintf(&b, "lifecycle: %s\n", lifecycle)
	}
	if summary != "" && facetName != "intent" {
		fmt.Fprintf(&b, "about: %s\n", summary)
	}
	fmt.Fprintf(&b, "facet: %s\n\n", facetName)
	return b.String()
}

// renderFacet writes a facet's subtree back out as YAML.
func renderFacet(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	data, err := yaml.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(data)
}

// split cuts a body into parts under the size budget, each carrying the header.
func split(head, body string) []string {
	body = strings.TrimRight(body, "\n")
	budget := MaxChunkChars - len(head)
	if budget < 200 {
		budget = 200
	}
	if len(body) <= budget {
		return []string{head + body}
	}

	lines := strings.Split(body, "\n")
	var parts []string
	var current []string
	size := 0

	flush := func() {
		if len(current) == 0 {
			return
		}
		parts = append(parts, head+strings.Join(current, "\n"))
		keep := overlapLines
		if keep > len(current) {
			keep = len(current)
		}
		current = append([]string(nil), current[len(current)-keep:]...)
		size = 0
		for _, l := range current {
			size += len(l) + 1
		}
	}

	for _, line := range lines {
		if size+len(line)+1 > budget && len(current) > 0 {
			flush()
		}
		current = append(current, line)
		size += len(line) + 1
	}
	if len(current) > 0 {
		parts = append(parts, head+strings.Join(current, "\n"))
	}
	return parts
}

// documentChunks indexes the generated articles the manifest claims.
func documentChunks(reg facet.Registry) ([]Chunk, error) {
	manifest, err := facet.LoadManifest(reg.ManifestPath())
	if err != nil {
		return nil, err
	}
	artifacts := append([]facet.Artifact(nil), manifest.Artifacts...)
	sort.Slice(artifacts, func(i, j int) bool {
		return artifacts[i].Subject.Path < artifacts[j].Subject.Path
	})

	var out []Chunk
	for _, artifact := range artifacts {
		rel := filepath.ToSlash(artifact.Subject.Path)
		data, err := os.ReadFile(filepath.Join(reg.Root, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		body := strings.TrimSpace(string(data))
		if body == "" {
			continue
		}

		var head strings.Builder
		fmt.Fprintf(&head, "document: %s\n", rel)
		if artifact.Feature != "" {
			fmt.Fprintf(&head, "generated from feature: %s\n", artifact.Feature)
		}
		if len(artifact.Slots) > 0 {
			fmt.Fprintf(&head, "knowledge slots: %s\n", strings.Join(artifact.Slots, ", "))
		}
		head.WriteString("facet: article\n\n")

		for part, text := range split(head.String(), body) {
			id := "doc:" + rel
			if part > 0 {
				id = fmt.Sprintf("%s/%d", id, part)
			}
			out = append(out, Chunk{
				ID:       id,
				Registry: reg.Name,
				Feature:  artifact.Feature,
				Kind:     "doc",
				Facet:    "article",
				Path:     rel,
				Part:     part,
				Title:    rel,
				Text:     text,
			})
		}
	}
	return out, nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}
