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
VERSION=v0.19.0
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

CSV·TSV·XLSX는 홈 화면에서 미리보기 후 원자적으로 가져올 수 있습니다. 편집기에서는 시트 간 참조와 이름 범위를 포함한 수식을 사용할 수 있고, 행·열 삽입/삭제 시 수식·병합·이름 범위·검증·필터·댓글 참조가 함께 이동하며 변경 직전 복구 버전이 자동 생성됩니다. 행 높이·열 너비·행/열 숨김·고정 영역도 PostgreSQL에 버전 관리되며 공동 편집자에게 동기화됩니다. 표시 형식, 가로·세로 정렬, 텍스트 넘침·자르기·자동 줄바꿈, 회전 및 범위 테두리는 값과 수식을 보존하는 원자적 서식 작업으로 저장되며 실행 취소할 수 있습니다. XLSX Import/Export는 이 공통 서식 모델을 사용해 글꼴·색상·채우기·정렬·줄바꿈·표시 형식·테두리를 왕복 변환합니다. `Ctrl/⌘+F` 또는 `Ctrl/⌘+K`로 서버에 저장된 워크북 전체의 값·수식을 검색하고 다른 시트의 결과 셀로 바로 이동할 수 있습니다. 대소문자 구분, 셀 전체 일치, 정규식, 수식 제외와 현재 시트 범위를 조합할 수 있고 `Ctrl/⌘+H`의 찾기 및 바꾸기는 변경 대상 셀 수를 미리 확인한 뒤 시트별 원자적·실행 취소 가능한 작업으로 반영합니다. 같은 기능은 REST `GET /api/v1/workbooks/{workbookId}/search`, `POST /api/v1/workbooks/{workbookId}/search:replace`와 MCP `spreadsheet.workbook.search`, `spreadsheet.workbook.replace`로 제공됩니다.

편집기의 상단 메뉴 막대(파일·수정·보기·삽입·서식·데이터·도구·도움말)와 셀·행 머리글·열 머리글·모서리 컨텍스트 메뉴는 같은 명령 집합을 제공합니다. 머리글을 클릭하거나 드래그해 행과 열 전체를 선택하고, 머리글 경계를 드래그하면 안내선과 픽셀 값을 보며 크기를 조절하며, 더블클릭하면 셀 내용에 맞춰 자동으로 맞춥니다. 여러 행·열을 선택하면 삽입·삭제·숨기기·크기 조절이 한 번에 적용됩니다. `Ctrl/⌘ + \``로 수식 보기, `Alt + =`로 자동 합계, `Ctrl/⌘ + ;`로 오늘 날짜, `Ctrl/⌘ + Alt + =`/`-`로 행·열 삽입·삭제, `Ctrl/⌘ + PageUp`/`PageDown`으로 시트 전환을 사용할 수 있고 전체 목록은 `Ctrl/⌘ + /`에서 확인합니다.

편집기 오른쪽 아래 **차트** 버튼에서는 선택 범위로 막대·선·영역·원형·분산·히스토그램 차트를 만들 수 있습니다. 차트는 서버에 버전 관리되는 독립 엔터티이며 최신 셀 값과 수식 결과를 사용해 자동 갱신됩니다. 시트 위에서 이동·크기 조절하고 SVG 또는 PNG로 내보낼 수 있으며, REST `/api/v1/workbooks/{workbookId}/charts`, `/api/v1/charts/{chartId}`와 `spreadsheet.chart.*` MCP 도구가 같은 계약을 제공합니다.

**피벗** 버튼에서는 선택 범위를 행·열로 그룹화하고 합계·평균·개수·최소·최대 집계, 필터, 날짜·숫자·사용자 그룹과 계산 필드를 구성할 수 있습니다. 피벗 정의와 수동 갱신 캐시는 PostgreSQL에 저장되며 자동/수동 새로 고침, 전체 합계, 집계 셀의 원본 행 드릴다운, 구조 변경·복제·버전 복원을 지원합니다. REST `/api/v1/workbooks/{workbookId}/pivots`, `/api/v1/pivots/{pivotId}`와 `spreadsheet.pivot.*` MCP 도구는 동일한 권한·검증 계약을 사용합니다.

툴바의 **조건부 서식**에서는 값 비교, 텍스트 포함, 빈 값, 중복·고유 값, 2색·3색 범위와 데이터 막대를 설정할 수 있습니다. 규칙은 우선순위와 `조건이 참이면 중지` 정책에 따라 서버에서 평가되고 Canvas의 보이는 셀에만 합성되므로 원본 값·수식·기본 서식을 변경하지 않습니다. 정의는 PostgreSQL 및 워크북 버전에 보존되며 행·열 구조 변경, 시트·워크북 복제와 복원에도 포함됩니다. REST `/api/v1/sheets/{sheetId}/conditional-formats`와 `spreadsheet.conditional_format.*` MCP 도구는 `format.read`/`format.write` scope를 공유합니다.

