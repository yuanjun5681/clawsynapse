#!/usr/bin/env bash

set -euo pipefail

DEFAULT_REPO="yuanjun5681/clawsynapse"

usage() {
	cat <<'EOF'
Generate GitHub Release notes for ClawSynapse.

Usage:
  ./scripts/release-notes.sh --version v0.0.4 [--output dist/release-notes-v0.0.4.md]

Options:
  --version VERSION    Release version or tag name
  --repo OWNER/REPO    GitHub repository slug (default: yuanjun5681/clawsynapse)
  --output PATH        Output markdown file path (default: stdout)
  -h, --help           Show this help
EOF
}

VERSION=""
REPO="$DEFAULT_REPO"
OUTPUT=""

while [ "$#" -gt 0 ]; do
	case "$1" in
		--version)
			VERSION="${2:-}"
			shift 2
			;;
		--repo)
			REPO="${2:-}"
			shift 2
			;;
		--output)
			OUTPUT="${2:-}"
			shift 2
			;;
		-h|--help)
			usage
			exit 0
			;;
		*)
			echo "unknown option: $1" >&2
			usage >&2
			exit 1
			;;
	esac
done

if [ -z "$VERSION" ]; then
	VERSION="$(git describe --tags --exact-match 2>/dev/null || true)"
fi

if [ -z "$VERSION" ]; then
	echo "release version is required; pass --version <tag>" >&2
	exit 1
fi

if ! git rev-parse -q --verify "refs/tags/${VERSION}" >/dev/null 2>&1; then
	echo "tag not found: ${VERSION}" >&2
	exit 1
fi

PREVIOUS_TAG="$(git describe --tags --abbrev=0 "${VERSION}^" 2>/dev/null || true)"
TMP_FILE="$(mktemp)"
trap 'rm -f "$TMP_FILE"' EXIT

{
	echo "## Summary"
	echo
	echo "Automated release for \`${VERSION}\`."
	echo
	echo "Highlights:"
	echo "- ships both \`clawsynapse\` CLI and \`clawsynapsed\` daemon binaries"
	echo "- supports one-line install with default CLI + daemon deployment"
	echo "- includes \`checksums.txt\` for release asset verification"
	echo "- adds passive \`chat.progress\` with exact active-request correlation, independent durable sequencing, and per-invocation CLI/Harness context"
	echo "- keeps workspace conversation answers final-only by native assistant-message boundaries; work updates stay in \`chat.progress\`"
	echo
	echo "## Install"
	echo
	echo "Latest stable release:"
	echo
	echo '```bash'
	echo "curl -fsSL https://raw.githubusercontent.com/${REPO}/main/scripts/install.sh | bash"
	echo '```'
	echo
	echo "Install this exact release:"
	echo
	echo '```bash'
	echo "curl -fsSL https://raw.githubusercontent.com/${REPO}/main/scripts/install.sh | bash -s -- --version ${VERSION}"
	echo '```'
	echo
	echo "Post-install:"
	echo
	echo '```bash'
	echo "clawsynapse version"
	echo "clawsynapsed --version"
	echo "clawsynapse service status"
	echo "clawsynapse daemon"
	echo "clawsynapse bots"
	echo '```'
	echo
	echo "The installer creates a schemaVersion 3 daemon config.json, not a Bot or identity. Version 2 layouts require an explicit offline migration before starting the new daemon."
	echo "Create Bots explicitly with \`clawsynapse bot create --file bot.json\`; use \`--bot <bot-id>\` for Bot-scoped commands."
	echo "Legacy single-Bot installs must stop the old daemon, back up their state, and run \`clawsynapse migrate --legacy-config /path/to/config.yaml --data-dir /path/to/daemon-root\` before installing the daemon service."
	echo
	echo "## Assets"
	echo
	echo "Release assets include platform binaries for \`clawsynapse\` and \`clawsynapsed\`, plus \`checksums.txt\`."
	echo
	echo "## Changes"
	echo
	if [ -n "$PREVIOUS_TAG" ]; then
		echo "Changes since \`${PREVIOUS_TAG}\`:"
		echo
		git log --no-merges --format='- %s (%h)' "${PREVIOUS_TAG}..${VERSION}"
	else
		echo "Initial tagged release."
	fi
} >"$TMP_FILE"

if [ -n "$OUTPUT" ]; then
	mkdir -p "$(dirname "$OUTPUT")"
	cp "$TMP_FILE" "$OUTPUT"
else
	cat "$TMP_FILE"
fi
