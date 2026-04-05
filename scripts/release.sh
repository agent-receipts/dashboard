#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<EOF
Usage: $(basename "$0") <version>

Create a GitHub Release for the dashboard.

Examples:
  $(basename "$0") 0.1.0
  $(basename "$0") 1.0.0-beta.1
EOF
  exit 1
}

fail() { echo "error: $1" >&2; exit 1; }

[[ $# -eq 1 ]] || usage

VERSION="$1"
TAG="v${VERSION}"

# Validate version format (SemVer 2.0)
[[ "$VERSION" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-([0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*))?(\+([0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*))?$ ]] || \
  fail "invalid version '$VERSION' — expected semver (e.g. 0.1.0, 1.0.0-beta.1)"

# Ensure we're on main and up to date
BRANCH=$(git branch --show-current)
[[ "$BRANCH" == "main" ]] || fail "must be on main branch (currently on $BRANCH)"
git fetch origin main --quiet
LOCAL=$(git rev-parse HEAD)
REMOTE=$(git rev-parse origin/main)
[[ "$LOCAL" == "$REMOTE" ]] || fail "local main is not up to date with origin — run git pull"

# Ensure working tree is clean
[[ -z "$(git status --porcelain)" ]] || fail "working tree is not clean — commit or stash changes first"

# Check tag doesn't already exist (locally or on remote)
git fetch origin --tags --quiet
git tag -l "$TAG" | grep -q . && fail "tag $TAG already exists"
git ls-remote --tags origin "refs/tags/$TAG" | grep -q . && fail "tag $TAG already exists on origin"

echo "==> Releasing dashboard v$VERSION (tag: $TAG)"
echo ""

echo "--- Running checks"
go vet ./...
go build ./cmd/dashboard
go test ./...

echo ""
echo "--- All checks passed"
echo ""
echo "Will create release:"
echo "  Tag:    $TAG"
echo "  Title:  v$VERSION"
echo ""
read -rp "Proceed? [y/N] " confirm
[[ "$confirm" =~ ^[Yy]$ ]] || { echo "Aborted."; exit 0; }

REPO_URL=$(gh repo view --json url -q '.url')
gh release create "$TAG" --title "v$VERSION" --generate-notes
echo ""
echo "==> Released dashboard v$VERSION"
echo "    ${REPO_URL}/releases/tag/$TAG"
