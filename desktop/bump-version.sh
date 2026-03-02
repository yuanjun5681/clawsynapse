#!/bin/bash
# Bump version across all desktop app config files
set -euo pipefail

DIR="$(cd "$(dirname "$0")" && pwd)"

if [ $# -ne 1 ]; then
  echo "Usage: $0 <version>"
  echo "Example: $0 1.2.0"
  exit 1
fi

VERSION="$1"

if ! [[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "Error: version must be semver (e.g. 1.2.0)"
  exit 1
fi

# tauri.conf.json
sed -i '' "s/\"version\": \"[^\"]*\"/\"version\": \"$VERSION\"/" "$DIR/src-tauri/tauri.conf.json"

# Cargo.toml (only the package version, line 3)
sed -i '' '3s/^version = ".*"/version = "'"$VERSION"'"/' "$DIR/src-tauri/Cargo.toml"

# package.json
sed -i '' "s/\"version\": \"[^\"]*\"/\"version\": \"$VERSION\"/" "$DIR/package.json"

echo "Bumped to $VERSION:"
echo "  - src-tauri/tauri.conf.json"
echo "  - src-tauri/Cargo.toml"
echo "  - package.json"
