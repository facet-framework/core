package cli

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/facet-framework/core/registry"
)

// hookReadContext delivers existing claims while the agent is still reading
// source, before it chooses an edit. It never validates a record or writes code.
//
// @facet: authoring/session/read-context
// @facet-role: primary
func hookReadContext(ctx hookContext, in hookInput) {
	path := in.ToolInput.FilePath
	if path == "" {
		return
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(in.CWD, path)
	}
	path = filepath.Clean(path)
	// A fallback context may describe cwd even when the Read names an unrelated
	// repository. Default registries claim every relative path, including ../.
	rel, err := filepath.Rel(ctx.root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return
	}
	if _, record := ctx.recordRegistry(path); record {
		return
	}
	anchors, err := facet.ScanFile(path)
	if err != nil {
		return
	}
	claiming := ctx.sourceRegistries(path)
	const footer = "\nCheck these existing claims before changing observable behaviour. Use read_facet for the full intent or contract; update the record when its claims change."
	const omitted = "\nFurther anchored claims omitted; use read_facet for their intent and contract.\n"
	var b strings.Builder
	seen := map[string]bool{}
anchorLoop:
	for _, a := range anchors {
		if a.Role != "" && a.Role != "primary" && a.Role != "related" {
			continue
		}
		for _, reg := range claiming {
			if !a.BelongsTo(reg.Name) {
				continue
			}
			recordPath := reg.PathForID(a.ID)
			if seen[recordPath] {
				continue
			}
			rec, err := facet.LoadRecord(recordPath, "features")
			if err != nil || (rec.Lifecycle != "active" && rec.Lifecycle != "planned") {
				continue
			}
			digest := recordDigest([]facet.Registry{reg}, a)
			if digest == "" {
				continue
			}
			// Bound the aggregate as well as the per-record digest; many anchors must
			// not turn one source Read into an unbounded context injection.
			fragment := "\n" + digest
			if b.Len() == 0 {
				fragment = fmt.Sprintf("FACET claims for %s, before editing:\n", ctx.tag(reg, reg.Rel(path))) + fragment
			}
			// The shared digest may cut a multibyte rune at its byte boundary.
			// Remove that incomplete rune before counting bytes; JSON encoding
			// would otherwise replace each invalid byte and grow the context.
			fragment = strings.ToValidUTF8(fragment, "")
			if b.Len()+len(fragment)+len(footer)+len(omitted) > 8192 {
				b.WriteString(omitted)
				break anchorLoop
			}
			seen[recordPath] = true
			key := fmt.Sprintf("read-context:%x", sha256.Sum256([]byte(ctx.root+"\x00"+path+"\x00"+recordPath)))
			if !noticeIsNew(in.SessionID, key) {
				continue
			}
			b.WriteString(fragment)
		}
	}
	if b.Len() > 0 {
		b.WriteString(footer)
		ctx.emit("PostToolUse", b.String())
	}
}
