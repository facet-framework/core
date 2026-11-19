package facet

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// ErrGitHubSourceMalformed means a "github:" source did not parse as
// owner/repo@tag.
var ErrGitHubSourceMalformed = errors.New("plugin: a github: source must be github:owner/repo@tag")

// ErrGitHubAssetMissing means the named release carries no asset matching
// the plugin id.
var ErrGitHubAssetMissing = errors.New("plugin: the release carries no *.facetplugin asset")

// ErrGitHubChecksumsMissing means the release carries no checksums.txt.
var ErrGitHubChecksumsMissing = errors.New("plugin: the release carries no checksums.txt")

// GitHubSource resolves a github:owner/repo@tag reference to bytes, entirely
// through injected functions — the same shape bin/facet's shell functions
// have, kept swappable so a test can stand in an httptest server for GitHub
// itself rather than reaching the network.
type GitHubSource struct {
	APIBase      string
	DownloadBase string
	HTTPClient   *http.Client
	Token        string
}

func (g *GitHubSource) apiBase() string {
	if g == nil || g.APIBase == "" {
		return "https://api.github.com"
	}
	return g.APIBase
}

func (g *GitHubSource) downloadBase() string {
	if g == nil || g.DownloadBase == "" {
		return "https://github.com"
	}
	return g.DownloadBase
}

type githubAuthTransport struct {
	base          http.RoundTripper
	origin, token string
}

func (t githubAuthTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	clone.Header.Del("Authorization")
	if clone.URL.Scheme+"://"+clone.URL.Host == t.origin {
		clone.Header.Set("Authorization", "Bearer "+t.token)
		if strings.Contains(clone.URL.Path, "/releases/assets/") {
			clone.Header.Set("Accept", "application/octet-stream")
		} else {
			clone.Header.Set("Accept", "application/vnd.github+json")
		}
	}
	return t.base.RoundTrip(clone)
}
func (g *GitHubSource) client() *http.Client {
	client := http.DefaultClient
	if g != nil && g.HTTPClient != nil {
		client = g.HTTPClient
	}
	if g == nil || g.Token == "" {
		return client
	}
	copy := *client
	base := copy.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	parsed, err := url.Parse(g.apiBase())
	if err != nil {
		return client
	}
	copy.Transport = githubAuthTransport{base: base, origin: parsed.Scheme + "://" + parsed.Host, token: g.Token}
	return &copy
}

// parseGitHubSource splits "github:owner/repo@tag" into its parts.
func parseGitHubSource(src string) (repo, tag string, err error) {
	rest, ok := strings.CutPrefix(src, "github:")
	if !ok {
		return "", "", ErrGitHubSourceMalformed
	}
	repo, tag, ok = strings.Cut(rest, "@")
	if !ok || repo == "" || tag == "" {
		return "", "", ErrGitHubSourceMalformed
	}
	if strings.Count(repo, "/") != 1 {
		return "", "", ErrGitHubSourceMalformed
	}
	return repo, tag, nil
}

// loadFromGitHub downloads the release's *.facetplugin asset and its
// checksums.txt from the SAME release, verifies one against the other before
// anything is unpacked, and only then stages the package.
func loadFromGitHub(src, work string, opts LoadOptions) (string, error) {
	repo, tag, err := parseGitHubSource(src)
	if err != nil {
		return "", err
	}
	gh := opts.GitHub
	client := gh.client()

	assetName, assetURL, checksumsAssetURL, err := findProductReleaseAssets(client, gh.apiBase(), repo, tag, opts.AssetSuffix, gh != nil && gh.Token != "")
	if err != nil {
		return "", err
	}
	checksumsURL := checksumsAssetURL
	if checksumsURL == "" {
		checksumsURL = gh.downloadBase() + "/" + repo + "/releases/download/" + url.PathEscape(tag) + "/checksums.txt"
	}

	tmpPkg, err := os.CreateTemp("", "facet-plugin-gh-*.facetplugin")
	if err != nil {
		return "", err
	}
	tmpPkgPath := tmpPkg.Name()
	defer os.Remove(tmpPkgPath)
	if err := downloadTo(client, assetURL, tmpPkg); err != nil {
		tmpPkg.Close()
		return "", err
	}
	if err := tmpPkg.Close(); err != nil {
		return "", err
	}

	checksums, err := fetchChecksums(client, checksumsURL)
	if err != nil {
		return "", err
	}
	expected, ok := checksums[assetName]
	if !ok {
		return "", fmt.Errorf("%w: checksums.txt does not name %s", ErrGitHubChecksumsMissing, assetName)
	}

	sum, err := fileSHA256(tmpPkgPath)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(sum, expected) {
		return "", fmt.Errorf("%w: %s has %s, checksums.txt says %s", ErrPluginChecksumMismatch, assetName, sum, expected)
	}
	if err := stageZipEntries(tmpPkgPath, work); err != nil {
		return "", err
	}
	return sum, nil
}