동일 셀을 두 사용자가 같은 기준 버전에서 수정하면 마지막 입력은 화면에 임시 반영하되 충돌을 별도 PostgreSQL 기록으로 남깁니다. 편집기 상단의 주황색 충돌 배지 또는 **편집 충돌** 도구를 열면 충돌 전 기준, 먼저 반영된 상대 변경, 당시 제출값과 현재 서버값을 값·수식·서식 단위로 비교할 수 있습니다. **현재 값 유지** 또는 **먼저 반영된 값 복원** 결정은 새로운 서버 작업과 워크북 버전으로 기록되며, 복원 전 같은 셀이 다시 바뀌었다면 안전하게 거부됩니다. REST `/api/v1/workbooks/{workbookId}/conflicts`, `/api/v1/conflicts/{conflictId}:resolve`와 MCP `spreadsheet.conflict.list|get|resolve`가 같은 `range.read`/`range.write` 계약을 제공합니다.

편집기의 **AI 도우미**는 관리자가 등록한 사내 OpenAI 호환 LLM Gateway만 사용합니다. 사용자가 선택한 범위만 모델에 전달해 수식 생성·설명·오류 수정뿐 아니라 범위 요약, 이상치 탐지와 데이터 정제를 수행합니다. 설명·요약·이상치 탐지는 읽기 전용이고, 수식 또는 정제 변경은 셀별 미리보기와 명시적 승인 전에는 워크북을 바꾸지 않습니다. 승인 시 계획 당시 워크북 버전과 셀 값을 다시 검사하여 하나의 원자적 서버 작업으로 반영하고 즉시 Undo할 수 있습니다. 계획·모델·도구·승인·Undo 이력은 PostgreSQL과 감사 로그에 보존됩니다. REST `/api/v1/ai/*`와 MCP `spreadsheet.ai.action.*`가 같은 `ai.use` 및 작업별 최소 scope 계약을 사용합니다.

편집기 오른쪽 아래 **자동화** 버튼에서는 수동 실행, 특정 셀 범위 변경, 5필드 Cron 일정 또는 개인 API 키로 인증한 인바운드 웹훅을 조건으로 값 설정, 상대 참조 수식 적용과 내용 지우기 작업을 정의할 수 있습니다. 일정에는 IANA 시간대(기본 `UTC`)를 지정하며 다음 실행 시각과 성공·변경 없음·실패 이력을 확인할 수 있습니다. 웹훅은 `automation.webhook.invoke` scope와 `Idempotency-Key`가 필수이고 최대 1MiB JSON 원문은 저장하지 않으며 SHA-256·크기·호출 키 ID만 감사에 보존합니다. 저장 직후 최신 서버 셀 기준 미리보기를 확인하고 명시적으로 실행하며, 실행 이력에서 성공 작업을 Undo할 수 있습니다. 정의 revision, 예약 시각, 실행 기준 버전, 변경 전 셀 스냅샷, 작업 ID와 감사 이력은 PostgreSQL에 보존되고 모든 계약은 REST와 `spreadsheet.automation.*` MCP 도구로 동일하게 제공됩니다. 자동화는 기본 비활성화이며 관리자가 `/admin`의 **워크북 자동화 실행 정책**에서 셀 수, 시간당 실행 수와 스케줄러 확인 주기를 검증한 뒤 활성화합니다.

툴바의 댓글 버튼에서는 현재 셀 또는 범위에 스레드를 만들고 답글·수정·삭제·해결·재열기를 수행할 수 있습니다. 본문에서 `@사용자ID` 또는 `@이메일`을 입력하면 상단 알림 메뉴에 멘션이 표시되고, 알림을 선택하면 원래 시트와 범위로 이동합니다. 댓글은 HTML로 렌더링하지 않으며 revision 기반 충돌 방지와 idempotency key를 사용합니다. REST의 `/api/v1/workbooks/{workbookId}/comments`, `/api/v1/comments/{commentId}`, `/api/v1/me/notifications` 계약은 `spreadsheet.comment.*`와 `spreadsheet.notification.*` MCP 도구로도 동일하게 제공됩니다.

릴리즈 번들은 다음 명령으로 만듭니다. GitHub Release에는 버전별 핵심 변경, 검증 결과, 업그레이드 참고와 오프라인 설치 절차를 담은 릴리즈 노트가 함께 게시됩니다.

```bash
./scripts/release.sh vX.Y.Z
```

## 문의 및 연락처 (Contact & Support)

- **도입 & 구축 문의 이메일**: [gagagiga@naver.com](mailto:gagagiga@naver.com)
- **GitHub Pages 홍보 페이지**: [https://hkjang.github.io/kanpic/](https://hkjang.github.io/kanpic/)
