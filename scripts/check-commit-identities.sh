#!/usr/bin/env bash
set -euo pipefail

readonly required_name="hkjang"
# GitHub writes this committer on merge commits it makes for a pull request.
# The merge is an empty shell over commits that are checked on their own, so
# it is let through only when it is a merge (two parents) and the author is
# still hkjang. Any ordinary commit with this committer is still rejected.
readonly github_merge_committer="GitHub <noreply@github.com>"

if [[ "$#" -eq 0 ]]; then
  set -- HEAD
fi

failed=0
# The unit separator, not a tab: read collapses runs of whitespace separators,
# and a root commit's empty parent list would shift every field left.
while IFS=$'\x1f' read -r commit_hash parents author_name author_email committer_name committer_email; do
  parent_count=$(wc -w <<<"$parents")
  if [[ "$parent_count" -ge 2 && "$author_name" == "$required_name" && "$committer_name <$committer_email>" == "$github_merge_committer" ]]; then
    continue
  fi
  if [[ "$author_name" != "$required_name" || "$committer_name" != "$required_name" ]] ||
    printf '%s\n%s\n%s\n%s\n' "$author_name" "$author_email" "$committer_name" "$committer_email" | grep -Eiq 'shimonenator'; then
    printf 'Rejected commit %s: author=%s <%s>, committer=%s <%s>\n' \
      "$commit_hash" "$author_name" "$author_email" "$committer_name" "$committer_email" >&2
    failed=1
  fi
done < <(git log --format='%H%x1f%P%x1f%an%x1f%ae%x1f%cn%x1f%ce' "$@")

if [[ "$failed" -ne 0 ]]; then
  echo "Every kanpic commit must use hkjang as both author and committer (GitHub may commit a merge)." >&2
  exit 1
fi
