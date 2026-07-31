# kanpic 관리자 가이드 (Enterprise Administration Manual)

**제품명**: kanpic 데이터 협업 플랫폼  
**시스템 버전**: v0.3.0  
**문서 버전**: v1.0  
**최종 수정일**: 2026년 7월 31일  
**문서 분류**: 시스템 관리자 및 DevOps 엔지니어용 통합 운영 매뉴얼 (System Administrator Manual)

---

## 1. 시스템 설계 사상 및 아키텍처

kanpic은 보안 규제로 인해 외부 인터넷망 통신 및 복잡한 인프라 구성이 어려운 엔터프라이즈 환경을 고려하여 **Go 모듈형 모놀리스(Modular Monolith)** 아키텍처로 설계되었습니다.

### 1.1 시스템 구조도 (System Architecture Diagram)

```mermaid
graph TB
    subgraph External Client Layer [클라이언트 & 인가된 시스템]
        BrowserClient[React 19 Canvas Browser App]
        AIAgent[Model Context Protocol Client / Agent]
        ExternalApp[사내 API 연동 서비스]
    end

    subgraph Container Layer [kanpic Single Application Container :8080]
        HttpRouter[HTTP Router & Auth Middleware]
        OidcService[OIDC PKCE Service]
        McpHandler[Streamable MCP JSON-RPC Server]
        WorkbookEngine[Workbook & Sheet Service]
        ValidationService[Data Validation Evaluator]
        SettingsEngine[Settings Revisioning System]
        MigrationRunner[Embedded DB Migration Runner]
    end

    subgraph Data & Auth Layer [인프라 및 인증 레이어]
        PostgreSQL[(PostgreSQL 16 DB Server)]
        KeycloakSSO[Keycloak OIDC Provider]
    end

    BrowserClient -->|REST / WebSockets| HttpRouter
    AIAgent -->|MCP JSON-RPC /mcp| McpHandler
    ExternalApp -->|Bearer API Key| HttpRouter

    HttpRouter --> OidcService
    OidcService -->|OIDC Discovery & PKCE| KeycloakSSO
    HttpRouter --> WorkbookEngine
    WorkbookEngine --> ValidationService
    HttpRouter --> SettingsEngine

    MigrationRunner -->|Auto Migration 001~005| PostgreSQL
    WorkbookEngine -->|SQL Connection Pool| PostgreSQL
    SettingsEngine -->|SQL Connection Pool| PostgreSQL
```

### 1.2 핵심 아키텍처 특징
- **Redis-Free 인메모리 아키텍처**: 외부 캐시 인프라 없이 PostgreSQL 연결 풀과 Go 동시성 제어만으로 고성능을 구현하여 인프라 TCO를 대폭 절감합니다.
- **서버 권위 저장소 (Server-Authoritative)**: 클라이언트의 모든 셀 데이터 변형(Mutation)은 서버에서 `base_version`과 비교 검증된 후 PostgreSQL에 원자적으로 커밋됩니다.
- **임베디드 자산 (Embedded Assets)**: React Canvas 정적 파일, 시간대 데이터, CA 인증서 묶음, DB 마이그레이션 SQL이 단일 Go 바이너리에 바이너리 수준으로 내장되어 추가 외부 의존성이 없습니다.

---

## 2. 시스템 요구사항 및 네트워크 포트

### 2.1 하드웨어 사양 기준

| 사이징 구분 | 예상 사용자 수 | CPU | RAM | Storage |
| :--- | :--- | :--- | :--- | :--- |
| **Small (부서급)** | ~50 명 | 2 vCPU | 4 GB RAM | 20 GB (SSD) |
| **Medium (본부급)** | ~500 명 | 4 vCPU | 8 GB RAM | 100 GB (NVMe SSD) |
| **Large (전사급)** | 1,000 명 이상 | 8 vCPU 이상 | 16 GB RAM 이상 | 300 GB (NVMe SSD) |

### 2.2 네트워크 방화벽 포트 정책

| Port | Protocol | Source | Destination | 용도 및 설명 |
| :--- | :--- | :--- | :--- | :--- |
| **8080** | TCP | Client / Agent | kanpic Container | Web UI, REST API, MCP Endpoint (`/mcp`) |
| **5432** | TCP | kanpic Container | PostgreSQL DB | PostgreSQL 서버 데이터베이스 연결 |
| **443** | TCP | kanpic Container | Keycloak SSO | Keycloak OIDC Provider HTTPS 연동 |

---

## 3. 배포 방법론 (Deployment Scenarios)

