package facet

// The broker's bundled-plugin tests need one bundled plugin that brings a
// sidecar with an environment. Core bundles none such itself; a linked product
// registers its own through RegisterBundledPlugin. This test binary registers a
// plugin of that shape, under the id the tests address, so the bundled path is
// exercised exactly as it is for a product's registration.
func init() {
	RegisterBundledPlugin(PluginManifest{
		ID: "wiki",
		Tools: []string{
			"repo.state", "repo.log", "repo.write", "repo.branch", "repo.commit", "repo.pr",
			"confluence.state", "confluence.reach",
			"confluence.import", "confluence.mirror",
		},
		Sidecar: &ManifestSidecar{
			Bin: "confluence",
			Env: []string{"CONFLUENCE_URL", "CONFLUENCE_EMAIL", "CONFLUENCE_API_TOKEN"},
		},
	})
}
