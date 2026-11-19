package facet

import (
	"reflect"
	"testing"
)

// The four-way intersection, on the function alone.
func TestAgentEnvIsTheFourWayIntersection(t *testing.T) {
	m := &ManifestSidecar{
		Env:   []string{"A", "B", "C", "E"}, // D missing only here
		AIEnv: []string{"A", "B", "C", "D"}, // E missing only here
	}
	g := Grant{
		Env:   []string{"A", "B", "D", "E"}, // C missing only here
		AIEnv: []string{"A", "C", "D", "E"}, // B missing only here
	}
	if got := agentEnv(m, g); !reflect.DeepEqual(got, []string{"A"}) {
		t.Fatalf("agentEnv = %v, want [A]", got)
	}
}

// A grants.json written before ai_env existed gives the agent nothing.
func TestAGrantWithoutAIEnvGivesTheAgentNoVariable(t *testing.T) {
	m := &ManifestSidecar{Env: []string{"TOKEN"}, AIEnv: []string{"TOKEN"}}
	g := Grant{Tools: []string{"x"}, Env: []string{"TOKEN"}} // AIEnv absent

	if got := agentEnv(m, g); len(got) != 0 {
		t.Fatalf("a grant with no ai_env handed the agent %v", got)
	}
	// The precondition: the same grant does give the SURFACE the variable,
	// so the empty list above is the agent's narrowing and not an empty
	// fixture.
	if got := sidecarEnv(m, g, forSurface); !reflect.DeepEqual(got, []string{"TOKEN"}) {
		t.Fatalf("the surface's environment from the same grant = %v, want [TOKEN]", got)
	}
}

// A manifest without ai_env offers the agent nothing, however the grant reads.
func TestAManifestWithoutAIEnvOffersTheAgentNothing(t *testing.T) {
	m := &ManifestSidecar{Env: []string{"TOKEN"}}
	g := Grant{Env: []string{"TOKEN"}, AIEnv: []string{"TOKEN"}}

	if got := agentEnv(m, g); len(got) != 0 {
		t.Fatalf("a manifest with no ai_env handed the agent %v", got)
	}
}

// The bundled path narrows the same way, from the manifest alone.
func TestTheBundledSidecarNarrowsItsEnvironmentForTheAgent(t *testing.T) {
	restoreSum := bundledSidecarSum
	bundledSidecarSum = func(string) string { return "00" }
	t.Cleanup(func() { bundledSidecarSum = restoreSum })
	restoreExe := bundledSidecarExecutable
	bundledSidecarExecutable = func() (string, error) { return "/nowhere/facet", nil }
	t.Cleanup(func() { bundledSidecarExecutable = restoreExe })

	b := NewPluginBroker(t.TempDir())
	m := PluginManifest{ID: "probe", Sidecar: &ManifestSidecar{
		Bin:   "probe",
		Env:   []string{"URL", "TOKEN"},
		AIEnv: []string{"URL"},
	}}

	surface, err := b.bundledSidecarSpec(m, forSurface)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := b.bundledSidecarSpec(m, forAgent)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(surface.Env, []string{"URL", "TOKEN"}) {
		t.Fatalf("the surface's bundled environment = %v, want [URL TOKEN]", surface.Env)
	}
	if !reflect.DeepEqual(agent.Env, []string{"URL"}) {
		t.Fatalf("the agent's bundled environment = %v, want [URL]", agent.Env)
	}
}