### 3.1 Docker Compose 로컬/서버 배포

필수 환경 변수로 `POSTGRES_DSN`, 부트스트랩 계정인 `BOOTSTRAP_ADMIN_ID` 및 `BOOTSTRAP_ADMIN_PASSWORD`를 설정합니다.

```yaml
# docker-compose.yml
version: '3.8'

services:
  postgres:
    image: postgres:16-alpine
    container_name: kanpic-postgres
    environment:
      POSTGRES_DB: kanpic
      POSTGRES_USER: kanpic
      POSTGRES_PASSWORD: StrongDbPassword123!
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U kanpic -d kanpic"]
      interval: 5s
      timeout: 5s
      retries: 5

  kanpic:
    image: kanpic:v0.3.0
    container_name: kanpic-app
    ports:
      - "8080:8080"
    environment:
      POSTGRES_DSN: "postgres://kanpic:StrongDbPassword123!@postgres:5432/kanpic?sslmode=disable"
      BOOTSTRAP_ADMIN_ID: "admin"
      BOOTSTRAP_ADMIN_PASSWORD: "Strong-Bootstrap-Password-123!"
    depends_on:
      postgres:
        condition: service_healthy
    restart: always

volumes:
  pgdata:
```

```bash
# 실행 명령
docker compose up -d --build
```

---

### 3.2 에어갭(Air-Gapped) 쿠버네티스(Kubernetes) 배포

외부 인터넷 통신이 완전 차단된 폐쇄망 쿠버네티스 클러스터 배포용 매니페스트 예시입니다.

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: kanpic-app
  namespace: kanpic
spec:
  replicas: 2
  selector:
    matchLabels:
      app: kanpic
  template:
    metadata:
      labels:
        app: kanpic
    spec:
      containers:
      - name: kanpic
        image: internal-registry.company.local/kanpic:v0.3.0
        imagePullPolicy: IfNotPresent
        ports:
        - containerPort: 8080
        env:
        - name: POSTGRES_DSN
          valueFrom:
            secretKeyRef:
              name: kanpic-secrets
              key: dsn
        - name: BOOTSTRAP_ADMIN_ID
          value: "admin"
        - name: BOOTSTRAP_ADMIN_PASSWORD
          valueFrom:
            secretKeyRef:
              name: kanpic-secrets
              key: bootstrap-password
        livenessProbe:
          httpGet:
            path: /healthz
            port: 8080
          initialDelaySeconds: 10
          periodSeconds: 15
        readinessProbe:
          httpGet:
            path: /healthz
            port: 8080
          initialDelaySeconds: 5
          periodSeconds: 10
```

---

## 4. 관리자 콘솔 (`/admin`) 세부 운영 지침

브라우저에서 `http://<서버주소>:8080/admin`으로 접속하여 시스템 설정 및 보안을 관리합니다.

### 4.1 시스템 설정 변경 및 원클릭 복원 (Revisioning)
kanpic의 모든 시스템 설정은 데이터베이스 버전 관리 시스템을 통해 변경 이력이 완벽 추적됩니다.

```mermaid
sequenceDiagram
    autonumber
    actor Admin as 관리자
    participant UI as /admin 콘솔
    participant Settings as Settings Engine
    participant DB as PostgreSQL DB

    Admin->>UI: 설정 키 변경 (예: auth.oidc.client_id)
    UI->>Settings: UpdateSetting(Key, Value, Summary)
    Settings->>DB: INSERT INTO setting_revisions (Revision, Key, Value, Actor)
    Settings->>DB: UPDATE system_settings
    Settings-->>UI: Revision #5 생성 및 성공 응답

    Note over Admin, DB: 문제 발생 시 Past Revision으로 즉시 복원
    Admin->>UI: [Revision #4로 복원] 클릭
    UI->>Settings: RollbackSetting(Key, Revision=4)
    Settings->>DB: 이전 값 복원 및 Revision #6 기록
    Settings-->>UI: 원복 완료
```

---

### 4.2 Keycloak OIDC SSO 연동 절차

#### 1. Keycloak 설정
- Keycloak 관리 콘솔에서 신규 Client 생성: `kanpic-web-client`
- Access Type: `confidential` (또는 `public` with PKCE)
- Valid Redirect URIs: `http://<kanpic-주소>:8080/auth/callback`

