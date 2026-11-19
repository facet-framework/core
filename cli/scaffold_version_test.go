package cli

import "testing"

func TestScaffoldUsesDeclaredCompatibility(t *testing.T) {
	for _, version := range []string{"0.1.0-dev.123+gabc", "0.1.0-rc.1", "1.21.0", "dev"} {
		for _, spec := range []string{"1.20.1", "1.21.0", "0.1.0", "1.22.0", "1.21.0-rc.1", "unknown"} {
			t.Run(version+"/"+spec, func(t *testing.T) {
				root := statusRepo(t, withRecord(map[string]string{
					"docs/facets/schema/facet.schema.json": statusSchema,
					"docs/facets/framework/FACET.md":       "# The FACET Framework " + spec + "\n",
					"tools/facet-lint/run-all.sh":          "#!/bin/sh\n",
				}))
				env := newStatusEnv(root)
				env.version = version
				want := stateRed
				if spec == "0.1.0" {
					want = stateOK
				}
				if got := checkScaffold(env); got.State != want {
					t.Fatalf("%+v; want %s", got, want)
				}
				if env.version != version {
					t.Fatal("scaffold rewrote product identity")
				}
			})
		}
	}
}
