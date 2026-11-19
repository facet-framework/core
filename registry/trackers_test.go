package facet

import (
	"path/filepath"
	"testing"
)

func trackerConfig(t *testing.T, body string) *Config {
	t.Helper()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ConfigName), body)
	cfg, err := LoadConfig(root)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	return cfg
}

const twoTrackers = `schema_version: "0.1"
trackers:
  - id: jira
    pattern: '^[A-Z][A-Z0-9_]+-[0-9]+$'
    url: https://example.atlassian.net/browse/{key}
    default: true
  - id: github
    pattern: '^(?P<repo>[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)#(?P<number>[0-9]+)$'
    url: https://github.com/{repo}/issues/{number}
`

// A key is assigned to the tracker whose pattern it matches, and the link is
// built from that tracker's template — named groups included.
func TestATicketIsAssignedByItsPattern(t *testing.T) {
	trackers := trackerConfig(t, twoTrackers).Trackers()
	cases := []struct{ key, tracker, url string }{
		{"KAN-12", "jira", "https://example.atlassian.net/browse/KAN-12"},
		{"example-org/library#12", "github", "https://github.com/example-org/library/issues/12"},
	}
	for _, c := range cases {
		got := ResolveTicket(trackers, c.key, "")
		if got.Tracker != c.tracker || got.URL != c.url {
			t.Errorf("%s: got tracker %q url %q, want %q %q", c.key, got.Tracker, got.URL, c.tracker, c.url)
		}
	}
}

// An explicit tracker on the reference wins over pattern matching: a project
// with two Jira instances cannot tell them apart by key.
func TestAnExplicitTrackerWinsOverThePattern(t *testing.T) {
	trackers := trackerConfig(t, twoTrackers+`  - id: jira-legacy
    pattern: '^OLD-[0-9]+$'
    url: https://legacy.example.com/browse/{key}
`).Trackers()
	got := ResolveTicket(trackers, "KAN-12", "jira-legacy")
	if got.Tracker != "jira-legacy" || got.URL != "https://legacy.example.com/browse/KAN-12" {
		t.Errorf("got %+v, want jira-legacy", got)
	}
}

// A key no pattern matches falls to the default tracker, and says so: an
// agent must be able to tell a recognised key from a guess.
func TestAnUnmatchedKeyFallsToTheDefaultAndSaysSo(t *testing.T) {
	trackers := trackerConfig(t, twoTrackers).Trackers()
	got := ResolveTicket(trackers, "not a key", "")
	if got.Tracker != "jira" || !got.ByDefault {
		t.Errorf("got %+v, want the default tracker, marked as default", got)
	}
}

// Without trackers nothing is resolved and nothing is invented: no tracker,
// no link.
func TestWithoutTrackersAKeyStaysUnresolved(t *testing.T) {
	got := ResolveTicket(trackerConfig(t, `schema_version: "0.1"`+"\n").Trackers(), "KAN-12", "")
	if got.Tracker != "" || got.URL != "" {
		t.Errorf("got %+v, want nothing resolved", got)
	}
}

// A pattern that does not compile is a configuration error, reported at load
// time rather than turning into a tracker that never matches.
func TestABrokenPatternIsAConfigError(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ConfigName), `schema_version: "0.1"
trackers:
  - id: jira
    pattern: '^[A-Z'
    url: https://example.atlassian.net/browse/{key}
`)
	if _, err := LoadConfig(root); err == nil {
		t.Fatal("a pattern that does not compile was accepted")
	}
}

// trackers is a new key on a schema whose top level is additionalProperties:
// false. The precondition: a project with trackers lints clean against the
// shipped schema. Then the shape is enforced there too.
func TestTrackersConfigPassesTheRealSchema(t *testing.T) {
	schemaErrors := func(body string) []Finding {
		root := project(t, body)
		copySchemas(t, root)
		rep, err := LintAll(root, LintOptions{Skip: map[string]bool{"knowledge": true}})
		if err != nil {
			t.Fatal(err)
		}
		var out []Finding
		for _, f := range rep.Findings {
			if f.Check == "schema" || f.Check == "config" {
				out = append(out, f)
			}
		}
		return out
	}
	if errs := schemaErrors(twoTrackers); len(errs) > 0 {
		t.Fatalf("precondition: trackers must validate against the shipped schema, got %+v", errs)
	}
	// A tracker id outside the slug grammar is refused by the schema.
	bad := `schema_version: "0.1"
trackers:
  - id: Jira Cloud
    pattern: '^[A-Z]+-[0-9]+$'
`
	if errs := schemaErrors(bad); len(errs) == 0 {
		t.Error("a tracker id with spaces and capitals was accepted")
	}
}
