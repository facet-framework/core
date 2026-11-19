// @facet: delivery/distribution/build-identity
// @facet-role: primary
package cli

import (
	"encoding/json"
	"fmt"

	"github.com/facet-framework/core/release"
	"github.com/spf13/cobra"
)

// versionReport is what 'facet version --json' prints. The build identity stays
// flat at the top level; where this file was installed from, and what the
// project pins, are separate facts and get their own objects.
//
// @facet: delivery/distribution/core-pin
// @facet-role: related
// @facet: delivery/distribution/install-origin
// @facet-role: related
type versionReport struct {
	release.Identity
	Install    release.Install   `json:"install"`
	ProjectPin *projectPinReport `json:"project_pin,omitempty"`
}

func newVersionCmd(identity release.Identity) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{Use: "version", Short: "Show product version, build origin and format compatibility", Args: cobra.NoArgs,
		// No project pre-run: version must answer in a project whose
		// configuration is broken or whose pin this build does not meet.
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
		RunE: func(cmd *cobra.Command, _ []string) error {
			install := release.CurrentInstall()
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(versionReport{
					Identity:   identity,
					Install:    install,
					ProjectPin: projectPinFor(ResolveRoot(cmd), identity.Version),
				})
			}
			dirty := "unknown"
			if identity.Dirty != nil {
				dirty = fmt.Sprint(*identity.Dirty)
			}
			commit := identity.SourceCommit
			if commit == "" {
				commit = "unknown"
			}
			built := identity.BuildTime
			if built == "" {
				built = "unknown"
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "facet core %s (%s, %s)\nsource: %s; dirty: %s; built: %s\nspecification: %s; product protocol: %d\ninstall: %s (%s)\n", identity.Version, identity.Distribution, identity.Channel, commit, dirty, built, identity.Compatibility.SpecificationVersion, identity.Compatibility.ProductProtocol, install.Origin, install.Path)
			return err
		}}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit build identity, install origin, project pin and compatibility as JSON")
	return markPinExempt(cmd, pinExemptSelf)
}
