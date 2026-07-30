#!/usr/bin/env bash
set -euo pipefail

readonly required_name="hkjang"

if [[ "$#" -eq 0 ]]; then
  set -- HEAD
fi

failed=0
while IFS=$'\t' read -r commit_hash author_name author_email committer_name committer_email; do
  if [[ "$author_name" != "$required_name" || "$committer_name" != "$required_name" ]] ||
    printf '%s\n%s\n%s\n%s\n' "$author_name" "$author_email" "$committer_name" "$committer_email" | grep -Eiq 'shimonenator'; then
    printf 'Rejected commit %s: author=%s <%s>, committer=%s <%s>\n' \
      "$commit_hash" "$author_name" "$author_email" "$committer_name" "$committer_email" >&2
    failed=1
  fi
done < <(git log --format='%H%x09%an%x09%ae%x09%cn%x09%ce' "$@")

if [[ "$failed" -ne 0 ]]; then
  echo "Every kanpic commit must use hkjang as both author and committer." >&2
  exit 1
fi
