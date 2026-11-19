package remote

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"unicode/utf8"
)

type Input struct {
	ID       string   `json:"id,omitempty"`
	Kind     string   `json:"kind,omitempty"`
	Query    string   `json:"query,omitempty"`
	Limit    int      `json:"limit,omitempty"`
	Cursor   string   `json:"cursor,omitempty"`
	Sections []string `json:"sections,omitempty"`
	Path     string   `json:"path,omitempty"`
	Start    int      `json:"start,omitempty"`
	End      int      `json:"end,omitempty"`
}
type Summary struct {
	ID          string `json:"id"`
	Registry    string `json:"registry"`
	Category    string `json:"category"`
	Summary     string `json:"summary"`
	QualifiedID string `json:"qualified_id"`
}
type RecordList struct {
	Snapshot   string    `json:"snapshot"`
	Records    []Summary `json:"records"`
	NextCursor *string   `json:"next_cursor"`
}
type Section struct {
	Name  string  `json:"name"`
	Bytes int     `json:"bytes"`
	Lines int     `json:"lines"`
	Text  *string `json:"text"`
}
type Sections struct {
	Category string    `json:"category"`
	Snapshot string    `json:"snapshot"`
	ID       string    `json:"id"`
	Registry string    `json:"registry"`
	Sections []Section `json:"sections"`
}
type Anchor struct {
	ID       string `json:"id"`
	Registry string `json:"registry"`
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Role     string `json:"role"`
}
type Anchors struct {
	Category string   `json:"category"`
	Snapshot string   `json:"snapshot"`
	Anchors  []Anchor `json:"anchors"`
}
type Source struct {
	Snapshot string `json:"snapshot"`
	Path     string `json:"path"`
	Start    int    `json:"start"`
	End      int    `json:"end"`
	Text     string `json:"text"`
}
type cursor struct {
	Snapshot string
	Query    string
	Kind     string
	Limit    int
	Offset   int
}
type Reader struct {
	corpus    *Corpus
	cursorKey [32]byte
}

