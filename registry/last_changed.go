package facet

import (
	"os/exec"
	"strings"
)

// LastChanged maps each path under root to the day of the newest commit that
// touched it, relative to root.
func LastChanged(root string) map[string]string {
	if shallow, err := exec.Command("git", "-C", root, "rev-parse", "--is-shallow-repository").Output(); err != nil ||
		strings.TrimSpace(string(shallow)) == "true" {
		return nil
	}
	out, err := exec.Command("git", "-C", root, "log",
		"--relative", "--name-only", "--no-renames",
		"--date=short", "--format=\x1f%cd").Output()
	if err != nil {
		return nil
	}
	changed := map[string]string{}
	day := ""
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "\x1f") {
			day = strings.TrimSpace(line[1:])
			continue
		}
		path := strings.TrimSpace(line)
		if path == "" || day == "" {
			continue
		}
		// git log walks newest first, so the first time a path appears is the
		// last time it changed.
		if _, seen := changed[path]; !seen {
			changed[path] = day
		}
	}
	return changed
}
