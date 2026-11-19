// @facet: navigation/documentation/guided-documentation
// @facet-role: related
package docsite

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

// Compose creates conservative drafts from declared summaries and populated
// Knowledge answers only. It never treats anchored implementation as a new,
// reviewed answer, and never turns a scenario into an invented walkthrough.
func Compose(inv Inventory, c Config) (Config, error) {
	if _, e := MakePlan(inv, c); e != nil {
		return c, e
	}
	if len(c.Articles) > 0 {
		return c, fmt.Errorf("compose requires an empty articles list; preserve existing editorial content")
	}
	if c.Mode == "reference" {
		return composeReference(inv, c)
	}

	records := map[string]Evidence{}
	for _, r := range inv.Records {
		records[r.ID] = r
	}
	slots := []struct{ key, title string }{{"prerequisite_knowledge", "Before you begin"}, {"discoverability", "Where to start"}, {"not_for", "When to use another approach"}, {"common_pitfalls", "Avoid common mistakes"}, {"success_signal", "Check your result"}}
	for _, aud := range c.Audiences {
		ordered, e := dependencyOrder(aud.Features, records)
		if e != nil {
			return c, e
		}
		for i, id := range ordered {
			r := records[id]
			if r.Category != "features" {
				continue
			}
			// Planned and variant-specific behavior is evidence for planning, not a
			// promise that a customer can perform a task today.
			if r.Lifecycle != "active" {
				continue
			}
			hash := sha256.Sum256([]byte(aud.ID + ":" + id))
			name := strings.ReplaceAll(strings.Split(id, ":")[len(strings.Split(id, ":"))-1], "/", " · ")
			a := Article{ID: fmt.Sprintf("guide-%x", hash[:8]), Title: name, Kind: "feature", Audience: aud.ID, Category: r.Topic, Tags: []string{r.Topic}, Features: []string{id}, Sections: []Section{}, Order: i + 1}
			if strings.TrimSpace(r.Summary) != "" {
				a.Sections = append(a.Sections, Section{Title: "What you can do", Body: r.Summary, Sources: []string{id}})
			}
			for _, slot := range slots {
				if body := answer(r, slot.key); strings.TrimSpace(body) != "" {
					a.Sections = append(a.Sections, Section{Title: slot.title, Body: body, Sources: []string{id}})
				}
			}
			if len(a.Sections) > 0 {
				c.Articles = append(c.Articles, a)
			}
			faq := Article{ID: fmt.Sprintf("faq-%x", hash[:8]), Title: "Questions about " + name, Kind: "faq", Audience: aud.ID, Category: r.Topic, Tags: []string{"faq", r.Topic}, Features: []string{id}, Sections: []Section{}, Order: 1000 + i}
			for _, slot := range slots {
				v := object(object(r.Fields, "knowledge"), slot.key)
				q := text(v, "question")
				body := text(v, "answer")
				if q != "" && strings.TrimSpace(body) != "" {
					faq.Sections = append(faq.Sections, Section{Title: q, Body: body, Sources: []string{id}})
				}
			}
			if len(faq.Sections) > 0 {
				c.Articles = append(c.Articles, faq)
			}
		}
	}
	return c, nil
}

func composeReference(inv Inventory, c Config) (Config, error) {
	features := featureCatalog(inv, c)
	for _, topic := range topicViews(c, features) {
		a := Article{ID: "topic-" + topic.Topic.ID, Title: topic.Topic.Title, Description: topic.Topic.Description, Kind: "reference", Topic: topic.Topic.ID, Category: topic.Topic.Title, Features: []string{}, Sections: []Section{}}
		for _, f := range topic.Features {
			if f.Evidence.Lifecycle != "active" {
				continue
			}
			if strings.TrimSpace(f.Evidence.Summary) == "" {
				continue
			}
			a.Features = append(a.Features, f.Evidence.ID)
			a.Sections = append(a.Sections, Section{Title: f.Title, Body: f.Evidence.Summary, Sources: []string{f.Evidence.ID}})
		}
		if len(a.Sections) > 0 {
			c.Articles = append(c.Articles, a)
		}
	}
	return c, nil
}
