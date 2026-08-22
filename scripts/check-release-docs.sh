#!/usr/bin/env bash
# The version README advertises and the newest release note must agree. They
# drifted once because a docs edit failed while the commit went ahead anyway,
# and nothing noticed until someone read the tag.
set -euo pipefail

project_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
declared="$(grep -m1 '^VERSION=' "$project_root/README.md" | cut -d= -f2- || true)"
newest="$(ls "$project_root/docs/releases" | sed -n 's/^\(v[0-9]\+\.[0-9]\+\.[0-9]\+\)\.md$/\1/p' | sort -V | tail -1)"

if [[ -z "$declared" ]]; then
  echo "README.md에 VERSION= 줄이 없습니다." >&2
  exit 1
fi
if [[ -z "$newest" ]]; then
  echo "docs/releases에 릴리즈 노트가 없습니다." >&2
  exit 1
fi
if [[ "$declared" != "$newest" ]]; then
  echo "README.md의 VERSION($declared)과 최신 릴리즈 노트($newest)가 다릅니다." >&2
  echo "둘 중 하나가 빠졌습니다. 릴리즈 노트를 추가했다면 README의 VERSION도 함께 올려 주세요." >&2
  exit 1
fi
echo "release docs ok: $declared"
