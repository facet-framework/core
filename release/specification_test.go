package release

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"
)

// specificationHeader matches the version line at the top of the
// specification document.
var specificationHeader = regexp.MustCompile(`(?m)^\*\*Version:\*\* ([0-9]+\.[0-9]+\.[0-9]+)\s*$`)

// specificationDocument is the specification that ships in the same tree as
// this package; go test runs in the package directory.
var specificationDocument = filepath.Join("..", "docs", "framework", "FACET.md")

// specificationAgreement reports whether a build contract names the edition
// of the specification document beside it and lists it as compatible.
func specificationAgreement(contract Compatibility, document []byte) error {
	match := specificationHeader.FindSubmatch(document)
	if match == nil {
		return fmt.Errorf("the specification has no **Version:** header line")
	}
	header := string(match[1])
	if contract.SpecificationVersion != header {
		return fmt.Errorf("core-version.json declares specification_version %q, the specification header is %q", contract.SpecificationVersion, header)
	}
	if !slices.Contains(contract.CompatibleSpecifications, header) {
		return fmt.Errorf("the specification header %q is not in compatible_specifications %v", header, contract.CompatibleSpecifications)
	}
	return nil
}

// The binary reports the embedded contract's specification_version as its
// specification (facet version), so the contract and the specification
// document of the same tree must name one edition. Each tree carries its own
// pair: a change that updates one and not the other fails here.
func TestEmbeddedContractNamesTheSpecificationBesideIt(t *testing.T) {
	document, err := os.ReadFile(specificationDocument)
	if err != nil {
		t.Fatalf("read the specification of this tree: %v", err)
	}
	if err := specificationAgreement(Supported(), document); err != nil {
		t.Fatal(err)
	}
}

func TestSpecificationAgreementRefusesADifferentEdition(t *testing.T) {
	contract := Compatibility{SpecificationVersion: "0.1.0", CompatibleSpecifications: []string{"0.1.0"}}
	// Premise: the same contract agrees with a document of its own edition.
	if err := specificationAgreement(contract, []byte("# Title\n\n**Version:** 0.1.0\n")); err != nil {
		t.Fatalf("premise: %v", err)
	}
	cases := map[string]struct {
		contract Compatibility
		document string
		want     string
	}{
		"other header":   {contract, "**Version:** 0.2.0\n", `declares specification_version "0.1.0", the specification header is "0.2.0"`},
		"not compatible": {Compatibility{SpecificationVersion: "0.2.0", CompatibleSpecifications: []string{"0.1.0"}}, "**Version:** 0.2.0\n", `"0.2.0" is not in compatible_specifications`},
		"no header":      {contract, "# Title\n", "no **Version:** header line"},
	}
	for name, c := range cases {
		err := specificationAgreement(c.contract, []byte(c.document))
		if err == nil || !regexp.MustCompile(regexp.QuoteMeta(c.want)).MatchString(err.Error()) {
			t.Errorf("%s: got %v, want an error containing %q", name, err, c.want)
		}
	}
}
