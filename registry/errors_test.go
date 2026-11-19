package facet

import (
	"strings"
	"testing"
)

func recordWith(id, summary string, statements []string, pitfalls ...string) Record {
	var scenarios []any
	for i, s := range statements {
		scenarios = append(scenarios, map[string]any{
			"id":        "scenario-" + string(rune('a'+i)),
			"statement": s,
		})
	}
	fields := map[string]any{
		"intent": map[string]any{"summary": summary},
		"contract": map[string]any{
			"scenarios": scenarios,
		},
	}
	if len(pitfalls) > 0 {
		fields["knowledge"] = map[string]any{
			"common_pitfalls": map[string]any{
				"answer": strings.Join(pitfalls, "\n"),
			},
		}
	}
	return Record{ID: id, Category: "features", Fields: fields}
}

// The three fields the error-code register scans are exactly what gets scanned: nothing
// else on the record contributes a mention.
func TestExtractErrorCodesScansIntentContractAndKnowledge(t *testing.T) {
	rec := recordWith("mail/dkim/key-rotation",
		"Rotates DKIM keys; fails with E5501 when the zone is unreachable.",
		[]string{"Given a due rotation\nWhen the cron fires\nThen it returns E5502 on timeout"},
		"A common mistake is ignoring E5503 in the logs.")

	reg, err := ExtractErrorCodes([]Record{rec}, "")
	if err != nil {
		t.Fatal(err)
	}

	byCode := map[string]ErrorCode{}
	for _, ec := range reg.Codes {
		byCode[ec.Code] = ec
	}
	for _, code := range []string{"E5501", "E5502", "E5503"} {
		if _, ok := byCode[code]; !ok {
			t.Fatalf("want %s found, got codes %+v", code, reg.Codes)
		}
	}

	wantField := map[string]string{
		"E5501": "intent.summary",
		"E5502": "contract.scenarios",
		"E5503": "knowledge.common_pitfalls",
	}
	for code, field := range wantField {
		ec := byCode[code]
		if len(ec.Mentions) != 1 || ec.Mentions[0].Field != field {
			t.Errorf("%s: want one mention from %q, got %+v", code, field, ec.Mentions)
		}
		if ec.Mentions[0].Feature != "mail/dkim/key-rotation" {
			t.Errorf("%s: mention lost its record, got %+v", code, ec.Mentions[0])
		}
	}
}

// A record that only ever names a code in prose that is not one of the three
// scanned fields — here the scenario id, never the statement — contributes
// nothing. Only the three fields the error-code register scans are read.
func TestExtractErrorCodesIgnoresFieldsOutsideTheThree(t *testing.T) {
	rec := Record{
		ID: "crypto/secrets/rotate",
		Fields: map[string]any{
			"id":     "E1099-not-a-real-field-scan",
			"intent": map[string]any{"summary": "Rotates encryption keys on a schedule."},
			"contract": map[string]any{
				"scenarios": []any{
					map[string]any{"id": "E1099", "statement": "Given a key\nThen it rotates"},
				},
			},
		},
	}
	reg, err := ExtractErrorCodes([]Record{rec}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Codes) != 0 {
		t.Fatalf("want nothing found — E1099 only appears in the scenario id and the record's own id, neither of which is scanned; got %+v", reg.Codes)
	}
}

// The same code named twice inside one record — its own repeated mentions —
// is one ErrorCode with one feature, not a collision the way two different
// records sharing a code would be.
func TestExtractErrorCodesDedupesFeaturesButKeepsEveryMention(t *testing.T) {
	rec := recordWith("shop/sync/queue",
		"Retries a sync job and gives up with E2010 after the limit.",
		[]string{"Then it also logs E2010 for the operator dashboard"})

	reg, err := ExtractErrorCodes([]Record{rec}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Codes) != 1 {
		t.Fatalf("want one distinct code, got %+v", reg.Codes)
	}
	ec := reg.Codes[0]
	if len(ec.Mentions) != 2 {
		t.Errorf("want both mentions kept, got %+v", ec.Mentions)
	}
	if got := ec.Features(); len(got) != 1 || got[0] != "shop/sync/queue" {
		t.Errorf("want exactly one feature, got %v", got)
	}
}

