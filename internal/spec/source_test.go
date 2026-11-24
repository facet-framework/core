package spec

import (
	"strings"
	"testing"
)

func TestSpecSourceConfiguration(t *testing.T) {
	sources := []Source{{ID: "product", Repo: "github:spec-org/specs", Path: "specs/"}}
	if err := ConfigureSources(sources); err != nil {
		t.Fatal(err)
	}
	source := sources[0]
	if source.gitURL() != "https://github.com/spec-org/specs.git" ||
		source.Format != FacetSpec || source.Follow != "main" || source.Path != "specs" {
		t.Fatalf("unexpected defaults or GitHub URL: %+v", source)
	}
	for _, tc := range []struct {
		name, reason string
		change       func(*Source)
	}{
		{"id", "invalid or duplicate", func(s *Source) { s.ID = "../other" }},
		{"path", "path must be inside", func(s *Source) { s.Path = "specs/../../outside" }},
		{"branch", "must name a Git branch", func(s *Source) { s.Follow = "main:refs/heads/other" }},
		{"format", "unsupported spec format", func(s *Source) { s.Format = "unknown" }},
		{"credentials", "without embedded credentials", func(s *Source) { s.Repo = "https://token@example.invalid/specs" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			modified := source
			tc.change(&modified)
			err := ConfigureSources([]Source{modified})
			if err == nil || !strings.Contains(err.Error(), tc.reason) || strings.Contains(err.Error(), "token@example") {
				t.Fatalf("unexpected validation error: %v", err)
			}
		})
	}
	if err := ConfigureSources([]Source{source, source}); err == nil {
		t.Fatal("duplicate source ID accepted")
	}
}

func TestSpecSelectiveUpdateRefusesAChangedSourceIdentity(t *testing.T) {
	address := "pricing/discounts#cap"
	before := LockedSource{Repo: "file:///specs", Follow: "main", Format: FacetSpec,
		Files: map[string]LockedFile{}, Scenarios: map[string]LockedScenario{address: {Commit: "old"}}}
	for name, change := range map[string]func(*LockedSource){
		"follow": func(s *LockedSource) { s.Follow = "release" },
		"repo":   func(s *LockedSource) { s.Repo = "file:///other" },
		"format": func(s *LockedSource) { s.Format = Gherkin },
	} {
		t.Run(name, func(t *testing.T) {
			current := LockedSource{Repo: before.Repo, Follow: before.Follow, Format: before.Format,
				Files: map[string]LockedFile{"pricing/discounts": {}}, Scenarios: map[string]LockedScenario{address: {Commit: "new"}}}
			if _, err := pinSelected(before, current, map[string]bool{address: false}); err != nil {
				t.Fatalf("unchanged identity refused: %v", err)
			}
			change(&current)
			if _, err := pinSelected(before, current, map[string]bool{address: false}); err == nil || !strings.Contains(err.Error(), "full update is required") {
				t.Fatalf("changed %s accepted: %v", name, err)
			}
		})
	}
}
