package atlaslauncher

import (
	"crypto/ed25519"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/mod/semver"
)

const manifestName = "release-manifest.json"
const signatureName = manifestName + ".sig"

//go:embed signing-keys.json
var signingKeysJSON []byte

var trustedKeys = func() map[string]ed25519.PublicKey {
	var config struct {
		Keys []struct {
			ID        string `json:"key_id"`
			PublicKey string `json:"public_key"`
		}
	}
	if err := json.Unmarshal(signingKeysJSON, &config); err != nil {
		panic("invalid embedded Atlas signing keys: " + err.Error())
	}
	keys := make(map[string]ed25519.PublicKey, len(config.Keys))
	for _, entry := range config.Keys {
		key, err := base64.StdEncoding.DecodeString(entry.PublicKey)
		if err != nil || len(key) != ed25519.PublicKeySize || keyID(key) != entry.ID {
			panic("invalid embedded Atlas signing key " + entry.ID)
		}
		keys[entry.ID] = key
	}
	return keys
}()

func keyID(key []byte) string {
	hash := sha256.Sum256(key)
	return hex.EncodeToString(hash[:8])
}

type manifest struct {
	Format    int    `json:"manifest_version"`
	Product   string `json:"product"`
	Version   string `json:"version"`
	Core      string `json:"core_version"`
	Platforms []struct {
		Platform string `json:"platform"`
		asset
	} `json:"platforms"`
}

type signature struct {
	KeyID     string `json:"key_id"`
	Algorithm string `json:"algorithm"`
	Signature string `json:"signature"`
}

// @facet: delivery/distribution/atlas-updates
// @facet-role: primary

func verifyManifest(data, sig []byte, version string, keys map[string]ed25519.PublicKey) (pin, error) {
	var s signature
	if err := json.Unmarshal(sig, &s); err != nil || s.Algorithm != "ed25519" {
		return pin{}, fmt.Errorf("invalid Atlas signature envelope")
	}
	key := keys[s.KeyID]
	if len(key) != ed25519.PublicKeySize || keyID(key) != s.KeyID {
		return pin{}, fmt.Errorf("unknown Atlas signing key %q", s.KeyID)
	}
	raw, err := base64.StdEncoding.DecodeString(s.Signature)
	if err != nil || !ed25519.Verify(key, data, raw) {
		return pin{}, fmt.Errorf("invalid Atlas manifest signature")
	}
	return parseManifest(data, version)
}

var platformName = regexp.MustCompile(`^[a-z0-9]+_[a-z0-9]+$`)
var archiveName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._+-]*\.(tar\.gz|zip)$`)

func validVersion(v string) bool {
	// The semver package also accepts abbreviated major and minor versions.
	base := v
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		base = v[:i]
	}
	return semver.IsValid("v"+v) && strings.Count(base, ".") == 2
}

func parseManifest(data []byte, version string) (pin, error) {
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return pin{}, fmt.Errorf("decode Atlas manifest: %w", err)
	}
	core := strings.TrimPrefix(m.Core, "v")
	if m.Format != 1 || m.Product != "atlas" || m.Version != version || !validVersion(version) || !validVersion(core) || len(m.Platforms) == 0 {
		return pin{}, fmt.Errorf("invalid Atlas manifest for v%s", version)
	}
	p := pin{Version: version, Core: core, Platforms: make(map[string]asset)}
	for _, entry := range m.Platforms {
		name := strings.Replace(entry.Platform, "_", "/", 1)
		digest, err := hex.DecodeString(entry.SHA256)
		if !platformName.MatchString(entry.Platform) || !archiveName.MatchString(entry.Archive) ||
			err != nil || len(digest) != sha256.Size || entry.Size <= 0 {
			return pin{}, fmt.Errorf("invalid Atlas archive metadata for %q", entry.Platform)
		}
		if _, exists := p.Platforms[name]; exists {
			return pin{}, fmt.Errorf("duplicate Atlas platform %q", entry.Platform)
		}
		a := entry.asset
		a.SHA256 = strings.ToLower(a.SHA256)
		p.Platforms[name] = a
	}
	return p, nil
}

// PinJSONForRelease converts validated release metadata into an embedded pin.
// An unsigned manifest must have been verified against attested archives by the caller.
func PinJSONForRelease(data, sig []byte, version string, unsigned bool) ([]byte, error) {
	var p pin
	var err error
	if unsigned {
		switch version {
		case "0.1.0-rc.1", "0.1.0-rc.2", "0.1.0-rc.3":
			p, err = parseManifest(data, version)
		default:
			return nil, fmt.Errorf("unsigned Atlas pin is unsupported for v%s", version)
		}
	} else {
		p, err = verifyManifest(data, sig, version, trustedKeys)
	}
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(p, "", "  ")
}
