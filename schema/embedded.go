// Package schema provides the canonical schemas used by the spec parser.
package schema

import "embed"

// Files contains the spec frontmatter, lock, and record schemas.
//
//go:embed spec.schema.json facet.schema.json spec-lock.schema.json
var Files embed.FS
