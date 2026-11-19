// Package facet — identifier register.
//
// @facet: registry/integrity/error-code-register
// @facet-role: primary
package facet

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
)

// DefaultErrorCodePattern matches E followed by exactly four digits, with the
// first digit identifying a band. It is configurable; this naming convention is
// not a FACET requirement.
const DefaultErrorCodePattern = `E\d{4}`

// scannedFields names the three record facets ExtractErrorCodes reads, and
// the field label each mention is tagged with.
const (
	fieldIntentSummary    = "intent.summary"
	fieldContractScenario = "contract.scenarios"
	fieldCommonPitfalls   = "knowledge.common_pitfalls"
)

// ErrorCodeMention is one place a code was found: which record, which facet,
// and where that record lives on disk.
type ErrorCodeMention struct {
	Feature string `json:"feature"`
	Path    string `json:"path,omitempty"`
	Field   string `json:"field"`
}

// ErrorCode is every place the pattern matched one distinct identifier,
// across the whole set of records extraction ran over.
type ErrorCode struct {
	Code string `json:"code"`
	// Band is the code's letter prefix plus its first digit — "E1" out of
	// "E1074" — or empty when the match has no digits to band on at all.
	Band     string             `json:"band,omitempty"`
	Mentions []ErrorCodeMention `json:"mentions"`
}

// Features lists the distinct, sorted record ids that mention this code. A
// code mentioned twice within the same record — its own repeated mentions —
// still counts as one feature: it is the same claim made twice, not two
// records agreeing to reuse it.
func (c ErrorCode) Features() []string {
	seen := make(map[string]bool, len(c.Mentions))
	var out []string
	for _, m := range c.Mentions {
		if seen[m.Feature] {
			continue
		}
		seen[m.Feature] = true
		out = append(out, m.Feature)
	}
	sort.Strings(out)
	return out
}

// CodeBand summarizes one band's assigned codes and the numbers nobody has
// claimed yet between the lowest and the highest assigned code — the
// question "what is the next free code in E2xxx" a register exists to answer
// without grepping a service's source and hoping.
type CodeBand struct {
	Band  string   `json:"band"`
	Codes []string `json:"codes"`
	// Gaps lists every code between the band's lowest and highest assigned
	// code that nothing claims. Nothing below the lowest or above the
	// highest is a gap — an unopened range is not a hole in one.
	Gaps []string `json:"gaps,omitempty"`
	// Next is one past the band's highest assigned code, empty when the
	// band's codes are not uniform enough in shape to extend safely or that
	// successor would exceed the fixed width.
	Next string `json:"next,omitempty"`
}

// ErrorCodeRegister is the extracted view over a set of records: every
// distinct identifier found, and the per-band summary built from them.
type ErrorCodeRegister struct {
	Codes []ErrorCode `json:"codes"`
	Bands []CodeBand  `json:"bands"`
}

// ExtractErrorCodes scans intent.summary, contract.scenarios[].statement, and
// knowledge.common_pitfalls across every given record for pattern, and groups
// what it finds into a register.
func ExtractErrorCodes(records []Record, pattern string) (ErrorCodeRegister, error) {
	if pattern == "" {
		pattern = DefaultErrorCodePattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return ErrorCodeRegister{}, fmt.Errorf("error code pattern %q: %w", pattern, err)
	}

	byCode := map[string]*ErrorCode{}
	var order []string

	add := func(rec Record, field, text string) {
		for _, code := range re.FindAllString(text, -1) {
			ec, ok := byCode[code]
			if !ok {
				ec = &ErrorCode{Code: code, Band: bandOf(code)}
				byCode[code] = ec
				order = append(order, code)
			}
			ec.Mentions = append(ec.Mentions, ErrorCodeMention{
				Feature: rec.ID,
				Path:    rec.Path,
				Field:   field,
			})
		}
	}

	for _, rec := range records {
		add(rec, fieldIntentSummary, rec.Intent())
		for _, s := range rec.ScenarioStatements() {
			add(rec, fieldContractScenario, s)
		}
		for _, s := range rec.CommonPitfallsText() {
			add(rec, fieldCommonPitfalls, s)
		}
	}

	sort.Strings(order)
	codes := make([]ErrorCode, 0, len(order))
	for _, code := range order {
		codes = append(codes, *byCode[code])
	}

	return ErrorCodeRegister{Codes: codes, Bands: bandsOf(codes)}, nil
}

// bandOf derives a code's band: its leading non-digit run plus its first
// digit. This assumes nothing about the pattern beyond "letters then
// digits" — a match with no digits at all has no band, reported as "".
func bandOf(code string) string {
	i := 0
	for i < len(code) && !isASCIIDigit(code[i]) {
		i++
	}
	if i >= len(code) {
		return ""
	}
	return code[:i+1]
}

func isASCIIDigit(b byte) bool { return b >= '0' && b <= '9' }

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isASCIIDigit(s[i]) {
			return false
		}
	}
	return true
}

// bandsOf groups codes by band and computes each band's gaps and next free
// code. A band whose codes are not uniform digit-suffixed numbers (mixed
// widths, or a suffix that is not pure digits) still lists its codes, but
// reports no gaps and no next — extending or filling a shape that is not a
// simple counter would be a guess, not an answer.
func bandsOf(codes []ErrorCode) []CodeBand {
	byBand := map[string][]string{}
	var bandOrder []string
	for _, ec := range codes {
		if ec.Band == "" {
			continue
		}
		if _, ok := byBand[ec.Band]; !ok {
			bandOrder = append(bandOrder, ec.Band)
		}
		byBand[ec.Band] = append(byBand[ec.Band], ec.Code)
	}
	sort.Strings(bandOrder)

	bands := make([]CodeBand, 0, len(bandOrder))
	for _, name := range bandOrder {
		codeStrs := append([]string(nil), byBand[name]...)
		sort.Strings(codeStrs)
		b := CodeBand{Band: name, Codes: codeStrs}

		width := -1
		uniform := true
		nums := make([]int, 0, len(codeStrs))
		for _, c := range codeStrs {
			suffix := c[len(name):]
			if !allDigits(suffix) {
				uniform = false
				break
			}
			if width == -1 {
				width = len(suffix)
			} else if len(suffix) != width {
				uniform = false
				break
			}
			n, err := strconv.Atoi(suffix)
			if err != nil {
				uniform = false
				break
			}
			nums = append(nums, n)
		}

		if uniform && len(nums) > 0 {
			sort.Ints(nums)
			seen := make(map[int]bool, len(nums))
			for _, n := range nums {
				seen[n] = true
			}
			for n := nums[0]; n < nums[len(nums)-1]; n++ {
				if !seen[n] {
					b.Gaps = append(b.Gaps, name+fmt.Sprintf("%0*d", width, n))
				}
			}
			// A fixed-width convention has no valid successor beyond its all-9s
			// value: E9999 must not become E91000 under the E\d{4} default.
			// A rollover needs an explicit convention rather than an invented one.
			max := 1
			for i := 0; i < width; i++ {
				max *= 10
			}
			if nums[len(nums)-1] < max-1 {
				b.Next = name + fmt.Sprintf("%0*d", width, nums[len(nums)-1]+1)
			}
		}

		bands = append(bands, b)
	}
	return bands
}
