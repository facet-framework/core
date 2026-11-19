// @facet: navigation/documentation/guided-documentation
package docsite

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

type FeatureView struct {
	Evidence    Evidence
	Title       string
	Description string
	URL         string
	Topic       string
	Knowledge   []KnowledgeView
}
type KnowledgeView struct {
	Title string
	Body  string
	Kind  string
}
type TopicView struct {
	Topic    Topic
	URL      string
	Articles []Article
	Features []FeatureView
}
type SearchEntry struct {
	Lifecycle   string `json:"lifecycle,omitempty"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Text        string `json:"text"`
	URL         string `json:"url"`
	Category    string `json:"category"`
	Kind        string `json:"kind"`
}

func featureAnchor(id string) string {
	sum := sha256.Sum256([]byte(id))
	return fmt.Sprintf("feature-%x", sum[:10])
}
func humanTitle(id string) string {
	parts := strings.Split(id, "/")
	s := strings.ReplaceAll(parts[len(parts)-1], "-", " ")
	r := []rune(s)
	if len(r) > 0 {
		r[0] = unicode.ToUpper(r[0])
	}
	return string(r)
}
func firstParagraph(s string) string { return strings.TrimSpace(strings.Split(s, "\n\n")[0]) }
func featureCatalog(inv Inventory, c Config) []FeatureView {
	selected := map[string]bool{}
	for _, a := range c.Audiences {
		for _, id := range a.Features {
			selected[id] = true
		}
	}
	for _, a := range c.Articles {
		for _, id := range a.Features {
			selected[id] = true
		}
	}
	out := []FeatureView{}
	for _, r := range inv.Records {
		if r.Category != "features" || (!c.Catalog && !selected[r.ID]) || (c.Visibility != "internal" && r.Lifecycle != "active") {
			continue
		}
		label := c.FeatureLabels[r.ID]
		if label.Title == "" {
			label.Title = humanTitle(r.ID)
		}
		if label.Description == "" {
			label.Description = firstParagraph(r.Summary)
		}
		topic := "other"
		for _, t := range c.Topics {
			matched := false
			for _, prefix := range t.Prefixes {
				if strings.HasPrefix(r.ID, prefix) || strings.HasPrefix(strings.SplitN(r.ID, ":", 2)[len(strings.SplitN(r.ID, ":", 2))-1], prefix) {
					matched = true
					break
				}
			}
			if matched {
				topic = t.ID
				break
			}
		}
		f := FeatureView{Evidence: r, Title: label.Title, Description: label.Description, URL: "features.html#" + featureAnchor(r.ID), Topic: topic, Knowledge: []KnowledgeView{}}
		for _, slot := range []struct{ key, label string }{{"prerequisite_knowledge", "prerequisites"}, {"discoverability", "entry"}, {"not_for", "boundaries"}, {"common_pitfalls", "pitfalls"}, {"success_signal", "success"}} {
			if body := answer(r, slot.key); strings.TrimSpace(body) != "" {
				f.Knowledge = append(f.Knowledge, KnowledgeView{Title: slot.label, Body: body, Kind: slot.key})
			}
		}
		if c.Visibility != "internal" {
			clean := publicInventory(Inventory{Version: Version, Records: []Evidence{r}}, Config{Audiences: []Audience{{Features: []string{r.ID}}}})
			if len(clean.Records) == 0 {
				continue
			}
			f.Evidence = clean.Records[0]
			if c.FeatureLabels[r.ID].Description == "" {
				f.Description = firstParagraph(f.Evidence.Summary)
			}
			// Public catalog content is the reviewed summary/label; raw Knowledge
			// answers require explicit inclusion in an editorial article.
			f.Knowledge = nil
		}
		out = append(out, f)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Title == out[j].Title {
			return out[i].Evidence.ID < out[j].Evidence.ID
		}
		return out[i].Title < out[j].Title
	})
	return out
}
func topicViews(c Config, features []FeatureView) []TopicView {
	out := []TopicView{}
	topics := append([]Topic{}, c.Topics...)
	hasOther := false
	for _, a := range c.Articles {
		if a.Topic == "other" {
			hasOther = true
		}
	}
	for _, f := range features {
		if f.Topic == "other" {
			hasOther = true
		}
	}
	hasOtherTopic := false
	for _, t := range topics {
		if t.ID == "other" {
			hasOtherTopic = true
		}
	}
	if hasOther && !hasOtherTopic {
		topics = append(topics, Topic{ID: "other", Title: translate(c.Language, "otherTitle"), Description: translate(c.Language, "otherDescription")})
	}
	for _, t := range topics {
		v := TopicView{Topic: t, URL: "features.html#topic-" + t.ID, Articles: []Article{}, Features: []FeatureView{}}
		for _, a := range c.Articles {
			if a.Topic == t.ID {
				v.Articles = append(v.Articles, a)
			}
		}
		sort.SliceStable(v.Articles, func(i, j int) bool { return v.Articles[i].Order < v.Articles[j].Order })
		if len(v.Articles) > 0 {
			v.URL = "article-" + v.Articles[0].ID + ".html"
		}
		for _, f := range features {
			if f.Topic == t.ID {
				v.Features = append(v.Features, f)
			}
		}
		out = append(out, v)
	}
	return out
}
func buildSearch(c Config, topics []TopicView, features []FeatureView) []SearchEntry {
	out := []SearchEntry{}
	labels := map[string]string{}
	for _, t := range topics {
		labels[t.Topic.ID] = t.Topic.Title
	}
	for _, a := range c.Articles {
		texts := []string{a.Title, a.Description, a.Category, strings.Join(a.Tags, " ")}
		for _, s := range a.Sections {
			texts = append(texts, s.Title, s.Body, s.Code, strings.Join(s.Items, " "))
			if s.Table != nil {
				for _, row := range s.Table.Rows {
					texts = append(texts, strings.Join(row, " "))
				}
			}
		}
		out = append(out, SearchEntry{Title: a.Title, Description: a.Description, Text: strings.Join(texts, " "), URL: "article-" + a.ID + ".html", Category: labels[a.Topic], Kind: "article"})
	}
	for _, f := range features {
		texts := []string{f.Title, f.Description, f.Evidence.ID, f.Evidence.Summary}
		for _, k := range f.Knowledge {
			texts = append(texts, k.Body)
		}
		out = append(out, SearchEntry{Title: f.Title, Description: f.Description, Text: strings.Join(texts, " "), URL: f.URL, Category: labels[f.Topic], Kind: "feature", Lifecycle: f.Evidence.Lifecycle})
	}
	return out
}

// translate returns the site text for key in the site's language. English is
// the default; a key missing from the German table falls back to English, and
// a key missing from both comes back as itself.
func translate(language, key string) string {
	if language == "de" {
		if v, ok := siteTextDE[key]; ok {
			return v
		}
	}
	if v, ok := siteTextEN[key]; ok {
		return v
	}
	return key
}
