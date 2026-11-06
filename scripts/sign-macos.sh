#!/usr/bin/env bash

set -euo pipefail

: "${MACOS_SIGNING_IDENTITY:?Developer ID Application identity is required}"
: "${APPLE_ID:?Apple ID is required}"
: "${APPLE_TEAM_ID:?Apple team ID is required}"
: "${APPLE_APP_PASSWORD:?App-specific password is required}"

if [ "$(uname -s)" != Darwin ]; then
	echo "macOS is required for signing and notarization" >&2
	exit 1
fi

dist_dir="${1:-dist}"
binaries=(
	"$dist_dir/clawsynapse-darwin-amd64"
	"$dist_dir/clawsynapse-darwin-arm64"
	"$dist_dir/clawsynapsed-darwin-amd64"
	"$dist_dir/clawsynapsed-darwin-arm64"
)
for binary in "${binaries[@]}"; do
	[ -f "$binary" ] || { echo "missing binary: $binary" >&2; exit 1; }
done

work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"' EXIT
mkdir "$work_dir/payload"
for binary in "${binaries[@]}"; do
	codesign --force --sign "$MACOS_SIGNING_IDENTITY" --options runtime --timestamp "$binary"
	codesign --verify --strict --verbose=2 "$binary"
	cp "$binary" "$work_dir/payload/"
done

ditto -c -k "$work_dir/payload" "$work_dir/notarization.zip"
# Capture the status explicitly: a completed submission is not necessarily Accepted.
xcrun notarytool submit "$work_dir/notarization.zip" \
	--apple-id "$APPLE_ID" --team-id "$APPLE_TEAM_ID" \
	--password "$APPLE_APP_PASSWORD" --wait --timeout 30m \
	--output-format json > "$work_dir/result.json"
cat "$work_dir/result.json"
status="$(plutil -extract status raw -o - "$work_dir/result.json")"
if [ "$status" != Accepted ]; then
	submission_id="$(plutil -extract id raw -o - "$work_dir/result.json")"
	xcrun notarytool log "$submission_id" \
		--apple-id "$APPLE_ID" --team-id "$APPLE_TEAM_ID" \
		--password "$APPLE_APP_PASSWORD" || true
	echo "notarization was not Accepted: $status" >&2
	exit 1
fi

echo "macOS binaries signed and notarized (Accepted)"
