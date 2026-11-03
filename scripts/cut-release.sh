#!/usr/bin/env bash

set -euo pipefail

DEFAULT_REMOTE="origin"
DEFAULT_BASE_BRANCH="develop"
DEFAULT_RELEASE_BRANCH="main"
SEMVER_REGEX='^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$'

usage() {
	cat <<'EOF'
Prepare and cut a ClawSynapse release from develop to main.

Usage:
  ./scripts/cut-release.sh v0.0.4
  ./scripts/cut-release.sh v0.0.4-rc.1

Options:
  --remote NAME          Git remote name (default: origin)
  --base-branch NAME     Source branch to release from (default: develop)
  --release-branch NAME  Target branch for releases (default: main)
  --ff-only              Require a fast-forward merge from base to release
  --no-push              Stop after local merge and tag creation
  -h, --help             Show this help

Behavior:
  1. Verify the worktree is clean.
  2. Fetch remote branches and tags.
  3. Checkout the release branch.
  4. Fast-forward it to the remote release branch.
  5. Merge the base branch into the release branch.
  6. Push the release branch.
  7. Create the release tag.
  8. Push the release tag.
  9. Checkout your original branch again.
EOF
}

is_valid_version() {
	printf '%s\n' "$1" | grep -Eq "$SEMVER_REGEX"
}

is_prerelease_version() {
	case "$1" in
		*-*)
			return 0
			;;
		*)
			return 1
			;;
	esac
}

VERSION=""
REMOTE="$DEFAULT_REMOTE"
BASE_BRANCH="$DEFAULT_BASE_BRANCH"
RELEASE_BRANCH="$DEFAULT_RELEASE_BRANCH"
NO_PUSH=0
FF_ONLY=0

while [ "$#" -gt 0 ]; do
	case "$1" in
		--remote)
			REMOTE="${2:-}"
			shift 2
			;;
		--base-branch)
			BASE_BRANCH="${2:-}"
			shift 2
			;;
		--release-branch)
			RELEASE_BRANCH="${2:-}"
			shift 2
			;;
		--ff-only)
			FF_ONLY=1
			shift
			;;
		--no-push)
			NO_PUSH=1
			shift
			;;
		-h|--help)
			usage
			exit 0
			;;
		-*)
			echo "unknown option: $1" >&2
			usage >&2
			exit 1
			;;
		*)
			if [ -n "$VERSION" ]; then
				echo "unexpected argument: $1" >&2
				usage >&2
				exit 1
			fi
			VERSION="$1"
			shift
			;;
	esac
done

if [ -z "$VERSION" ]; then
	echo "release version is required" >&2
	usage >&2
	exit 1
fi

if ! is_valid_version "$VERSION"; then
	echo "release version must use semantic tag format like v0.0.4 or v0.0.4-rc.1" >&2
	exit 1
fi

current_branch="$(git branch --show-current)"
cleanup() {
	if [ -n "$current_branch" ] && [ "$(git branch --show-current)" != "$current_branch" ]; then
		git checkout "$current_branch" >/dev/null
	fi
}
trap cleanup EXIT

if ! git diff --quiet || ! git diff --cached --quiet; then
	echo "worktree is not clean; commit or stash changes before cutting a release" >&2
	exit 1
fi

if git rev-parse -q --verify "refs/tags/${VERSION}" >/dev/null 2>&1; then
	echo "tag already exists locally: ${VERSION}" >&2
	exit 1
fi

if git ls-remote --tags "$REMOTE" "refs/tags/${VERSION}" | grep -q .; then
	echo "tag already exists on ${REMOTE}: ${VERSION}" >&2
	exit 1
fi

git fetch "$REMOTE" "$BASE_BRANCH" "$RELEASE_BRANCH" --tags

if ! git show-ref --verify --quiet "refs/heads/${RELEASE_BRANCH}"; then
	echo "local branch not found: ${RELEASE_BRANCH}" >&2
	exit 1
fi

if ! git show-ref --verify --quiet "refs/remotes/${REMOTE}/${BASE_BRANCH}"; then
	echo "remote branch not found: ${REMOTE}/${BASE_BRANCH}" >&2
	exit 1
fi

if ! git show-ref --verify --quiet "refs/remotes/${REMOTE}/${RELEASE_BRANCH}"; then
	echo "remote branch not found: ${REMOTE}/${RELEASE_BRANCH}" >&2
	exit 1
fi

if git show-ref --verify --quiet "refs/heads/${BASE_BRANCH}"; then
	ahead="$(git rev-list --count "refs/remotes/${REMOTE}/${BASE_BRANCH}..refs/heads/${BASE_BRANCH}")"
	if [ "$ahead" -ne 0 ]; then
		echo "local ${BASE_BRANCH} is ahead of ${REMOTE}/${BASE_BRANCH} by ${ahead} commit(s); push it first: git push ${REMOTE} ${BASE_BRANCH}" >&2
		exit 1
	fi
fi

git checkout "$RELEASE_BRANCH"
git merge --ff-only "${REMOTE}/${RELEASE_BRANCH}"
if [ "$FF_ONLY" -eq 1 ]; then
	git merge --ff-only "${REMOTE}/${BASE_BRANCH}"
else
	git merge --no-ff --no-edit "${REMOTE}/${BASE_BRANCH}"
fi

if [ "$NO_PUSH" -eq 0 ]; then
	git push "$REMOTE" "$RELEASE_BRANCH"
fi

git tag "$VERSION"

if [ "$NO_PUSH" -eq 0 ]; then
	git push "$REMOTE" "$VERSION"
	echo "release pushed: branch ${RELEASE_BRANCH}, tag ${VERSION}"
	if is_prerelease_version "$VERSION"; then
		echo "GitHub Actions will publish prerelease assets for ${VERSION} without updating latest."
	else
		echo "GitHub Actions will publish release assets for ${VERSION}."
	fi
else
	echo "local release prepared: branch ${RELEASE_BRANCH}, tag ${VERSION}"
	echo "next steps:"
	echo "  git push ${REMOTE} ${RELEASE_BRANCH}"
	echo "  git push ${REMOTE} ${VERSION}"
fi
