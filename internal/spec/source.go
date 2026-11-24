package spec

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
)

const (
	segment     = "[a-z][a-z0-9-]*"
	numberedID  = "[0-9]{3}-" + segment
	storyID     = "[0-9]+-[0-9]+-" + segment
	fileSegment = "(" + segment + "|" + numberedID + "|" + storyID + ")"
)

var (
	segmentPattern = regexp.MustCompile("^" + segment + "$")
	numberedSpecID = regexp.MustCompile("^" + numberedID + "$")
	filePattern    = regexp.MustCompile("^" + fileSegment + "(/" + fileSegment + ")*$")
	addressPattern = regexp.MustCompile("^" + fileSegment + "(/" + fileSegment + ")*#" + segment + "$")
	githubRepo     = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
)

type Source struct {
	ID         string   `yaml:"id"`
	Repo       string   `yaml:"repo"`
	Path       string   `yaml:"path"`
	Follow     string   `yaml:"follow"`
	Format     Format   `yaml:"format"`
	Runner     string   `yaml:"runner,omitempty"`
	Registries []string `yaml:"registries"`
}

func (s *Source) UnmarshalYAML(unmarshal func(any) error) error {
	type plain Source
	var value plain
	if err := unmarshal(&value); err != nil {
		return err
	}
	var raw map[string]any
	if err := unmarshal(&raw); err != nil {
		return err
	}
	for key, field := range raw {
		switch key {
		case "runner":
			if name, ok := field.(string); !ok || !segmentPattern.MatchString(name) {
				return fmt.Errorf("spec_sources runner must be a valid runner name")
			}
		case "id", "repo", "path", "follow", "format", "registries":
			if field != nil {
				continue
			}
			return fmt.Errorf("spec_sources field %q must not be null", key)
		default:
			return fmt.Errorf("unknown spec_sources field %q", key)
		}
	}
	if _, hasPath := raw["path"]; hasPath && value.Path == "" && (Source{Format: value.Format}).directory() != "" {
		value.Path = "."
	}
	*s = Source(value)
	return nil
}

func ConfigureSources(sources []Source) error {
	seen := make(map[string]bool)
	for i := range sources {
		s := &sources[i]
		if !segmentPattern.MatchString(s.ID) || seen[s.ID] {
			return fmt.Errorf("invalid or duplicate spec source id %q", s.ID)
		}
		seen[s.ID] = true
		if !validRepo(s.Repo) {
			return fmt.Errorf("spec source %q needs a Git repository URL without embedded credentials", s.ID)
		}
		if s.Path != "" {
			dir := path.Clean(s.Path)
			if strings.HasPrefix(dir, "/") || dir == ".." || strings.HasPrefix(dir, "../") || strings.ContainsAny(s.Path, "\\\x00") {
				return fmt.Errorf("spec source %q path must be inside the specification repository", s.ID)
			}
			s.Path = dir
		}
		if s.Follow == "" {
			s.Follow = "main"
		}
		if !validBranch(s.Follow) {
			return fmt.Errorf("spec source %q follow must name a Git branch", s.ID)
		}
		if s.Format == "" {
			s.Format = FacetSpec
		}
		if _, err := AdapterFor(s.Format); err != nil {
			return fmt.Errorf("spec source %q: %w", s.ID, err)
		}
		if s.Path == "." && (s.Format == FacetSpec || s.Format == Gherkin || s.Format == GherkinMarkdown) {
			s.Path = ""
		}
		if s.Runner != "" && s.Format.Strength() == Provenance {
			return fmt.Errorf("spec source %q cannot configure a runner for %s [provenance]", s.ID, s.Format)
		}
		registries := make(map[string]bool)
		for _, registry := range s.Registries {
			if !segmentPattern.MatchString(registry) || registries[registry] {
				return fmt.Errorf("spec source %q has an invalid or duplicate registry %q", s.ID, registry)
			}
			registries[registry] = true
		}
	}
	return nil
}

func (s Source) directory() string {
	if s.Path == "." {
		return ""
	}
	if s.Path != "" {
		return s.Path
	}
	switch s.Format {
	case OpenSpec:
		return "openspec/specs"
	case SpecKit, Gauge:
		return "specs"
	case Kiro:
		return ".kiro/specs"
	case BMAD:
		return "_bmad-output/implementation-artifacts"
	}
	return ""
}

func validBranch(branch string) bool {
	if strings.HasPrefix(branch, "-") || strings.HasSuffix(branch, ".") ||
		strings.Contains(branch, "..") || strings.Contains(branch, "@{") ||
		strings.ContainsAny(branch, " ~^:?*[\\") {
		return false
	}
	for _, c := range branch {
		if c < 32 || c == 127 {
			return false
		}
	}
	for part := range strings.SplitSeq(branch, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return true
}

func validRepo(repo string) bool {
	if short, ok := strings.CutPrefix(repo, "github:"); ok {
		return githubRepo.MatchString(short)
	}
	if strings.TrimSpace(repo) != repo || strings.ContainsAny(repo, "\r\n\x00") || strings.HasPrefix(repo, "-") {
		return false
	}
	if strings.Contains(repo, "://") {
		u, err := url.Parse(repo)
		if err != nil || u.RawQuery != "" || u.Fragment != "" {
			return false
		}
		if u.User != nil {
			_, hasPassword := u.User.Password()
			return !hasPassword && (u.Scheme == "ssh" || u.Scheme == "git+ssh")
		}
		return u.Host != "" || (u.Scheme == "file" && u.Path != "")
	}
	host, location, ok := strings.Cut(repo, ":")
	return ok && host != "" && location != "" && !strings.HasPrefix(location, ":")
}

func (s Source) gitURL() string {
	if short, ok := strings.CutPrefix(s.Repo, "github:"); ok {
		return "https://github.com/" + short + ".git"
	}
	return s.Repo
}

// Reference splits a scenario or file reference using the specification ID grammar.
func Reference(ref string, scenario bool) (source, file, address string, ok bool) {
	source, target, found := strings.Cut(ref, ":")
	if !found || !segmentPattern.MatchString(source) {
		return "", "", "", false
	}
	if !scenario {
		return source, target, "", filePattern.MatchString(target)
	}
	file, _, _ = strings.Cut(target, "#")
	return source, file, target, addressPattern.MatchString(target)
}
