package facet_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/facet-framework/core/registry"
)

// The variable under test. Set in the test process so that passThrough has a
// value to pass; whether it reaches the child is what each case decides.
const secretEnv = "FACET_TEST_PLUGIN_SECRET"

// agentEnvPluginDir writes one plugin whose sidecar is this test binary in
// "env" mode — the helper TestMain already carries, which reads one call and
// answers with the environment it sees. aiEnv is the manifest's ai_env;
// grantAIEnv is the grants.json ai_env.
func agentEnvPluginDir(t *testing.T, aiEnv, grantAIEnv []string) string {
	t.Helper()
	if !names(aiEnv, helperEnv) || !names(grantAIEnv, helperEnv) {
		t.Fatalf("fixture misuse: the helper variable %s must be on both ai_env lists, or the child runs the suite", helperEnv)
	}
	dir := t.TempDir()
	id := filepath.Join(dir, "wiki")
	if err := os.MkdirAll(id, 0o755); err != nil {
		t.Fatal(err)
	}
	platform := runtime.GOOS + "-" + runtime.GOARCH
	src, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(id, "confluence-"+platform), src, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(id, "plugin.json"), map[string]any{
		"id":    "wiki",
		"tools": []string{"confluence.pull"},
		"ai_tools": []map[string]any{{
			"tool":        "confluence.pull",
			"description": "does confluence.pull",
			"input":       map[string]any{"type": "object"},
		}},
		"sidecar": map[string]any{
			"bin":    "confluence",
			"env":    []string{helperEnv, secretEnv},
			"ai_env": aiEnv,
			"sha256": map[string]string{platform: selfSum(t)},
		},
	})
	writeJSON(t, filepath.Join(id, "grants.json"), map[string]any{
		"tools":    []string{"confluence.pull"},
		"ai_tools": []string{"confluence.pull"},
		"env":      []string{helperEnv, secretEnv},
		"ai_env":   grantAIEnv,
	})
	return dir
}

func names(list []string, want string) bool {
	for _, got := range list {
		if got == want {
			return true
		}
	}
	return false
}

// envSeenBy asks the sidecar, on one path, which variables it sees, and
// which process answered.
func envSeenBy(t *testing.T, call func(context.Context) (facet.SidecarReply, error)) (env string, pid int) {
	t.Helper()
	reply, err := call(context.Background())
	if err != nil {
		t.Fatalf("the call did not come back: %v", err)
	}
	var got struct {
		Env []string `json:"env"`
		Pid int      `json:"pid"`
	}
	if err := json.Unmarshal(reply.Result, &got); err != nil {
		t.Fatalf("the sidecar did not answer with its environment: %v (%s)", err, reply.Result)
	}
	joined := strings.Join(got.Env, "\n")
	if !strings.Contains(joined, helperEnv+"=env") {
		t.Fatalf("the helper did not run in env mode; the fixture is broken: %v", got.Env)
	}
	if got.Pid == 0 {
		t.Fatalf("the helper did not report its pid: %s", reply.Result)
	}
	return joined, got.Pid
}

// The surface and the agent are two audiences with two environments.
func TestTheAgentSeesOnlyTheVariablesGrantedToTheAgent(t *testing.T) {
	t.Setenv(helperEnv, "env")
	t.Setenv(secretEnv, "shh-secret")
	// Manifest: the secret is declared under env, and under ai_env too — the
	// plugin says the agent may have it. Grant: the grant answers env for
	// both variables but ai_env only for the helper. The grant's "no" is the
	// one that has to win.
	dir := agentEnvPluginDir(t,
		[]string{helperEnv, secretEnv},
		[]string{helperEnv},
	)
	b := facet.NewPluginBroker(dir)
	defer b.StopAll()

	surface, _ := envSeenBy(t, func(ctx context.Context) (facet.SidecarReply, error) {
		return b.Call(ctx, "wiki", "confluence.pull", nil)
	})
	// The precondition: the surface does see the secret, so the agent's
	// missing line below is a withheld variable and not a variable nobody
	// had to begin with.
	if !strings.Contains(surface, secretEnv+"=shh-secret") {
		t.Fatalf("the surface did not see the variable the grant gave it: %s", surface)
	}

	agent, _ := envSeenBy(t, func(ctx context.Context) (facet.SidecarReply, error) {
		return b.CallForAgent(ctx, "wiki", "confluence.pull", nil)
	})
	if strings.Contains(agent, secretEnv+"=") {
		t.Errorf("a variable granted only to the surface reached the agent: %s", agent)
	}
}

// With ai_env naming the variable on both sides, the agent does see it.
func TestTheAgentSeesAVariableGrantedToTheAgent(t *testing.T) {
	t.Setenv(helperEnv, "env")
	t.Setenv(secretEnv, "shh-secret")
	dir := agentEnvPluginDir(t,
		[]string{helperEnv, secretEnv},
		[]string{helperEnv, secretEnv},
	)
	b := facet.NewPluginBroker(dir)
	defer b.StopAll()

	agent, _ := envSeenBy(t, func(ctx context.Context) (facet.SidecarReply, error) {
		return b.CallForAgent(ctx, "wiki", "confluence.pull", nil)
	})

	if !strings.Contains(agent, secretEnv+"=shh-secret") {
		t.Errorf("a variable granted to the agent on all four lists did not reach it: %s", agent)
	}
}

func TestAVariableTheManifestDoesNotOfferTheAgentIsWithheld(t *testing.T) {
	t.Setenv(helperEnv, "env")
	t.Setenv(secretEnv, "shh-secret")
	dir := agentEnvPluginDir(t,
		[]string{helperEnv},            // manifest ai_env: helper only
		[]string{helperEnv, secretEnv}, // grant ai_env: both
	)
	b := facet.NewPluginBroker(dir)
	defer b.StopAll()

	agent, _ := envSeenBy(t, func(ctx context.Context) (facet.SidecarReply, error) {
		return b.CallForAgent(ctx, "wiki", "confluence.pull", nil)
	})

	if strings.Contains(agent, secretEnv+"=") {
		t.Errorf("a variable the manifest never offered the agent reached it: %s", agent)
	}
}

// Both audiences calling the same plugin in one broker get two processes.
func TestTheSurfaceAndTheAgentDoNotShareASidecarProcess(t *testing.T) {
	t.Setenv(helperEnv, "env")
	t.Setenv(secretEnv, "shh-secret")
	dir := agentEnvPluginDir(t,
		[]string{helperEnv, secretEnv},
		[]string{helperEnv, secretEnv},
	)
	b := facet.NewPluginBroker(dir)
	defer b.StopAll()

	_, surfacePid := envSeenBy(t, func(ctx context.Context) (facet.SidecarReply, error) {
		return b.Call(ctx, "wiki", "confluence.pull", nil)
	})
	_, agentPid := envSeenBy(t, func(ctx context.Context) (facet.SidecarReply, error) {
		return b.CallForAgent(ctx, "wiki", "confluence.pull", nil)
	})

	// helperEnvDump answers every call, so a shared process would answer
	// both and look healthy; the pid it reports is what tells the two apart.
	if surfacePid == agentPid {
		t.Fatalf("the surface and the agent were served by the same process %d", surfacePid)
	}
}
