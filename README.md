# kanpic

kanpic은 온프레미스와 폐쇄망을 우선 지원하는 웹 기반 AI 스프레드시트 및 데이터 협업 플랫폼입니다. 초기 버전은 Go 모듈형 모놀리스, React Canvas 편집기, PostgreSQL 서버 권위 저장소로 구성되며 Redis 없이 실행됩니다.

## 로컬 실행

필수 환경 변수는 `POSTGRES_DSN` 하나뿐입니다. 나머지 서비스 설정은 관리자 콘솔(`/admin`)에서 관리하고 변경할 때마다 버전이 생성됩니다.

```bash
docker compose up --build
```

브라우저에서 `http://localhost:8080`을 엽니다. 최초 설치는 OIDC가 꺼진 로컬 관리자 모드이며, 관리자 콘솔의 **Keycloak OIDC 간편 연결**에서 Issuer URL과 Public Client ID를 입력하고 검증·연결 테스트 후 활성화할 수 있습니다.

## 오프라인 설치

GitHub Release의 `kanpic-v0.1.0.tar.gz`는 Docker 이미지 아카이브입니다.

```bash
sha256sum -c kanpic-v0.1.0.tar.gz.sha256
gzip -dc kanpic-v0.1.0.tar.gz | docker load
docker run --rm -p 8080:8080 \
  -e POSTGRES_DSN='postgres://kanpic:password@postgres.internal:5432/kanpic?sslmode=require' \
  kanpic:v0.1.0
```

런타임에는 외부 인터넷 연결이 필요하지 않습니다. 애플리케이션 이미지에 웹 자산, CA 인증서 묶음, 시간대 데이터와 마이그레이션이 모두 포함됩니다.

## 운영 인터페이스

- `/admin`: 시스템 설정 CRUD, 설정 버전·복원, 검증·연결 테스트, 서버 로그, 전체 API 키 현황
- `/preferences`: 개인화 설정과 개인 API 키 생성·수정·폐기·회전
- `/mcp`: Streamable HTTP 방식의 MCP JSON-RPC endpoint
- `/api/v1/version`: 이미지 빌드 버전, Git commit, 빌드 시각
- `/healthz`: 컨테이너 health check

API 키 원문은 생성·회전 직후 한 번만 반환하며 데이터베이스에는 SHA-256 해시만 저장합니다. MCP 호출은 `mcp.use`와 각 도구의 실제 작업 scope를 모두 검사합니다.

## 개발 검증

```bash
go test ./...
cd web && npm ci && npm test && npm run build
```

이 저장소의 커밋 author와 committer는 모두 `hkjang`으로 고정합니다. 최초 clone 후 아래 설정을 적용하면 저장소에 포함된 prepare-commit-msg/pre-commit/pre-push 검사가 활성화되며, `shimonenator` 또는 다른 이름의 커밋은 거부됩니다. `prepare-commit-msg` 검사는 `git commit --no-verify`에도 생략되지 않습니다.

```bash
git config --local user.name hkjang
git config --local user.email gagagiga@naver.com
git config --local core.hooksPath .githooks
```

CSV·TSV·XLSX는 홈 화면에서 미리보기 후 원자적으로 가져올 수 있습니다. 편집기에서는 현재 워크북을 XLSX 또는 CSV로 내보낼 수 있으며, 동일 기능은 REST API와 MCP 도구에서도 제공됩니다.

릴리즈 번들은 다음 명령으로 만듭니다.

```bash
./scripts/release.sh v0.1.0
```
