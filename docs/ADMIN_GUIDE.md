# kanpic 관리자 가이드 (Admin Guide)

**시스템 버전**: v0.3.0  
**최종 수정일**: 2026년 7월 31일  
**문서 분류**: 시스템 관리자 및 엔터프라이즈 운영자용 가이드 (System Admin Documentation)

---

## 1. 시스템 아키텍처 및 요구사항

kanpic은 외부 인터넷 통신과 외부 캐시(Redis) 없이 단일 PostgreSQL과 통합 실행되는 **Go 모듈형 모놀리스(Modular Monolith)** 아키텍처로 설계되었습니다.

### 1.1 시스템 구성도 (System Architecture Diagram)

```mermaid
graph TB
    subgraph Client Layer [사용자 & 외부 연동 레이어]
        UserBrowser[사용자 브라우저 - React 19 Canvas]
        AIAgent[AI 에이전트 / Assistant]
    end

    subgraph Server Layer [kanpic Go Application Container :8080]
        HttpAPI[HTTP REST API Server]
        AuthModule[OIDC PKCE & Auth Service]
        McpServer[MCP Streamable JSON-RPC Server]
        FormulaEngine[Formula Recalculation Engine]
        SettingsManager[Settings Revisioning Engine]
    end

    subgraph Data Layer [데이터 및 인증 레이어]
        PostgresDB[(PostgreSQL 16 DB)]
        KeycloakSSO[사내 Keycloak OIDC Provider]
    end

    UserBrowser -->|REST / WebSockets| HttpAPI
    AIAgent -->|MCP JSON-RPC /mcp| McpServer
    HttpAPI --> AuthModule
    AuthModule -->|OIDC PKCE| KeycloakSSO
    HttpAPI --> FormulaEngine
    HttpAPI --> SettingsManager
    HttpAPI -->|SQL / Pool| PostgresDB
    SettingsManager -->|SQL| PostgresDB
```

### 1.2 시스템 요구사항

| 구분 | 최소 요구사항 | 권장 요구사항 |
| :--- | :--- | :--- |
| **CPU** | 2 Core | 4 Core 이상 |
| **Memory** | 2 GB RAM | 8 GB RAM 이상 |
| **Storage** | 10 GB (SSD) | 50 GB 이상 (NVMe SSD) |
| **OS** | Linux (Ubuntu 22.04+, RHEL 8+, Alpine 3.18+) | Linux (Docker 24.0+ / Containerd) |
| **Database** | PostgreSQL 14.0 이상 | PostgreSQL 16.0 이상 (HA 지원) |
| **포트(Port)** | 8080/TCP (HTTP API & Web) | 8080/TCP, 5432/TCP (Internal DB) |

---

## 2. 시스템 설치 및 배포

### 2.1 Docker Compose 로컬/서버 배포

초기 환경 변수로 `POSTGRES_DSN`, 부트스트랩 계정인 `BOOTSTRAP_ADMIN_ID` 및 `BOOTSTRAP_ADMIN_PASSWORD`를 설정하여 컨테이너를 시작합니다.

```bash
# 1. 환경 변수 지정 및 Docker Compose 실행
BOOTSTRAP_ADMIN_ID=admin \
BOOTSTRAP_ADMIN_PASSWORD='Strong-Bootstrap-Password-123!' \
POSTGRES_DSN='postgres://kanpic:password@postgres:5432/kanpic?sslmode=disable' \
docker compose up -d --build
```

### 2.2 폐쇄망 (Air-Gapped) 완전 오프라인 배포

외부 인터넷망 연결이 전면 차단된 폐쇄망 환경에서는 릴리즈 아카이브 패키지(`kanpic-v0.3.0.tar.gz`)를 전송받아 즉시 배포할 수 있습니다.

```bash
# 1. SHA-256 체크섬 무결성 검증
sha256sum -c kanpic-v0.3.0.tar.gz.sha256

# 2. Docker 이미지 아카이브 로드
gzip -dc kanpic-v0.3.0.tar.gz | docker load

# 3. 오프라인 단일 컨테이너 실행
docker run -d --name kanpic-prod \
  -p 8080:8080 \
  -e POSTGRES_DSN='postgres://kanpic:password@postgres.internal:5432/kanpic?sslmode=require' \
  -e BOOTSTRAP_ADMIN_ID='admin' \
  -e BOOTSTRAP_ADMIN_PASSWORD='Strong-Password-123!' \
  --restart=always \
  kanpic:v0.3.0
```

---

## 3. 관리자 콘솔 (`/admin`) 운영 가이드

브라우저에서 `http://<서버주소>:8080/admin`으로 접속하면 관리자 전용 대시보드가 제공됩니다.

### 3.1 시스템 설정 관리 & Revision 이력/복구
시스템의 동작 파라미터는 데이터베이스 기반 설정 이력 시스템을 통해 관리됩니다.