func NewReader(c *Corpus) *Reader {
	r := &Reader{corpus: c}
	if _, err := rand.Read(r.cursorKey[:]); err != nil {
		panic(err)
	}
	return r
}
func (r *Reader) find(in Input) (RecordList, error) {
	if in.Kind == "" {
		in.Kind = "features"
	}
	if !categories[in.Kind] || utf8.RuneCountInString(in.Query) > 4096 {
		return RecordList{}, problem(422, "invalid_selection")
	}
	if in.Limit == 0 {
		in.Limit = 25
	}
	if in.Limit < 1 || in.Limit > 100 {
		return RecordList{}, problem(422, "invalid_selection")
	}
	q := strings.ToLower(strings.TrimSpace(in.Query))
	queryHash := sha256.Sum256([]byte(q))
	queryDigest := base64.RawURLEncoding.EncodeToString(queryHash[:])
	offset := 0
	if in.Cursor != "" {
		if len(in.Cursor) > 2048 {
			return RecordList{}, problem(400, "invalid_cursor")
		}
		raw, err := base64.RawURLEncoding.DecodeString(in.Cursor)
		if err != nil || len(raw) <= 32 {
			return RecordList{}, problem(400, "invalid_cursor")
		}
		payload, signature := raw[:len(raw)-32], raw[len(raw)-32:]
		mac := hmac.New(sha256.New, r.cursorKey[:])
		mac.Write(payload)
		if !hmac.Equal(signature, mac.Sum(nil)) {
			return RecordList{}, problem(400, "invalid_cursor")
		}
		var cur cursor
		if json.Unmarshal(payload, &cur) != nil {
			return RecordList{}, problem(400, "invalid_cursor")
		}
		if cur.Snapshot != r.corpus.Snapshot {
			return RecordList{}, problem(400, "invalid_cursor")
		}
		if cur.Query != queryDigest || cur.Kind != in.Kind || cur.Limit != in.Limit || cur.Offset < 0 {
			return RecordList{}, problem(400, "invalid_cursor")
		}
		offset = cur.Offset
	}
	out := RecordList{Snapshot: r.corpus.Snapshot, Records: []Summary{}}
	matches := []Record{}
	for _, rec := range r.corpus.records {
		if rec.Category == in.Kind && (q == "" || strings.Contains(strings.ToLower(qualified(rec)+" "+rec.summary), q)) {
			matches = append(matches, rec)
		}
	}
	if offset > len(matches) {
		return RecordList{}, problem(400, "invalid_cursor")
	}
	end := min(offset+in.Limit, len(matches))
	for _, rec := range matches[offset:end] {
		out.Records = append(out.Records, Summary{rec.ID, rec.Registry, rec.Category, rec.summary, qualified(rec)})
	}
	if end < len(matches) {
		payload, _ := json.Marshal(cursor{r.corpus.Snapshot, queryDigest, in.Kind, in.Limit, end})
		mac := hmac.New(sha256.New, r.cursorKey[:])
		mac.Write(payload)
		value := base64.RawURLEncoding.EncodeToString(append(payload, mac.Sum(nil)...))
		out.NextCursor = &value
	}
	return out, nil
}
func (r *Reader) sections(in Input) (Sections, error) {
	rec, err := r.corpus.record(in.ID, in.Kind)
	if err != nil {
		return Sections{}, err
	}
	out := Sections{Category: rec.Category, Snapshot: r.corpus.Snapshot, ID: rec.ID, Registry: rec.Registry, Sections: []Section{}}
	outline := rec.sections
	if len(in.Sections) == 0 {
		for _, s := range outline {
			out.Sections = append(out.Sections, Section{Name: s.Name, Bytes: s.End - s.Start, Lines: s.Lines})
		}
		return out, nil
	}
	if len(in.Sections) > 16 {
		return Sections{}, problem(422, "invalid_selection")
	}
	seen := map[string]bool{}
	for _, name := range in.Sections {
		if len(name) > 128 || seen[name] {
			return Sections{}, problem(422, "invalid_selection")
		}
		seen[name] = true
		var selected *recordSection
		for i := range outline {
			if outline[i].Name == name {
				selected = &outline[i]
				break
			}
		}
		if selected == nil {
			return Sections{}, problem(422, "unknown_section")
		}
		content := rec.YAML[selected.Start:selected.End]
		out.Sections = append(out.Sections, Section{Name: name, Bytes: selected.End - selected.Start, Lines: selected.Lines, Text: &content})
	}
	return out, nil
}
func (r *Reader) anchors(in Input) (Anchors, error) {
	rec, err := r.corpus.record(in.ID, in.Kind)
	if err != nil {
		return Anchors{}, err
	}
	out := Anchors{Category: rec.Category, Snapshot: r.corpus.Snapshot, Anchors: []Anchor{}}
	for _, a := range r.corpus.anchors {
		if a.ID == rec.ID && a.Registry == rec.Registry {
			out.Anchors = append(out.Anchors, Anchor{a.ID, a.Registry, a.Path, a.Line, a.Role})
		}
	}
	return out, nil
}
func (r *Reader) source(in Input) (Source, error) {
	text, ok := r.corpus.sources[in.Path]
	if !ok {
		return Source{}, problem(404, "not_found")
	}
	if in.Start < 1 || in.End < in.Start || in.End-in.Start+1 > 200 {
		return Source{}, problem(422, "range_invalid")
	}
	start, end := -1, -1
	for line, pos := 1, 0; pos < len(text); line++ {
		next := strings.IndexByte(text[pos:], '\n')
		if next < 0 {
			next = len(text)
		} else {
			next += pos + 1
		}
		if line == in.Start {
			start = pos
		}
		if line == in.End {
			end = next
			break
		}
		pos = next
	}
	if start < 0 || end < 0 {
		return Source{}, problem(422, "range_invalid")
	}
	result := text[start:end]

	if len(result) > 65536 {
		return Source{}, problem(422, "response_too_large")
	}
	return Source{r.corpus.Snapshot, in.Path, in.Start, in.End, result}, nil
}
