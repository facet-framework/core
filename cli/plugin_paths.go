package cli

import (
	"os"
	"path/filepath"
)

// PluginsDir resolves shared plugin storage for every host command (CLI and
// MCP).
// An explicit override takes precedence over XDG_DATA_HOME and the user's
// .local/share directory. Storage is not resolved relative to the inspected
// repository: plugin manifests and grants belong to the host's user data.
// Configured paths are accepted unchanged. An unavailable home yields no
// plugin directory rather than a guessed location.
func PluginsDir() string {
	if dir := os.Getenv("FACET_PLUGINS_DIR"); dir != "" {
		return dir
	}
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "facet", "plugins")
}