```mermaid
sequenceDiagram
    autonumber
    actor Admin as 관리자
    participant AdminUI as /admin 콘솔
    participant SettingsRepo as Settings Engine
    participant DB as PostgreSQL DB

    Admin->>AdminUI: 시스템 설정 수정 (예: auth.oidc.enabled = true)
    AdminUI->>SettingsRepo: UpdateSetting(Key, Value, RevisionSummary)
    SettingsRepo->>DB: INSERT INTO setting_revisions
    SettingsRepo->>DB: UPDATE system_settings
    SettingsRepo-->>AdminUI: 설정 변경 성공 (Revision #4 생성)
    
    Note over Admin, DB: 문제 발생 시 이전 Revision으로 원클릭 복원 가능
    Admin->>AdminUI: [Revision #3으로 복원] 클릭
    AdminUI->>SettingsRepo: RollbackSetting(Key, Revision=3)
    SettingsRepo-->>AdminUI: 이전 값 복원 완료 (Revision #5 생성)
```

- **설정 변경 이력**: 모든 변경 건마다 작성자, 변경시각, 사유(Summary)가 개별 Revision으로 기록됩니다.
- **원클릭 복원**: 설정 오류로 인한 장애 발생 시 past revision의 **[복원]** 버튼을 클릭하여 즉시 원복할 수 있습니다.

### 3.2 OIDC / Keycloak SSO 연동 가이드

kanpic은 표준 OpenID Connect (Authorization Code Flow + PKCE)를 지원합니다.

```
+-------------------------------------------------------------------+
|               Keycloak OIDC 간편 연결 설정 (`/admin`)             |
+-------------------------------------------------------------------+
|  OIDC 활성화 여부     : [ON]                                      |
|  Issuer URL          : https://auth.company.com/realms/prod     |
|  Client ID           : kanpic-web-client                          |
|  Client Secret       : ******************** (Confidential Client) |
+-------------------------------------------------------------------+
|               [ 연결 검증 테스트 ]    [ 설정 저장 ]               |
+-------------------------------------------------------------------+
```

1. **Client 유형 선택**:
   - **Confidential Client**: `Client Secret` 값을 함께 설정.
   - **Public Client**: `Client Secret` 항목을 빈칸으로 유지.
2. **연결 검증 테스트**:
   - `[연결 검증 테스트]` 버튼 클릭 시 서버가 Keycloak의 `.well-known/openid-configuration` 엔드포인트를 호출하여 OIDC Discovery 및 Token Endpoint 유효성을 자동 검사합니다.

### 3.3 API 키 전체 감사 및 보안 회전 (Key Rotation)
- **전체 API 키 현황 조회**: 발급된 모든 API 키의 소유자, Scope, 생성일, 상태(Active/Revoked)를 한눈에 파악합니다.
- **보안 회전 (Rotate)**: 키 유출 우려 시 기존 키를 무효화함과 동시에 새로운 API 키를 안전하게 1회 재발급합니다.
- **즉시 폐기 (Revoke)**: 이상 징후 감지 시 해당 API 키를 즉시 비활성화 처리합니다.
- **SHA-256 해시 저장**: 데이터베이스에는 키의 SHA-256 해시값만 저장되므로 데이터베이스가 노출되어도 원문 키는 안전합니다.

### 3.4 실시간 서버 로그 및 Health Monitoring
- **로그 모니터링**: Scope(`httpapi`, `auth`, `workbook`, `mcp`) 및 Log Level(`INFO`, `WARN`, `ERROR`) 조건으로 서버 로그를 실시간 검색합니다.
- **헬스 체크 Endpoints**:
  - `GET /healthz`: PostgreSQL DB 연결 상태 검증 (쿠버네티스 Liveness/Readiness Probe용)
  - `GET /api/v1/version`: 현재 컨테이너의 Git Commit, 빌드 시간, 릴리즈 버전 정보 출력

---

## 4. 백업 및 복구 (Backup & Recovery)

### 4.1 데이터베이스 백업
PostgreSQL DB 전체 데이터를 주기적으로 백업합니다.

```bash
# PostgreSQL 백업 수행
docker exec -t kanpic-postgres pg_dump -U kanpic -d kanpic -F c -b -v -f /backups/kanpic_backup_$(date +%Y%m%d).dump
```

### 4.2 데이터베이스 복구
```bash
# 백업 파일 데이터베이스 복원
docker exec -i kanpic-postgres pg_restore -U kanpic -d kanpic -v /backups/kanpic_backup_20260731.dump
```

---

## 5. 장애 조치 및 FAQ (Troubleshooting)

> [!WARNING]
> **증상**: 부트스트랩 계정으로 로그인이 되지 않고 500 에러 발생  
> **원인**: `BOOTSTRAP_ADMIN_ID`와 `BOOTSTRAP_ADMIN_PASSWORD` 중 하나만 설정되었을 때 보안 정책에 의해 초기화가 차단됩니다.  
> **조치**: 두 환경 변수를 모두 정확히 설정하거나, 둘 다 제거한 뒤 복구합니다.
