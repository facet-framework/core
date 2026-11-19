package remote

import (
	"encoding/json"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestRESTResponsesMatchOpenAPI(t *testing.T) {
	s, key := serverFixture(t)
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("https://contract.example/openapi.json", s.spec); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ path, model string }{{"/v1/capabilities", "Capabilities"}, {"/v1/records", "RecordList"}, {"/v1/record?id=:navigation/interfaces/example", "Record"}, {"/v1/sections?id=:navigation/interfaces/example", "Sections"}, {"/v1/sections?id=:navigation/interfaces/example&section=intent", "Sections"}, {"/v1/anchors?id=:navigation/interfaces/example", "Anchors"}, {"/v1/source?path=source.go&start=1&end=2", "Source"}, {"/v1/record?id=missing/record/id", "Problem"}} {
		response := request(t, s, key, "GET", tc.path, "")
		schema, err := compiler.Compile("https://contract.example/openapi.json#/components/schemas/" + tc.model)
		if err != nil {
			t.Fatal(err)
		}
		var value any
		if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		if err := schema.Validate(value); err != nil {
			t.Fatalf("%s: %v", tc.path, err)
		}
	}
}
