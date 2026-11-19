// @facet: authoring/guardrails/large-record-read
// @facet-role: primary
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/facet-framework/core/registry"
)

// hookReadGate redirects a whole-file Read of a large record to read_facet.
func hookReadGate(ctx hookContext, in hookInput) {
	if os.Getenv("FACET_SKIP_READ_GATE") == "1" {
		return
	}
	if in.ToolInput.Offset != nil || in.ToolInput.Limit != nil {
		return
	}
	path := in.ToolInput.FilePath
	if path == "" || !strings.HasSuffix(path, ".yaml") {
		return
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(ctx.root, path)
	}
	for _, reg := range ctx.registries {
		features := reg.CategoryDir("features") + string(filepath.Separator)
		if !strings.HasPrefix(path, features) {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil || len(raw) <= facet.ReadLargeRecordThreshold() {
			return
		}
		id := reg.IDForPath(path)
		ctx.deny("PreToolUse", fmt.Sprintf(`%s is %d bytes. Read only the part you need with the read_facet tool of the FACET registry server:

  read_facet id: %q, sections: ["<name>", ...]

Sections and their sizes:
%s
Call read_facet with no sections to see this list again. Read has no way to
cut a record by section, but it can by lines: pass offset and limit and this
gate steps aside. To read the whole file anyway, set FACET_SKIP_READ_GATE=1.`,
			reg.Rel(path), len(raw), qualifiedID(reg, id), facet.FormatOutline(facet.SectionOutline(string(raw)))))
		return
	}
}

// qualifiedID qualifies an id for a monorepo, so the tool call that follows reaches the
// registry the file belongs to.
func qualifiedID(reg facet.Registry, id string) string {
	if reg.Name == "" {
		return id
	}
	return reg.Name + ":" + id
}
