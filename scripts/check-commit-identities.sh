#!/usr/bin/env bash
set -euo pipefail

readonly required_name="hkjang"
# GitHub writes this committer on the commit it makes when a pull request is
# merged — both for a merge commit and for a squash. Either way the person who
# pressed the button is the author, which is what this check is really about,
# so it is let through when the author is still hkjang. A squash carries the
# content rather than being an empty shell, but its parts were reviewed on the
# branch and GitHub only writes this committer for an authenticated user's own
# merge, so the attribution holds.
#
# A commit with this committer that no pull request produced cannot occur
# through the GitHub UI, and the author check below still has to pass.
readonly github_merge_committer="GitHub <noreply@github.com>"

if [[ "$#" -eq 0 ]]; then
  set -- HEAD
fi

failed=0
# The unit separator, not a tab: read collapses runs of whitespace separators,
# and a root commit's empty parent list would shift every field left.
while IFS=$'\x1f' read -r commit_hash parents author_name author_email committer_name committer_email; do
  if [[ "$author_name" == "$required_name" && "$committer_name <$committer_email>" == "$github_merge_committer" ]]; then
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
