// @facet: navigation/interfaces/claude-connector
// @facet-role: primary
package remote

import (
	"context"
	"errors"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const viewerURI = "ui://facet/reader-v1.html"
const viewerMIME = "text/html;profile=mcp-app"

var viewerRegistry struct {
	sync.Mutex
	html string
}

// RegisterViewer installs the MCP Apps viewer page for every server created
// afterwards. The page is not part of this package: a build that ships it
// registers it from package main before serving. Without a registered page a
// server configured with 'ui: true' offers no resources capability, attaches no
// '_meta.ui' to its tools and answers resources/* with JSON-RPC Method not
// found, so a host falls back to the text and structured results. It panics on
// an empty page or a second registration, because two pages would make the
// served resource depend on initialization order.
func RegisterViewer(html string) {
	if html == "" {
		panic("remote: RegisterViewer needs a non-empty page")
	}
	viewerRegistry.Lock()
	defer viewerRegistry.Unlock()
	if viewerRegistry.html != "" {
		panic("remote: the viewer is registered twice")
	}
	viewerRegistry.html = html
}

// RegisteredViewer returns the registered viewer page, or "" when this build
// carries none.
func RegisteredViewer() string {
	viewerRegistry.Lock()
	defer viewerRegistry.Unlock()
	return viewerRegistry.html
}

// viewerEnabled reports whether this server serves the viewer resource: the
// operator asked for it and the build carries a page.
func (s *Server) viewerEnabled() bool {
	return s.config.UI && s.viewer != ""
}

func (s *Server) addViewer(server *mcp.Server) {
	html := s.viewer
	server.AddResource(&mcp.Resource{URI: viewerURI, Name: "FACET reader", MIMEType: viewerMIME, Description: "Interactive read-only record, anchor and source viewer"}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		p, ok := ctx.Value(principalContext{}).(Principal)
		if !ok || !hasScopes(p, []string{RecordsScope}) {
			return nil, errors.New("insufficient_scope")
		}
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: viewerURI, MIMEType: viewerMIME, Text: html, Meta: mcp.Meta{"ui": map[string]any{"csp": map[string]any{"connectDomains": []string{}, "resourceDomains": []string{}}}}}}}, nil
	})
}