// Error-code bands group by the letter and first digit, such as E1xxx or E2xxx.
func TestExtractErrorCodesGroupsByBand(t *testing.T) {
	records := []Record{
		recordWith("crypto/keys/rotate", "Fails with E1001.", nil),
		recordWith("crypto/keys/derive", "Fails with E1002.", nil),
		recordWith("shop/sync/queue", "Fails with E2500.", nil),
	}
	reg, err := ExtractErrorCodes(records, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Bands) != 2 {
		t.Fatalf("want 2 bands, got %+v", reg.Bands)
	}
	byBand := map[string]CodeBand{}
	for _, b := range reg.Bands {
		byBand[b.Band] = b
	}
	if len(byBand["E1"].Codes) != 2 {
		t.Errorf("want 2 codes in E1, got %+v", byBand["E1"])
	}
	if len(byBand["E2"].Codes) != 1 {
		t.Errorf("want 1 code in E2, got %+v", byBand["E2"])
	}
}

// The question the register exists to answer: what is the next free code in
// a band, and which ones in between were never assigned.
func TestExtractErrorCodesFindsGapsAndTheNextFreeCode(t *testing.T) {
	records := []Record{
		recordWith("crypto/keys/rotate", "Fails with E1001.", nil),
		recordWith("crypto/keys/derive", "Fails with E1003.", nil),
		recordWith("crypto/keys/wrap", "Fails with E1006.", nil),
	}
	reg, err := ExtractErrorCodes(records, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Bands) != 1 {
		t.Fatalf("want one band, got %+v", reg.Bands)
	}
	b := reg.Bands[0]
	wantGaps := []string{"E1002", "E1004", "E1005"}
	if len(b.Gaps) != len(wantGaps) {
		t.Fatalf("gaps = %v, want %v", b.Gaps, wantGaps)
	}
	for i, g := range wantGaps {
		if b.Gaps[i] != g {
			t.Errorf("gap[%d] = %q, want %q", i, b.Gaps[i], g)
		}
	}
	if b.Next != "E1007" {
		t.Errorf("next = %q, want E1007", b.Next)
	}
}

// A single assigned code has no gap either side of it to report, but does
// have a next free code — the span is a single point, not a range.
func TestExtractErrorCodesSingleCodeBandHasNoGapsButHasNext(t *testing.T) {
	reg, err := ExtractErrorCodes([]Record{recordWith("crypto/keys/rotate", "Fails with E1050.", nil)}, "")
	if err != nil {
		t.Fatal(err)
	}
	b := reg.Bands[0]
	if len(b.Gaps) != 0 {
		t.Errorf("want no gaps for a single code, got %v", b.Gaps)
	}
	if b.Next != "E1051" {
		t.Errorf("next = %q, want E1051", b.Next)
	}
}

// A fixed-width code cannot grow another digit while still matching the
// configured convention. Until a rollover is explicitly defined, the
// register must leave the recommendation absent at the boundary.
func TestExtractErrorCodesOmitsNextAtFixedWidthMaximum(t *testing.T) {
	reg, err := ExtractErrorCodes([]Record{recordWith("audit/log/review", "Fails with E9999.", nil)}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Bands) != 1 {
		t.Fatalf("want one band, got %+v", reg.Bands)
	}
	if got := reg.Bands[0].Next; got != "" {
		t.Fatalf("next = %q; no valid E\\d{4} successor exists after E9999", got)
	}
}

