#!/usr/bin/env bash
# Picks the next release version from the Conventional Commits since the
# latest vX.Y.Z tag reachable from HEAD.
#
#   BUMP=auto|major|minor|patch (default auto)
#
# auto: a "type!:" subject or a "BREAKING CHANGE:" footer is major, any
# "feat:" is minor, anything else is patch. Prints key=value lines (previous,
# bump, version, tag) and appends them to $GITHUB_OUTPUT when it is set.
set -euo pipefail

bump="${BUMP:-auto}"
case "$bump" in
  auto | major | minor | patch) ;;
  *) echo "BUMP must be auto, major, minor or patch, not '$bump'" >&2; exit 1 ;;
esac

previous=$(git tag --merged HEAD --list 'v*' --sort=-v:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | head -n 1 || true)
if [ -n "$previous" ]; then
  range="$previous..HEAD"
  IFS=. read -r major minor patch <<<"${previous#v}"
else
  range=HEAD
  major=0 minor=0 patch=0
fi

if [ -z "$(git rev-list "$range")" ]; then
  echo "No commits since $previous; nothing to release." >&2
  exit 1
fi

if [ "$bump" = auto ]; then
  subjects=$(git log --format=%s "$range")
  bodies=$(git log --format=%b "$range")
  if grep -qE '^[A-Za-z]+(\([^)]*\))?!:' <<<"$subjects" || grep -qE '^BREAKING[ -]CHANGE:' <<<"$bodies"; then
    bump=major
  elif grep -qE '^feat(\([^)]*\))?:' <<<"$subjects"; then
    bump=minor
  else
    bump=patch
  fi
fi

case "$bump" in
  major) major=$((major + 1)) minor=0 patch=0 ;;
  minor) minor=$((minor + 1)) patch=0 ;;
  patch) patch=$((patch + 1)) ;;
esac
version="$major.$minor.$patch"

if git rev-parse -q --verify "refs/tags/v$version" >/dev/null; then
  echo "Tag v$version already exists." >&2
  exit 1
fi

out=$(printf 'previous=%s\nbump=%s\nversion=%s\ntag=v%s\n' "$previous" "$bump" "$version" "$version")
echo "$out"
if [ -n "${GITHUB_OUTPUT:-}" ]; then
  echo "$out" >>"$GITHUB_OUTPUT"
fi
