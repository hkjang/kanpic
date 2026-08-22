#!/usr/bin/env bash
set -euo pipefail

release_version="${1:-v0.1.0}"
if [[ ! "$release_version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "usage: $0 vMAJOR.MINOR.PATCH" >&2
  exit 2
fi

project_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
artifact_dir="$project_root/dist"
artifact_name="kanpic-${release_version}.tar.gz"
image_name="kanpic:${release_version}"
commit="$(git -C "$project_root" rev-parse HEAD 2>/dev/null || echo unknown)"
build_time="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

mkdir -p "$artifact_dir"
docker build \
  --build-arg "VERSION=$release_version" \
  --build-arg "COMMIT=$commit" \
  --build-arg "BUILD_TIME=$build_time" \
  --tag "$image_name" \
  --tag "ghcr.io/hkjang/kanpic:$release_version" \
  "$project_root"

docker save "$image_name" "ghcr.io/hkjang/kanpic:$release_version" | gzip -9 > "$artifact_dir/$artifact_name"
(cd "$artifact_dir" && sha256sum "$artifact_name" > "$artifact_name.sha256")

echo "created $artifact_dir/$artifact_name"
echo "offline load: gzip -dc $artifact_name | docker load"
