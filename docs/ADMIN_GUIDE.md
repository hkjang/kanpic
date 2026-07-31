# kanpic 관리자 가이드 (Admin Guide)

본 가이드는 kanpic 데이터 협업 플랫폼의 시스템 관리자(System Administrator)를 위한 설치, 배포, OIDC 통합, API 키 보안 및 운영 관리 가이드입니다.

---

## 1. 아키텍처 개요

kanpic은 온프레미스 및 에어갭(Air-gapped) 폐쇄망 환경에 최적화된 아키텍처로 설계되었습니다.

- **Go 모듈형 모놀리스 (Go Modular Monolith)**: Redis 등 외부 인메모리 캐시 없이 실행되는 단일 바이너리/컨테이너 구조.
- **PostgreSQL 서버 권위 저장소**: 동구조 및 마이그레이션 자동 실행 (`migrations/001_initial.sql` ~ `005_data_validations.sql`).
- **React Canvas 프론트엔드**: 임베디드 정적 자산(Static Assets)으로 Go 바이너리에 포함되어 단일 포트(8080)로 제공.
- **Model Context Protocol (MCP)**: HTTP JSON-RPC 인터페이스를 통한 AI 에이전트 연동 지원 (`/mcp`).

---

## 2. 설치 및 배포

### 2.1 Docker Compose 로컬/서버 배포

필수 환경 변수는 `POSTGRES_DSN` 하나입니다. 초기 부트스트랩 관리자 계정을 설정하려면 `BOOTSTRAP_ADMIN_ID`와 `BOOTSTRAP_ADMIN_PASSWORD`를 함께 지정합니다.

```bash
BOOTSTRAP_ADMIN_ID=admin \
BOOTSTRAP_ADMIN_PASSWORD='Strong-Bootstrap-Password-123!' \
POSTGRES_DSN='postgres://kanpic:password@postgres:5432/kanpic?sslmode=disable' \
docker compose up -d --build
```

### 2.2 폐쇄망(Air-Gapped) 오프라인 배포

인터넷 연결이 차단된 완전 폐쇄망 환경에서는 릴리즈 이미지 아카이브(`kanpic-v0.1.0.tar.gz`)를 사용하여 즉시 로드할 수 있습니다.

```bash
# 1. 무결성 검증
sha256sum -c kanpic-v0.1.0.tar.gz.sha256

# 2. 이미지 로드
gzip -dc kanpic-v0.1.0.tar.gz | docker load

# 3. 컨테이너 실행
docker run -d --name kanpic -p 8080:8080 \
  -e POSTGRES_DSN='postgres://kanpic:password@postgres.internal:5432/kanpic?sslmode=require' \
  -e BOOTSTRAP_ADMIN_ID='admin' \
  -e BOOTSTRAP_ADMIN_PASSWORD='Strong-Password-123!' \
  kanpic:v0.1.0
```

---

## 3. 관리자 콘솔 (`/admin`)

브라우저에서 `http://<HOST>:8080/admin`으로 이동하면 관리자 전용 운영 인터페이스가 제공됩니다.

### 3.1 시스템 설정 CRUD 및 버전 관리 (Revision Control)
- 모든 시스템 설정 항목은 변경 시마다 **설정 버전(Revision)**이 생성됩니다.
- 설정 오류 발생 시 past revision으로 **원클릭 복원(Rollback)**이 가능합니다.
- 비밀 설정(Secret Key, Secret Value)은 원문이 노출되지 않도록 암호화/마스킹 처리됩니다.

### 3.2 Keycloak OIDC 간편 연동 설정
kanpic은 표준 OpenID Connect (Authorization Code Flow + PKCE)를 지원합니다.

1. **설정 방법**:
   - `/admin` 콘솔의 **Keycloak OIDC 간편 연결** 섹션으로 이동
   - **Issuer URL**: Keycloak Realm URL 입력 (예: `https://auth.company.com/realms/enterprise`)
   - **Client ID**: OIDC Client ID 입력
   - **Client Secret**: Confidential Client인 경우 입력 (Public Client인 경우 비워둠)
2. **검증 및 연결 테스트**:
   - `연결 테스트` 버튼을 클릭하여 Issuer Discovery Metadata 및 OIDC Endpoints 자동 검증
   - 성공 시 OIDC 활성화 토글을 ON으로 전환합니다.

### 3.3 전체 API 키 관리 및 감사
- 시스템 전체에서 발급된 API 키의 상태(활성/폐기), 발급자, Scope, 생성일시를 통합 감사할 수 있습니다.
- 보안 침해요소 발견 시 관리자는 특정 API 키를 **즉시 폐기(Revoke)** 또는 **회전(Rotate)**시킬 수 있습니다.
- API 키 원문은 SHA-256 해시로만 DB에 저장되므로 원문 유출 위험이 없습니다.

### 3.4 실시간 로그 및 서버 상태
- **서버 로그**: Scope 및 Log Level(INFO, WARN, ERROR) 필터링 지원.
- **헬스 체크 Endpoints**:
  - `/healthz`: Liveness & Readiness probe (PostgreSQL 연결 상태 검증)
  - `/api/v1/version`: Git Commit ID, 이미지 빌드 시각 및 버전 정보 확인

---

## 4. 보안 및 운영 정책

1. **최소 권한 원칙 (Least Privilege)**: MCP 및 REST API 호출 시 요청마다 `mcp.use` 및 Resource Scope를 독립 검증합니다.
2. **데이터베이스 백업**: `pg_dump`를 이용하여 주기적으로 PostgreSQL 데이터베이스 전체를 백업합니다.
3. **마이그레이션 관리**: 앱 시작 시 `migrations/` 내의 DDL이 순차적으로 자동 적용되므로 별도 DDL 작업이 필요 없습니다.
