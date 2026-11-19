package facet_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// newStaticHTTPServer serves the exact bytes at the exact paths given, and
// 404s on anything else — a stand-in for a plain file host an https:// source
// would point at.
func newStaticHTTPServer(t *testing.T, files map[string][]byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newRoutedHTTPServer is newStaticHTTPServer's mutable twin: the map is read
// on every request rather than copied at construction, so a test can fill in
// entries whose content depends on the server's own URL (the GitHub release
// JSON's asset download link, for instance) after the server already exists.
func newRoutedHTTPServer(t *testing.T, files map[string][]byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}
