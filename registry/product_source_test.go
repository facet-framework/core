package facet_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/facet-framework/core/registry"
)

func TestPrivateProductReleaseSelectsPlatformAndScopesCredentials(t *testing.T) {
	entries := map[string][]byte{"plugin.json": manifestBytes(t, "acme-notes"), "plugin.js": []byte("x")}
	entries["SHA256SUMS"] = sumsBytes(entries, "SHA256SUMS")
	archive := writeZip(t, entries, nil)
	payload, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	artifact := "notes-1.0.0-linux-amd64.facetplugin"
	var leaked atomic.Bool
	download := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			leaked.Store(true)
		}
		_, _ = w.Write(payload)
	}))
	defer download.Close()
	var api *httptest.Server
	api = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-token" {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/repos/acme/notes/releases/tags/v1":
			_ = json.NewEncoder(w).Encode(map[string]any{"assets": []map[string]string{
				{"name": "notes-1.0.0-windows-amd64.facetplugin", "url": api.URL + "/wrong", "browser_download_url": api.URL + "/wrong"},
				{"name": artifact, "url": api.URL + "/repos/acme/notes/releases/assets/1", "browser_download_url": api.URL + "/anonymous"},
				{"name": "checksums.txt", "url": api.URL + "/repos/acme/notes/releases/assets/2", "browser_download_url": api.URL + "/anonymous"},
			}})
		case "/repos/acme/notes/releases/assets/1":
			if r.Header.Get("Accept") != "application/octet-stream" {
				t.Error("missing asset content negotiation")
			}
			http.Redirect(w, r, download.URL, http.StatusFound)
		case "/repos/acme/notes/releases/assets/2":
			_, _ = w.Write([]byte(hashBytes(payload) + "  " + artifact + "\n"))
		default:
			t.Errorf("unexpected release route %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer api.Close()
	_, err = facet.LoadPackage("github:acme/notes@v1", t.TempDir(), facet.LoadOptions{AssetSuffix: "-linux-amd64.facetplugin", GitHub: &facet.GitHubSource{APIBase: api.URL, DownloadBase: api.URL, HTTPClient: api.Client(), Token: "fixture-token"}})
	if err != nil {
		t.Fatal(err)
	}
	if leaked.Load() {
		t.Fatal("API credential reached redirected download host")
	}
	_, err = facet.LoadPackage("github:acme/notes@v1", t.TempDir(), facet.LoadOptions{AssetSuffix: "-linux-amd64.facetplugin", GitHub: &facet.GitHubSource{APIBase: api.URL, DownloadBase: api.URL, HTTPClient: api.Client()}})
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("private release without credentials: %v", err)
	}
}