#### 2. `/admin` 콘솔 연동 적용
1. `/admin` -> **Keycloak OIDC 간편 연결** 섹션으로 이동.
2. `Issuer URL`: `https://auth.company.com/realms/enterprise` 입력.
3. `Client ID`: `kanpic-web-client` 입력.
4. `Client Secret`: Confidential Client인 경우 Secret 입력.
5. **[연결 검증 테스트]** 버튼 클릭: 서버가 Issuer의 `.well-known/openid-configuration` 엔드포인트를 호출하여 OIDC Discovery 및 Token Endpoint 유효성을 자동 검사합니다.
6. 검증 성공 시 OIDC 활성화 토글을 ON으로 전환합니다.

---

### 4.3 전체 API 키 통합 감사 및 회전/폐기
- **전체 감사**: 시스템 내 발급된 모든 API 키의 소유자, 부여된 Scope, 생성일시, 마지막 사용 시각을 모니터링합니다.
- **보안 회전 (Key Rotation)**: 기존 키의 접근 권한을 무효화하고 동일한 Scope를 가진 신규 키를 1회 안전하게 발급합니다.
- **즉시 폐기 (Revocation)**: API 키 유출 정황 발견 시 해당 키를 즉각 무효화(Revoke) 처리합니다.
- **보안 매커니즘**: 데이터베이스에는 SHA-256 해시값만 저장되므로 데이터베이스 백업 파일이 유출되더라도 원문 키는 안전합니다.

---

## 5. Model Context Protocol (MCP) 서버 운영

kanpic은 AI 에이전트 연동을 위한 **Streamable HTTP MCP JSON-RPC 2.0** 엔드포인트를 제공합니다.

- **MCP Endpoint**: `POST /mcp`
- **보안 인가**: 요청 Header의 Bearer Token (API Key)에서 `mcp.use` 스코프 권한을 검증합니다.
- **제공 MCP Tools**:
  - `kanpic_list_workbooks`: 사용자가 접근 가능한 워크북 목록 조회
  - `kanpic_read_sheet`: 시트 셀 범위 데이터 조회
  - `kanpic_apply_mutations`: 셀 값 및 수식 변형 커밋
  - `kanpic_set_data_validation`: 데이터 유효성 검사 규칙 적용
  - `kanpic_export_workbook`: XLSX/CSV 바이너리 내보내기

---

## 6. 백업 및 재해 복구 (Backup & Disaster Recovery)

### 6.1 크론탭 자동 백업 스크립트

다음 스크립트를 `/opt/kanpic/scripts/backup.sh`로 저장하고 Cron에 등록합니다.

```bash
#!/bin/bash
# /opt/kanpic/scripts/backup.sh
BACKUP_DIR="/backups/kanpic"
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
CONTAINER_NAME="kanpic-postgres"

mkdir -p ${BACKUP_DIR}

# PostgreSQL 백업 실행
docker exec -t ${CONTAINER_NAME} pg_dump -U kanpic -d kanpic -F c -b -v -f /tmp/backup_${TIMESTAMP}.dump

# 백업 파일 호스트로 복사 및 오래된 백업 정리 (30일 초과 삭제)
docker cp ${CONTAINER_NAME}:/tmp/backup_${TIMESTAMP}.dump ${BACKUP_DIR}/
docker exec -t ${CONTAINER_NAME} rm -f /tmp/backup_${TIMESTAMP}.dump
find ${BACKUP_DIR} -name "*.dump" -mtime +30 -delete

echo "[${TIMESTAMP}] kanpic DB Backup Completed Successfully."
```

```bash
# 매일 새벽 3시 백업 실행 크론탭 설정
0 3 * * * /opt/kanpic/scripts/backup.sh >> /var/log/kanpic_backup.log 2>&1
```

---

## 7. 운영 FAQ 및 문제 해결 (Troubleshooting Guide)

> [!WARNING]
> **장애 1. 로그인 시 500 Internal Server Error 발생**  
> **원인**: `BOOTSTRAP_ADMIN_ID`와 `BOOTSTRAP_ADMIN_PASSWORD` 환경 변수 중 하나만 입력된 경우 보안 정책에 의해 서버 동작이 차단됩니다.  
> **조치**: 환경 변수 두 값을 모두 지정하거나 두 값 모두 삭제 후 재시작합니다.

> [!IMPORTANT]
> **장애 2. Keycloak SSO 로그인 시 Redirection mismatch 에러 발생**  
> **원인**: Keycloak Client의 Valid Redirect URIs에 kanpic 콜백 URL(`http://<kanpic-host>:8080/auth/callback`)이 포함되지 않은 경우입니다.  
> **조치**: Keycloak 관리자 콘솔에서 해당 Redirect URI를 정확히 추가 등록합니다.
