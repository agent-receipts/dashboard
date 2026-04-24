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
git rev-parse --is-inside-work-tree >/dev/null 2>&1 || fail "must be run from within the dashboard repo"
cd "$(git rev-parse --show-toplevel)"

REMOTE_NAME="${REMOTE:-origin}"

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
git remote get-url "$REMOTE_NAME" >/dev/null 2>&1 || fail "remote '$REMOTE_NAME' not found — set REMOTE=<name> if not using origin"
git fetch "$REMOTE_NAME" main --quiet
LOCAL=$(git rev-parse HEAD)
REMOTE_HEAD=$(git rev-parse "$REMOTE_NAME/main")
if [[ "$LOCAL" != "$REMOTE_HEAD" ]]; then
  read -r BEHIND AHEAD < <(git rev-list --left-right --count "$REMOTE_NAME/main"...HEAD)
  if [[ "$BEHIND" -gt 0 && "$AHEAD" -eq 0 ]]; then
    fail "local main is behind $REMOTE_NAME/main by $BEHIND commit(s) — run git pull"
  elif [[ "$BEHIND" -eq 0 && "$AHEAD" -gt 0 ]]; then
    fail "local main is ahead of $REMOTE_NAME/main by $AHEAD commit(s) — push your commits or reset to $REMOTE_NAME/main"
  else
    fail "local main has diverged from $REMOTE_NAME/main ($BEHIND behind, $AHEAD ahead) — reconcile with pull/rebase or reset"
  fi
fi

# Ensure working tree is clean
[[ -z "$(git status --porcelain)" ]] || fail "working tree is not clean — commit or stash changes first"

# Check tag doesn't already exist (locally or on remote)
git fetch "$REMOTE_NAME" --tags --quiet
git tag -l "$TAG" | grep -q . && fail "tag $TAG already exists"
git ls-remote --tags "$REMOTE_NAME" "refs/tags/$TAG" | grep -q . && fail "tag $TAG already exists on $REMOTE_NAME"

echo "==> Releasing dashboard v$VERSION (tag: $TAG)"
echo ""

echo "--- Running checks"
go vet ./...
go build ./cmd/dashboard
go test ./... -count=1

# Ensure checks did not leave any working tree changes (e.g. go.sum updates, generated outputs)
[[ -z "$(git status --porcelain)" ]] || fail "working tree changed after running checks — review and commit or discard changes before releasing"

echo ""
echo "--- All checks passed"
echo ""
echo "Will push tag:"
echo "  Tag:    $TAG"
echo ""
echo "The Release workflow (.github/workflows/release.yml) will build binaries,"
echo "publish the Homebrew formula, and create the GitHub release."
echo ""
read -rp "Proceed? [y/N] " confirm
[[ "$confirm" =~ ^[Yy]$ ]] || { echo "Aborted."; exit 0; }

REPO_URL=$(gh repo view --json url -q '.url')
git tag -a "$TAG" -m "dashboard $TAG"
git push "$REMOTE_NAME" "$TAG"
echo ""
echo "==> Pushed tag $TAG"
echo "    Follow the release workflow: ${REPO_URL}/actions/workflows/release.yml"
echo "    Release page: ${REPO_URL}/releases/tag/$TAG"
