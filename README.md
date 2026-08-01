# <img src="web/public/logo-icon.svg" alt="kanpic logo" width="36" height="36" align="absmiddle" /> kanpic

kanpic은 온프레미스와 폐쇄망을 우선 지원하는 웹 기반 AI 스프레드시트 및 데이터 협업 플랫폼입니다. 초기 버전은 Go 모듈형 모놀리스, React Canvas 편집기, PostgreSQL 서버 권위 저장소로 구성되며 Redis 없이 실행됩니다.

## 로컬 실행

필수 환경 변수는 `POSTGRES_DSN` 하나뿐입니다. 초기 로컬 관리자를 로그인으로 보호하려면 `BOOTSTRAP_ADMIN_ID`와 `BOOTSTRAP_ADMIN_PASSWORD`를 함께 설정합니다. 둘 중 하나만 설정하면 서버가 시작되지 않으며, 둘 다 생략하면 기존의 개방형 초기 설정 모드로 동작합니다. 나머지 서비스 설정은 관리자 콘솔(`/admin`)에서 관리하고 변경할 때마다 버전이 생성됩니다.

```bash
BOOTSTRAP_ADMIN_ID=admin \
BOOTSTRAP_ADMIN_PASSWORD='충분히-긴-초기-비밀번호' \
docker compose up --build
```

브라우저에서 `http://localhost:8080`을 열고 bootstrap 관리자 계정으로 로그인합니다. 관리자 콘솔의 **Keycloak OIDC 간편 연결**에서 Issuer URL과 Client ID를 입력하고 검증·연결 테스트 후 활성화할 수 있습니다. Keycloak Public Client는 Client Secret을 비워 두고, Confidential Client는 `auth.oidc.client_secret`에 secret을 저장합니다. 두 방식 모두 Authorization Code Flow와 PKCE를 사용합니다.

## 오프라인 설치

GitHub Release의 `kanpic-vX.Y.Z.tar.gz`는 Docker 이미지 아카이브입니다. 아래의 `VERSION`을 설치할 릴리즈 버전으로 바꿉니다.

```bash
VERSION=v0.8.0
sha256sum -c "kanpic-${VERSION}.tar.gz.sha256"
gzip -dc "kanpic-${VERSION}.tar.gz" | docker load
docker run --rm -p 8080:8080 \
  -e POSTGRES_DSN='postgres://kanpic:password@postgres.internal:5432/kanpic?sslmode=require' \
  -e BOOTSTRAP_ADMIN_ID='admin' \
  -e BOOTSTRAP_ADMIN_PASSWORD='충분히-긴-초기-비밀번호' \
  "kanpic:${VERSION}"
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

CSV·TSV·XLSX는 홈 화면에서 미리보기 후 원자적으로 가져올 수 있습니다. 편집기에서는 시트 간 참조와 이름 범위를 포함한 수식을 사용할 수 있고, 행·열 삽입/삭제 시 수식·병합·이름 범위·검증·필터 참조가 함께 이동하며 변경 직전 복구 버전이 자동 생성됩니다. 행 높이·열 너비·행/열 숨김·고정 영역도 PostgreSQL에 버전 관리되며 공동 편집자에게 동기화됩니다. 표시 형식, 가로·세로 정렬, 텍스트 넘침·자르기·자동 줄바꿈, 회전 및 범위 테두리는 값과 수식을 보존하는 원자적 서식 작업으로 저장되며 실행 취소할 수 있습니다. XLSX Import/Export는 이 공통 서식 모델을 사용해 글꼴·색상·채우기·정렬·줄바꿈·표시 형식·테두리를 왕복 변환합니다. 동일 기능은 REST API와 MCP 도구에서도 제공됩니다.

릴리즈 번들은 다음 명령으로 만듭니다.

```bash
./scripts/release.sh vX.Y.Z
```

## 문의 및 연락처 (Contact & Support)

- **도입 & 구축 문의 이메일**: [gagagiga@naver.com](mailto:gagagiga@naver.com)
- **GitHub Pages 홍보 페이지**: [https://hkjang.github.io/kanpic/](https://hkjang.github.io/kanpic/)
