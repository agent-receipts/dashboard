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

# cd to repo root so the script works from any directory
cd "$(git rev-parse --show-toplevel)"

# Preflight: ensure required tools are available
command -v go >/dev/null 2>&1 || fail "go is not installed"
command -v gh >/dev/null 2>&1 || fail "gh CLI is not installed — see https://cli.github.com"
gh auth status >/dev/null 2>&1 || fail "gh is not authenticated — run gh auth login"

[[ $# -eq 1 ]] || usage

VERSION="$1"
TAG="v${VERSION}"

# Validate version format (SemVer without build metadata — Go modules don't support +meta)
[[ "$VERSION" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-([0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*))?$ ]] || \
  fail "invalid version '$VERSION' — expected semver without build metadata (e.g. 0.1.0, 1.0.0-beta.1)"

# Ensure we're on main and up to date
BRANCH=$(git branch --show-current)
[[ "$BRANCH" == "main" ]] || fail "must be on main branch (currently on $BRANCH)"
git fetch origin main --quiet
LOCAL=$(git rev-parse HEAD)
REMOTE=$(git rev-parse origin/main)
if [[ "$LOCAL" != "$REMOTE" ]]; then
  read -r BEHIND AHEAD < <(git rev-list --left-right --count origin/main...HEAD)
  if [[ "$BEHIND" -gt 0 && "$AHEAD" -eq 0 ]]; then
    fail "local main is behind origin/main by $BEHIND commit(s) — run git pull"
  elif [[ "$BEHIND" -eq 0 && "$AHEAD" -gt 0 ]]; then
    fail "local main is ahead of origin/main by $AHEAD commit(s) — push your commits or reset to origin/main"
  else
    fail "local main has diverged from origin/main ($BEHIND behind, $AHEAD ahead) — reconcile with pull/rebase or reset"
  fi
fi

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
go test ./... -count=1

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
