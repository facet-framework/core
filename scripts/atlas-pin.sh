#!/usr/bin/env bash
# Regenerate Core's embedded pin from a published FACET Atlas release.
set -euo pipefail

if [[ $# -ne 1 || ! $1 =~ ^[0-9]+\.[0-9]+\.[0-9]+([-+][a-zA-Z0-9.+-]+)?$ ]]; then
  echo "Usage: $0 <version>" >&2
  exit 2
fi

version=$1
repo=facet-framework/atlas
pin_dir=$(cd -- "$(dirname -- "$0")/../internal/atlaslauncher" && pwd)
temp=$(mktemp -d "$pin_dir/.atlas-pin.XXXXXX")
trap 'rm -rf "$temp"' EXIT
gh release view "v$version" --repo "$repo" --json assets >"$temp/assets.json"
gh release download "v$version" --repo "$repo" --pattern checksums.txt --output "$temp/checksums.txt"
gh release download "v$version" --repo "$repo" --pattern release-manifest.json --output "$temp/release-manifest.json"

python3 - "$version" "$temp" >"$temp/atlas.json" <<'PY'
import json
import re
import sys
from pathlib import Path

version, directory = sys.argv[1:]
directory = Path(directory)
assets = json.loads((directory / "assets.json").read_text())["assets"]
release = json.loads((directory / "release-manifest.json").read_text())
core = release["core_version"].removeprefix("v")
if release["version"] != version or not core:
    sys.exit(f"Invalid release manifest for Atlas {version}")
sizes = {asset["name"]: asset["size"] for asset in assets}
digests = {}
for line in (directory / "checksums.txt").read_text().splitlines():
    if match := re.fullmatch(r"([0-9a-fA-F]{64})\s+\*?(\S+)", line.strip()):
        digest, name = match.groups()
        digests[name] = digest.lower()

platforms = {}
for os_name, extension in (("darwin", "tar.gz"), ("linux", "tar.gz"), ("windows", "zip")):
    for arch in ("amd64", "arm64"):
        name = f"facet-atlas_{version}_{os_name}_{arch}.{extension}"
        if name not in digests or sizes.get(name, 0) <= 0:
            sys.exit(f"Missing checksum or size for {name}")
        platforms[f"{os_name}/{arch}"] = {
            "archive": name,
            "sha256": digests[name],
            "size": sizes[name],
        }
print(json.dumps({"version": version, "core": core, "platforms": platforms}, indent=2))
PY

mv "$temp/atlas.json" "$pin_dir/atlas.json"
