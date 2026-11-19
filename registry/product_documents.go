// @facet: registry/integrity/record-validation
// @facet-role: related
package facet

import (
	"fmt"
	"sync"
)

// ProductDocument is a committed declaration a product keeps inside a registry,
// validated against a schema of the registry's schema directory the way Core's
// own registry documents are: by the full lint and by the write-time check of a
// single saved file. A build that does not link the product does not validate
// the document, and a project file at that path is then left alone.
type ProductDocument struct {
	// Path returns where the document lives in a registry.
	Path func(reg Registry) string
	// Schema is the file name of its schema inside the schema directory. The
	// document is validated only where the project carries that schema.
	Schema string
}

var productDocuments struct {
	sync.Mutex
	documents []ProductDocument
}

// RegisterProductDocument adds a product's registry document. Builds register
// from init, so the documents are known before any lint runs. It panics on a
// document without a path or schema: both are programming errors of the build.
func RegisterProductDocument(document ProductDocument) {
	if document.Path == nil || document.Schema == "" {
		panic(fmt.Sprintf("facet: RegisterProductDocument needs a path and a schema, got %+v", document))
	}
	productDocuments.Lock()
	defer productDocuments.Unlock()
	productDocuments.documents = append(productDocuments.documents, document)
}

// registeredProductDocuments returns the documents in registration order.
func registeredProductDocuments() []ProductDocument {
	productDocuments.Lock()
	defer productDocuments.Unlock()
	return append([]ProductDocument(nil), productDocuments.documents...)
}
