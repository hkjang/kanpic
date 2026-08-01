#!/usr/bin/env bash
set -euo pipefail

release_version="${1:-v0.1.0}"
project_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
"$project_root/scripts/release.sh" "$release_version"
notes_file="$(mktemp)"
trap 'rm -f "$notes_file"' EXIT
"$project_root/scripts/release-notes.sh" "$release_version" > "$notes_file"

gh release create "$release_version" \
  "$project_root/dist/kanpic-${release_version}.tar.gz" \
  "$project_root/dist/kanpic-${release_version}.tar.gz.sha256" \
  --repo hkjang/kanpic \
  --title "kanpic $release_version" \
  --notes-file "$notes_file"
