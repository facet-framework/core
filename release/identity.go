// Package release describes the running build independently of data formats.
//
// @facet: delivery/distribution/build-identity
// @facet-role: primary
package release

import (
	_ "embed"
	"encoding/json"
	"runtime/debug"
	"strconv"
	"strings"
	"time"
)

//go:embed core-version.json
var manifest []byte

type Compatibility struct {
	TargetVersion            string   `json:"target_version"`
	SpecificationVersion     string   `json:"specification_version"`
	CompatibleSpecifications []string `json:"compatible_specifications"`
	RecordSchemaVersions     []string `json:"record_schema_versions"`
	ProductProtocol          int      `json:"product_protocol"`
}

func Supported() Compatibility {
	var c Compatibility
	if err := json.Unmarshal(manifest, &c); err != nil {
		panic(err)
	}
	return c
}

type Identity struct {
	Product      string `json:"product"`
	Version      string `json:"version"`
	Distribution string `json:"distribution"`
	Channel      string `json:"channel"`
	SourceCommit string `json:"source_commit,omitempty"`
	Dirty        *bool  `json:"dirty"`
	// BuildTime is the stamped build time (release builds stamp the commit
	// time), or Go's vcs.time, normalised to RFC3339 UTC. Unknown stays absent.
	BuildTime     string        `json:"build_time,omitempty"`
	Compatibility Compatibility `json:"compatibility"`
	// AtlasVersion and UIVersion are set only by a distribution that bundles
	// Atlas, a separate product, with this CLI. A Core build leaves both empty,
	// and an empty value is omitted rather than reported as a version.
	AtlasVersion string `json:"atlas_version,omitempty"`
	UIVersion    string `json:"ui_version,omitempty"`
}

// These fields may be set through Go linker flags by build tooling. Empty metadata remains
// unknown; an arbitrary legacy -X main.version never establishes provenance.
var Distribution, SourceCommit, Dirty, BuildTime, BuildChannel string

func Current(version string) Identity {
	commit, dirty, stamp := SourceCommit, Dirty, BuildTime
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				if commit == "" {
					commit = s.Value
				}
			case "vcs.modified":
				if dirty == "" {
					dirty = s.Value
				}
			case "vcs.time":
				if stamp == "" {
					stamp = s.Value
				}
			}
		}
	}
	return Resolve(version, Distribution, BuildChannel, commit, dirty, stamp)
}
func Resolve(version, distribution, channel, commit, dirty, stamp string) Identity {
	c := Supported()
	if distribution == "" {
		distribution = "unidentified"
	}
	var modified *bool
	if value, err := strconv.ParseBool(dirty); err == nil {
		modified = &value
	}
	built := ""
	if t, err := time.Parse(time.RFC3339, stamp); err == nil {
		built = t.UTC().Format(time.RFC3339)
	}
	if version == "" || version == "dev" {
		version = c.TargetVersion + "-dev.unknown"
		if t, err := time.Parse(time.RFC3339, stamp); err == nil && t.Unix() >= 0 {
			version = c.TargetVersion + "-dev." + strconv.FormatInt(t.Unix(), 10)
		}
		if commit != "" {
			short := commit
			if len(short) > 12 {
				short = short[:12]
			}
			version += "+g" + short
		}
		if modified != nil && *modified {
			if strings.Contains(version, "+") {
				version += ".dirty"
			} else {
				version += "+dirty"
			}
		}
		if channel == "" {
			channel = "dev"
		}
	}
	if channel == "" {
		channel = "unspecified"
	}
	return Identity{Product: "core", Version: version, Distribution: distribution, Channel: channel, SourceCommit: commit, Dirty: modified, BuildTime: built, Compatibility: c}
}
