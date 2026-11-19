// Package integrations contains the remote API's versioned wire contract.
package integrations

import _ "embed"

// OpenAPI is the REST contract served without repository contents.
//
//go:embed openapi.json
var OpenAPI []byte