func findFacetPluginAsset(client *http.Client, apiBase, repo, tag string) (string, string, error) {
	name, asset, _, err := findProductReleaseAssets(client, apiBase, repo, tag, "", false)
	return name, asset, err
}
func findProductReleaseAssets(client *http.Client, apiBase, repo, tag, suffix string, authenticated bool) (name, assetURL, checksumsURL string, err error) {
	releaseURL := apiBase + "/repos/" + repo + "/releases/tags/" + url.PathEscape(tag)
	resp, err := client.Get(releaseURL)
	if err != nil {
		return "", "", "", fmt.Errorf("plugin: fetching release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", "", fmt.Errorf("plugin: fetching %s: server answered %s", releaseURL, resp.Status)
	}
	assets, err := parseReleaseAssets(resp.Body)
	if err != nil {
		return "", "", "", err
	}
	download := func(a releaseAsset) string {
		if authenticated && a.URL != "" {
			return a.URL
		}
		return a.BrowserDownloadURL
	}
	for _, a := range assets {
		if a.Name == "checksums.txt" {
			checksumsURL = download(a)
		}
		if strings.HasSuffix(a.Name, ".facetplugin") && (suffix == "" || strings.HasSuffix(a.Name, suffix)) {
			if name != "" && suffix != "" {
				return "", "", "", fmt.Errorf("plugin: release has ambiguous platform assets")
			}
			if name == "" {
				name, assetURL = a.Name, download(a)
			}
		}
	}
	if name == "" {
		return "", "", "", ErrGitHubAssetMissing
	}
	return name, assetURL, checksumsURL, nil
}

type releaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	URL                string `json:"url"`
}

func parseReleaseAssets(body io.Reader) ([]releaseAsset, error) {
	raw, err := io.ReadAll(io.LimitReader(body, (4<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > 4<<20 {
		return nil, fmt.Errorf("plugin: release metadata exceeds size limit")
	}
	var release struct {
		Assets []releaseAsset `json:"assets"`
	}
	if err = json.Unmarshal(raw, &release); err != nil {
		return nil, err
	}
	return release.Assets, nil
}

// extractJSONStringValue pulls the value out of one `"key": "value",` line.
func extractJSONStringValue(line string) string {
	_, rest, ok := strings.Cut(line, ":")
	if !ok {
		return ""
	}
	rest = strings.TrimSpace(rest)
	rest = strings.TrimSuffix(rest, ",")
	rest = strings.TrimSpace(rest)
	if len(rest) < 2 || rest[0] != '"' || rest[len(rest)-1] != '"' {
		return ""
	}
	return rest[1 : len(rest)-1]
}

// fetchChecksums downloads checksums.txt and parses it into name -> sha256,
// the same sha256sum-format lines SHA256SUMS uses inside a package.
func fetchChecksums(client *http.Client, url string) (map[string]string, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("plugin: fetching %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: fetching %s: server answered %s", ErrGitHubChecksumsMissing, url, resp.Status)
	}
	out := map[string]string{}
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		sum, name, ok := splitSumLine(line)
		if !ok {
			continue
		}
		out[name] = sum
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
