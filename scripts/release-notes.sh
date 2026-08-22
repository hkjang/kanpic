#!/usr/bin/env bash
set -euo pipefail

release_version="${1:-}"
if [[ ! "$release_version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "usage: $0 vMAJOR.MINOR.PATCH" >&2
  exit 2
fi

project_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
curated_notes="$project_root/docs/releases/$release_version.md"
artifact_name="kanpic-$release_version.tar.gz"

echo "# kanpic $release_version"
echo
if [[ ! -s "$curated_notes" ]]; then
  echo "release notes are required: $curated_notes" >&2
  exit 1
fi
cat "$curated_notes"

cat <<EOF

## 오프라인 설치

이 릴리즈는 웹 자산과 서버 바이너리를 포함한 Docker 이미지 아카이브로 제공됩니다.

\`\`\`bash
sha256sum -c $artifact_name.sha256
gzip -dc $artifact_name | docker load
docker run --rm -p 8080:8080 \\
  -e POSTGRES_DSN='postgres://kanpic:password@postgres.internal:5432/kanpic?sslmode=require' \\
  kanpic:$release_version
\`\`\`

- 필수 런타임 환경 변수: \`POSTGRES_DSN\`
- Redis 및 외부 인터넷 연결: 불필요
- 관리자 초기 로그인을 보호하려면 \`BOOTSTRAP_ADMIN_ID\`와 \`BOOTSTRAP_ADMIN_PASSWORD\`를 함께 지정합니다.
- 무결성 검증 파일: \`$artifact_name.sha256\`
EOF
