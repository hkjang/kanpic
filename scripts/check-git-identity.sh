#!/usr/bin/env bash
set -euo pipefail

readonly required_name="hkjang"

identity_name() {
  local ident="$1"
  printf '%s' "${ident%% <*}"
}

author_ident="$(git var GIT_AUTHOR_IDENT)"
committer_ident="$(git var GIT_COMMITTER_IDENT)"
author_name="$(identity_name "$author_ident")"
committer_name="$(identity_name "$committer_ident")"

if [[ "$author_name" != "$required_name" || "$committer_name" != "$required_name" ]]; then
  cat >&2 <<EOF
kanpic commits must use hkjang for both author and committer.
  author:    $author_ident
  committer: $committer_ident

Fix this repository with:
  git config --local user.name hkjang
  git config --local user.email gagagiga@naver.com
EOF
  exit 1
fi

if printf '%s\n%s\n' "$author_ident" "$committer_ident" | grep -Eiq 'shimonenator'; then
  echo "shimonenator is not allowed as a kanpic Git identity." >&2
  exit 1
fi
