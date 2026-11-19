package remote

import (
	"errors"
	"io"
	"net/http"
)

// Bound embedding responses before the shared embedder can buffer them.
type boundedTransport struct{ base http.RoundTripper }

func (t boundedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	response.Body = &limitedBody{ReadCloser: response.Body, remaining: 1 << 20}
	return response, nil
}

type limitedBody struct {
	io.ReadCloser
	remaining int64
}

func (b *limitedBody) Read(p []byte) (int, error) {
	if int64(len(p)) > b.remaining+1 {
		p = p[:b.remaining+1]
	}
	n, err := b.ReadCloser.Read(p)
	b.remaining -= int64(n)
	if b.remaining < 0 {
		return 0, errors.New("embedding response exceeds limit")
	}
	return n, err
}

// Explicitly ignore ambient proxy variables at the publication boundary.
func trustedTransport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.MaxResponseHeaderBytes = 16 << 10
	return transport
}