// The pattern is a project convention, not a FACET concept — a project using
// a different identifier shape gets the same view once it configures it.
func TestExtractErrorCodesPatternIsConfigurable(t *testing.T) {
	rec := recordWith("billing/payments/webhook", "Fails with ERR-042 on a bad signature. E9999 does not match this pattern.", nil)

	reg, err := ExtractErrorCodes([]Record{rec}, `ERR-\d{3}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Codes) != 1 || reg.Codes[0].Code != "ERR-042" {
		t.Fatalf("want only ERR-042 found with the configured pattern, got %+v", reg.Codes)
	}
}

// An empty configured pattern uses the default E-prefixed numeric convention.
func TestExtractErrorCodesEmptyPatternUsesTheDefault(t *testing.T) {
	rec := recordWith("billing/payments/webhook", "Fails with E8042.", nil)
	reg, err := ExtractErrorCodes([]Record{rec}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Codes) != 1 || reg.Codes[0].Code != "E8042" {
		t.Fatalf("want E8042 found via the default pattern, got %+v", reg.Codes)
	}
}

func TestExtractErrorCodesRejectsAnInvalidPattern(t *testing.T) {
	_, err := ExtractErrorCodes(nil, `E\d{4}(`)
	if err == nil {
		t.Fatal("want an error for an unparsable regular expression")
	}
}

// A match with no digits at all cannot be banded or gap-checked, but it is
// still a found identifier — it just carries no Band and is absent from the
// per-band summary rather than crashing the extractor.
func TestExtractErrorCodesWithoutDigitsHasNoBand(t *testing.T) {
	rec := recordWith("audit/log/export", "Tagged INCIDENT in the export.", nil)
	reg, err := ExtractErrorCodes([]Record{rec}, `INCIDENT`)
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Codes) != 1 || reg.Codes[0].Band != "" {
		t.Fatalf("want one bandless code, got %+v", reg.Codes)
	}
	if len(reg.Bands) != 0 {
		t.Errorf("a bandless code must not produce a band summary, got %+v", reg.Bands)
	}
}

// Output is sorted so two runs over the same registry are byte-identical —
// FACET spec 6.3.
func TestExtractErrorCodesOutputIsSorted(t *testing.T) {
	records := []Record{
		recordWith("crypto/keys/rotate", "Fails with E1050 and E1002 and E2001.", nil),
	}
	reg, err := ExtractErrorCodes(records, "")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, ec := range reg.Codes {
		got = append(got, ec.Code)
	}
	want := []string{"E1002", "E1050", "E2001"}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("codes = %v, want %v", got, want)
		}
	}
}

// Markdown around a code is display, not identifier. The extractor matches
// the pattern against the field's text and never parses Markdown, so a code
// in a span, a code in a fenced block, and a code in plain prose are one
// code — which is what a register wants, because they are one code in the
// source too.
func TestExtractErrorCodesIgnoresMarkdownAroundACode(t *testing.T) {
	rec := recordWith("crypto/keys/rotate",
		"Inline span: `E1001`.\n\n```\nreturn Err(E1001)\n```\n\nPlain prose: E1001.",
		nil)

	reg, err := ExtractErrorCodes([]Record{rec}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Codes) != 1 || reg.Codes[0].Code != "E1001" {
		t.Fatalf("a span, a fence, and plain prose must yield one code, got %+v", reg.Codes)
	}
	if got := len(reg.Codes[0].Mentions); got != 3 {
		t.Errorf("every occurrence stays a mention, want 3, got %d", got)
	}
}

// Next free considers only scanned fields, so it may recommend a code named
// only in deviation.reason. This boundary keeps recommendations scoped to
// the fields the register actually measures.
func TestNextFreeCodeCanNameACodeOnlyDeviationReasonAssigns(t *testing.T) {
	rec := Record{
		ID:       "crypto/keys/rotate",
		Category: "features",
		Fields: map[string]any{
			"intent": map[string]any{"summary": "Fails with E1001, E1002, E1003."},
			"contract": map[string]any{
				"scenarios": []any{
					map[string]any{
						"id":        "locked-store",
						"statement": "Given a locked store\nThen it fails with E1004",
						"deviation": map[string]any{
							"reason":     "The handler surfaces E1005 instead of E1004 — wrong branch.",
							"tracked_as": "crypto/keys/derive",
						},
					},
				},
			},
		},
	}

	reg, err := ExtractErrorCodes([]Record{rec}, "")
	if err != nil {
		t.Fatal(err)
	}

	for _, ec := range reg.Codes {
		if ec.Code == "E1005" {
			t.Fatalf("deviation.reason is not a scanned field; E1005 must not appear: %+v", reg.Codes)
		}
	}
	if len(reg.Codes) != 4 {
		t.Fatalf("want the four codes from scanned fields, got %+v", reg.Codes)
	}
	if len(reg.Bands) != 1 {
		t.Fatalf("want one band, got %+v", reg.Bands)
	}
	// The next-free recommendation ignores identifiers present only in
	// deviation.reason, so it may recommend such an identifier.
	if got := reg.Bands[0].Next; got != "E1005" {
		t.Fatalf("next = %q; the register computes 'next free' from scanned fields only, and this fixture's next is the code deviation.reason spent", got)
	}
}
