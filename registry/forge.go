// @facet: registry/layout/glossary-tools
// @facet-role: related
package facet

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// ForgeFromRemote is the source of a forge this host recognised itself.
const ForgeFromRemote = "git remote"

// DetectForge works out which forge a remote points at, and says how it knows.
func DetectForge(remote string, declared map[string]string) (forge, source string) {
	host := RemoteHost(remote)
	if host == "" {
		return "", ""
	}

	if forge, ok := declared[host]; ok && forge != "" {
		return forge, ConfigName
	}

	switch {
	case host == "github.com" || strings.HasSuffix(host, ".github.com"):
		return "github", ForgeFromRemote
	case host == "gitlab.com" || strings.HasSuffix(host, ".gitlab.com"):
		return "gitlab", ForgeFromRemote
	}
	return "", ""
}

// RemoteHost pulls the host out of the several shapes a git remote takes.
func RemoteHost(remote string) string {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return ""
	}
	// scp-like: git@host:owner/repo.git
	if !strings.Contains(remote, "://") {
		if at := strings.Index(remote, "@"); at >= 0 {
			rest := remote[at+1:]
			if colon := strings.Index(rest, ":"); colon >= 0 {
				return rest[:colon]
			}
			return rest
		}
		return ""
	}
	rest := remote[strings.Index(remote, "://")+3:]
	if at := strings.Index(rest, "@"); at >= 0 {
		rest = rest[at+1:]
	}
	if slash := strings.IndexAny(rest, "/:"); slash >= 0 {
		rest = rest[:slash]
	}
	return rest
}

// ForgeHostEnv names the server a forge CLI should talk to, or nothing.
func ForgeHostEnv(forge, host string) []string {
	if host == "" {
		return nil
	}
	switch forge {
	case "gitlab":
		if host == "gitlab.com" {
			return nil
		}
		return []string{"GITLAB_HOST=" + host}
	case "github":
		if host == "github.com" {
			return nil
		}
		return []string{"GH_HOST=" + host}
	}
	return nil
}

// PushForge contains only metadata needed after a proposal push. It makes
// no claim about the state or contents of the user's working files.
type PushForge struct {
	Remote      string
	RemoteHost  string
	Forge       string
	ForgeSource string
}

// ForgeForPush reads the remote of the working tree at root and the forge
// declaration of the project at configRoot, without git status, which can read
// unfinished working files to hash them. An empty root has no remote.
func ForgeForPush(root, configRoot string) PushForge {
	if root == "" {
		return PushForge{}
	}
	var state PushForge
	if out, err := runGitIn(root, "remote", "get-url", "origin"); err == nil {
		state.Remote = strings.TrimSpace(out)
		state.RemoteHost = RemoteHost(state.Remote)

		cfg, _ := LoadConfig(configRoot)
		state.Forge, state.ForgeSource = DetectForge(state.Remote, cfg.Forges())
	}
	return state
}

// OpenPullRequest opens the pull request for a pushed branch whose checkout is
// dir.
func OpenPullRequest(dir, branch string, state PushForge, title, body string) (string, error) {
	if strings.TrimSpace(title) == "" {
		return "", errors.New("a pull request needs a title")
	}
	var cmd *exec.Cmd
	switch state.Forge {
	case "github":
		if _, err := exec.LookPath("gh"); err != nil {
			return "", errors.New("the branch is pushed, but 'gh' is not installed to open the pull request with")
		}
		cmd = exec.Command("gh", "pr", "create", "--head", branch, "--title", title, "--body", body)
	case "gitlab":
		if _, err := exec.LookPath("glab"); err != nil {
			return "", errors.New("the branch is pushed, but 'glab' is not installed to open the merge request with")
		}
		cmd = exec.Command("glab", "mr", "create", "--source-branch", branch, "--title", title, "--description", body, "--yes")
	default:

		if state.RemoteHost == "" {
			return "", fmt.Errorf(
				"the branch is pushed; %q names no host, so there is no forge to open a pull request on",
				state.Remote)
		}
		return "", fmt.Errorf(
			"the branch is pushed; no forge is declared for %q, so no pull request was opened — "+
				"name it under 'forges:' in %s (github or gitlab) and this step runs",
			state.RemoteHost, ConfigName)
	}
	// The CLI is pointed at the declared host: 'glab' and 'gh' each talk to
	// their public server unless told otherwise, and opening a merge request on
	// gitlab.com for a branch that lives on git.example.com is the failure a
	// declaration exists to prevent.
	if env := ForgeHostEnv(state.Forge, state.RemoteHost); len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("the pull request could not be opened: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return FirstURL(string(out)), nil
}

// prURLRe finds the URL a forge CLI prints.
var prURLRe = regexp.MustCompile(`https?://\S+`)

// FirstURL returns the first URL in a CLI's output, which is where both CLIs
// print the link to what they just made.
func FirstURL(out string) string {
	return prURLRe.FindString(out)
}
