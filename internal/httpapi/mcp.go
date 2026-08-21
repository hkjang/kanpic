package httpapi

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"kanpic/internal/ai"
	"kanpic/internal/apikey"
	"kanpic/internal/automation"
	"kanpic/internal/importexport"
	"kanpic/internal/settings"
	"kanpic/internal/workbook"
	"kanpic/pkg/cellrange"
)

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Meta        map[string]any `json:"_meta,omitempty"`
}

var mcpTools = []mcpTool{
	tool("platform.version.get", "kanpic 서버 빌드 버전을 조회합니다.", "", nil),
	tool("platform.auth.config", "OIDC 로그인 활성화 상태를 조회합니다.", "", nil),
	tool("spreadsheet.workbook.list", "접근 가능한 워크북을 조회합니다.", "workbook.read", props("workspace_id", "string")),
	tool("spreadsheet.workbook.get", "워크북 메타데이터와 시트를 조회합니다.", "workbook.read", requiredProps("workbook_id", "string")),
	tool("spreadsheet.workbook.search", "워크북 전체의 셀 값과 수식을 대소문자·전체 셀·정규식·시트 범위 옵션으로 검색하고 시트·A1 주소를 반환합니다.", "workbook.read", workbookSearchSchema()),
	tool("spreadsheet.workbook.replace", "검색 조건과 동일한 셀의 값 또는 수식을 미리보기하거나 시트별 원자적·실행 취소 가능한 작업으로 바꿉니다.", "range.write", workbookReplaceSchema()),
	tool("spreadsheet.workbook.create", "워크북과 첫 시트를 생성합니다. template_id를 주면 템플릿 내용으로 채웁니다.", "workbook.write", workbookCreateSchema()),
	tool("spreadsheet.template.list", "바로 사용할 수 있는 워크북 템플릿 목록을 반환합니다.", "workbook.read", map[string]any{"type": "object"}),
	tool("spreadsheet.workbook.duplicate", "워크북의 시트, 셀, 수식, 서식과 속성을 새 워크북으로 원자적으로 복제합니다.", "workbook.write", requiredProps("workbook_id", "string")),
	tool("spreadsheet.workbook.update", "워크북 이름 또는 즐겨찾기를 변경합니다.", "workbook.write", requiredProps("workbook_id", "string")),
	tool("spreadsheet.workbook.delete", "워크북을 삭제합니다.", "workbook.write", requiredProps("workbook_id", "string")),
	tool("spreadsheet.share.list", "워크북의 공유 대상과 링크 액세스, 호출자의 유효 권한을 조회합니다.", "workbook.read", requiredProps("workbook_id", "string")),
	tool("spreadsheet.share.grant", "사용자·부서·역할에 뷰어·댓글 작성자·편집자 권한을 부여하거나 변경합니다.", "workbook.write", shareGrantSchema()),
	tool("spreadsheet.share.revoke", "공유 항목을 제거합니다.", "workbook.write", requiredProps2("workbook_id", "string", "share_id", "string")),
	tool("spreadsheet.share.link", "링크 액세스 범위와 역할, 편집자 공유 허용과 뷰어 복사 허용을 변경합니다.", "workbook.write", shareLinkSchema()),
	tool("spreadsheet.share.transfer_ownership", "워크북 소유자를 변경합니다.", "workbook.write", requiredProps2("workbook_id", "string", "new_owner_id", "string")),
	tool("spreadsheet.access_request.list", "워크북의 액세스 요청을 조회합니다.", "workbook.read", requiredProps("workbook_id", "string")),
	tool("spreadsheet.access_request.decide", "액세스 요청을 승인하거나 거부합니다.", "workbook.write", accessRequestDecisionSchema()),
	tool("spreadsheet.department.list", "부서 계층과 구성원 수를 조회합니다.", "workbook.read", map[string]any{"type": "object", "properties": map[string]any{}}),
	tool("spreadsheet.department.create", "부서를 생성합니다.", "admin.*", departmentSchema()),
	tool("spreadsheet.department.update", "부서 이름, 설명 또는 상위 부서를 변경합니다.", "admin.*", requiredProps("department_id", "string")),
	tool("spreadsheet.department.delete", "하위 부서가 없는 부서를 삭제하고 관련 공유를 정리합니다.", "admin.*", requiredProps("department_id", "string")),
	tool("spreadsheet.department.add_members", "부서에 구성원을 추가합니다.", "admin.*", requiredProps2("department_id", "string", "user_ids", "array")),
	tool("spreadsheet.department.remove_member", "부서에서 구성원을 제거합니다.", "admin.*", requiredProps2("department_id", "string", "user_id", "string")),
	tool("spreadsheet.sheet.list", "워크북의 시트를 조회합니다.", "workbook.read", requiredProps("workbook_id", "string")),
	tool("spreadsheet.sheet.create", "워크북에 시트를 추가합니다.", "workbook.write", requiredProps2("workbook_id", "string", "name", "string")),
	tool("spreadsheet.sheet.duplicate", "시트의 셀, 수식, 서식과 속성을 원자적으로 복제합니다.", "workbook.write", requiredProps("sheet_id", "string")),
	tool("spreadsheet.sheet.update", "시트 이름, 순서, 색상, 숨김 상태를 변경합니다.", "workbook.write", requiredProps("sheet_id", "string")),
	tool("spreadsheet.sheet.delete", "시트를 삭제합니다.", "workbook.write", requiredProps("sheet_id", "string")),
	tool("spreadsheet.workbook.trash", "삭제된 워크북 목록을 조회합니다.", "workbook.read", map[string]any{"type": "object", "properties": map[string]any{"workspace_id": map[string]any{"type": "string"}}}),
	tool("spreadsheet.workbook.restore", "삭제된 워크북을 복원합니다.", "workbook.write", requiredProps("workbook_id", "string")),
	tool("spreadsheet.workbook.purge", "삭제된 워크북을 완전히 제거합니다.", "workbook.write", requiredProps("workbook_id", "string")),
	tool("spreadsheet.workbook.favorite", "호출자의 개인 즐겨찾기를 설정하거나 해제합니다.", "workbook.read", requiredProps2("workbook_id", "string", "favorite", "boolean")),
	tool("spreadsheet.sheet.stats", "시트별 데이터 양, 수식 수, 사용 범위와 마지막 변경 시각을 조회합니다.", "workbook.read", requiredProps("workbook_id", "string")),
	tool("spreadsheet.sheet.copy", "시트를 셀·서식·레이아웃과 함께 다른 워크북으로 복사합니다.", "workbook.write", requiredProps2("sheet_id", "string", "target_workbook_id", "string")),
	tool("spreadsheet.named_range.list", "워크북의 이름 범위 정의와 revision을 조회합니다.", "workbook.read", requiredProps("workbook_id", "string")),
	tool("spreadsheet.named_range.get", "이름 범위 정의를 조회합니다.", "workbook.read", requiredProps("named_range_id", "string")),
	tool("spreadsheet.named_range.create", "수식과 API에서 재사용할 워크북 이름 범위를 멱등 생성합니다.", "formula.write", namedRangeSchema(true)),
	tool("spreadsheet.named_range.update", "expected_revision으로 이름 범위의 이름이나 대상을 변경합니다.", "formula.write", namedRangeSchema(false)),
	tool("spreadsheet.named_range.delete", "이름 범위를 삭제하고 종속 수식을 #NAME?으로 재계산합니다.", "formula.write", namedRangeDeleteSchema()),
	tool("spreadsheet.range.read", "A1 범위의 비어 있지 않은 셀을 조회합니다.", "range.read", requiredProps2("sheet_id", "string", "range", "string")),
	tool("spreadsheet.range.write", "최대 1천 셀을 멱등 일괄 변경합니다.", "range.write", requiredProps2("sheet_id", "string", "idempotency_key", "string")),
	tool("spreadsheet.range.paste", "최대 1만 셀을 하나의 원자적 작업으로 붙여넣습니다.", "range.write", requiredProps2("sheet_id", "string", "idempotency_key", "string")),
	tool("spreadsheet.range.fill", "계산된 자동 채우기 셀 최대 1만 개를 하나의 원자적·실행 취소 가능한 작업으로 적용합니다.", "range.write", requiredProps2("sheet_id", "string", "idempotency_key", "string")),
	tool("spreadsheet.range.format", "값과 수식을 보존하며 최대 1만 셀의 서식과 범위 테두리를 원자적으로 병합합니다.", "format.write", rangeFormatSchema()),
	tool("spreadsheet.range.merge", "값과 수식을 보존한 채 선택 범위를 원자적으로 병합합니다.", "format.write", requiredProps3("sheet_id", "string", "range", "string", "idempotency_key", "string")),
	tool("spreadsheet.range.unmerge", "선택한 병합 범위를 원자적으로 해제합니다.", "format.write", requiredProps3("sheet_id", "string", "range", "string", "idempotency_key", "string")),
	tool("spreadsheet.range.sort", "선택 범위를 다중 키로 안정 정렬하고 수식과 서식을 함께 이동합니다.", "range.write", requiredProps4("sheet_id", "string", "range", "string", "keys", "array", "idempotency_key", "string")),
	tool("spreadsheet.structure.apply", "행 또는 열을 원자적으로 삽입·삭제하고 수식, 병합, 이름 범위, 검증, 조건부 서식 및 필터 참조를 갱신합니다.", "range.write", structureSchema()),
	tool("spreadsheet.layout.apply", "행 높이·열 너비·숨김·고정 영역을 revision 기반으로 멱등 변경합니다.", "format.write", sheetLayoutSchema()),
	tool("spreadsheet.filter_view.list", "현재 사용자가 저장한 시트 필터 보기를 조회합니다.", "range.read", requiredProps("sheet_id", "string")),
	tool("spreadsheet.filter_view.get", "현재 사용자의 필터 보기 정의를 조회합니다.", "range.read", requiredProps("filter_view_id", "string")),
	tool("spreadsheet.filter_view.create", "값·조건·색상 기준의 사용자별 필터 보기를 멱등 생성합니다.", "range.write", filterViewSchema(true)),
	tool("spreadsheet.filter_view.update", "필터 보기 정의나 활성 상태를 변경합니다.", "range.write", filterViewSchema(false)),
	tool("spreadsheet.filter_view.delete", "현재 사용자의 필터 보기를 삭제합니다.", "range.write", requiredProps("filter_view_id", "string")),
	tool("spreadsheet.filter_view.evaluate", "최신 서버 셀을 기준으로 숨길 행을 평가합니다.", "range.read", requiredProps("filter_view_id", "string")),
	tool("spreadsheet.data_validation.list", "시트의 데이터 검증과 컬러 드롭다운 규칙을 조회합니다.", "range.read", requiredProps("sheet_id", "string")),
	tool("spreadsheet.data_validation.get", "데이터 검증 규칙과 revision을 조회합니다.", "range.read", requiredProps("validation_id", "string")),
	tool("spreadsheet.data_validation.create", "목록·숫자·날짜·사용자 수식 검증 규칙을 멱등 생성합니다.", "range.write", dataValidationSchema(true)),
	tool("spreadsheet.data_validation.update", "expected_revision으로 데이터 검증 규칙을 안전하게 변경합니다.", "range.write", dataValidationSchema(false)),
	tool("spreadsheet.data_validation.delete", "데이터 검증 규칙을 삭제합니다.", "range.write", dataValidationDeleteSchema()),
	tool("spreadsheet.data_validation.evaluate", "기존 범위 값을 데이터 검증 규칙으로 검사합니다.", "range.read", requiredProps("validation_id", "string")),
	tool("spreadsheet.conditional_format.list", "시트의 조건부 서식 규칙을 우선순위 순서로 조회합니다.", "format.read", requiredProps("sheet_id", "string")),
	tool("spreadsheet.conditional_format.get", "조건부 서식 정의와 revision을 조회합니다.", "format.read", requiredProps("conditional_format_id", "string")),
	tool("spreadsheet.conditional_format.evaluate", "범위의 값 비교·중복·색상 범위·데이터 막대 렌더링 결과를 계산합니다.", "format.read", conditionalFormatEvaluationSchema()),
	tool("spreadsheet.conditional_format.create", "시트 범위에 조건부 서식 규칙을 멱등 생성합니다.", "format.write", conditionalFormatSchema(true)),
	tool("spreadsheet.conditional_format.update", "조건부 서식 규칙을 revision 기반으로 변경합니다.", "format.write", conditionalFormatSchema(false)),
	tool("spreadsheet.conditional_format.delete", "조건부 서식 규칙을 삭제합니다.", "format.write", conditionalFormatDeleteSchema()),
	tool("spreadsheet.comment.list", "워크북 또는 시트의 댓글 스레드와 답글을 조회합니다.", "comment.read", commentListSchema()),
	tool("spreadsheet.comment.get", "댓글 스레드와 전체 답글을 조회합니다.", "comment.read", requiredProps("comment_id", "string")),
	tool("spreadsheet.comment.create", "셀 또는 범위에 멱등 댓글 스레드를 생성하고 @멘션 알림을 만듭니다.", "comment.write", commentCreateSchema()),
	tool("spreadsheet.comment.reply", "댓글 스레드에 멱등 답글을 추가하고 @멘션 알림을 만듭니다.", "comment.write", commentReplySchema()),
	tool("spreadsheet.comment.resolve", "revision을 확인하여 댓글 스레드를 해결 또는 재열기합니다.", "comment.write", commentResolveSchema()),
	tool("spreadsheet.comment.delete", "자신이 만든 댓글 스레드를 삭제합니다.", "comment.write", requiredProps("comment_id", "string")),
	tool("spreadsheet.comment.message.update", "자신의 댓글 메시지를 revision 기반으로 수정합니다.", "comment.write", commentMessageUpdateSchema()),
	tool("spreadsheet.comment.message.delete", "자신의 댓글 메시지를 revision 기반으로 삭제합니다.", "comment.write", commentMessageDeleteSchema()),
	tool("spreadsheet.notification.list", "현재 사용자에게 온 @멘션 알림을 조회합니다.", "comment.read", notificationListSchema()),
	tool("spreadsheet.notification.mark_read", "현재 사용자의 @멘션 알림을 읽음 처리합니다.", "comment.write", requiredProps("notification_id", "string")),
	tool("spreadsheet.chart.list", "워크북 또는 시트의 차트 정의를 조회합니다.", "chart.read", chartListSchema()),
	tool("spreadsheet.chart.get", "차트 정의와 revision을 조회합니다.", "chart.read", requiredProps("chart_id", "string")),
	tool("spreadsheet.chart.data", "최신 셀 값으로 계산된 차트 계열과 점을 조회합니다.", "chart.read", requiredProps("chart_id", "string")),
	tool("spreadsheet.chart.create", "셀 범위를 데이터 원본으로 사용하는 차트를 멱등 생성합니다.", "chart.write", chartSchema(true)),
	tool("spreadsheet.chart.update", "차트 유형, 원본, 제목, 범례 및 배치를 revision 기반으로 변경합니다.", "chart.write", chartSchema(false)),
	tool("spreadsheet.chart.delete", "차트 정의를 삭제합니다.", "chart.write", chartDeleteSchema()),
	tool("spreadsheet.pivot.list", "워크북 또는 시트의 관리형 피벗 정의를 조회합니다.", "pivot.read", pivotListSchema()),
	tool("spreadsheet.pivot.get", "피벗 정의, 갱신 방식과 revision을 조회합니다.", "pivot.read", requiredProps("pivot_id", "string")),
	tool("spreadsheet.pivot.data", "피벗의 집계 결과, 행·열 그룹과 총계를 조회합니다.", "pivot.read", requiredProps("pivot_id", "string")),
	tool("spreadsheet.pivot.drilldown", "피벗 집계 셀에 기여한 원본 행을 페이지 단위로 조회합니다.", "pivot.read", pivotDrilldownSchema()),
	tool("spreadsheet.pivot.create", "행·열·값·필터·계산 필드를 가진 관리형 피벗을 멱등 생성합니다.", "pivot.write", pivotSchema(true)),
	tool("spreadsheet.pivot.update", "피벗 정의와 자동·수동 갱신 방식을 revision 기반으로 변경합니다.", "pivot.write", pivotSchema(false)),
	tool("spreadsheet.pivot.refresh", "최신 원본 셀로 피벗을 즉시 다시 계산하고 수동 캐시를 갱신합니다.", "pivot.write", requiredProps("pivot_id", "string")),
	tool("spreadsheet.pivot.delete", "피벗 정의와 계산 캐시를 삭제합니다.", "pivot.write", pivotDeleteSchema()),
	tool("spreadsheet.formula.set", "셀 수식을 멱등 설정합니다.", "formula.write", requiredProps2("sheet_id", "string", "idempotency_key", "string")),
	tool("spreadsheet.formula.evaluate", "MVP 수식과 제공된 A1 셀 값을 서버에서 계산합니다.", "formula.read", requiredProps("formula", "string")),
	tool("spreadsheet.formula.explain", "저장된 수식과 직접 의존 셀·종속 수식을 조회합니다.", "formula.read", requiredProps2("sheet_id", "string", "address", "string")),
	tool("spreadsheet.operation.undo", "자신의 작업을 후속 변경과 충돌하지 않는 셀만 선택적으로 되돌립니다. Undo 작업을 다시 되돌리면 Redo가 됩니다.", "range.write", requiredProps2("operation_id", "string", "idempotency_key", "string")),
	tool("spreadsheet.conflict.list", "워크북의 열린 동일 셀 충돌과 선택적으로 해소된 이력을 조회합니다.", "range.read", conflictListSchema()),
	tool("spreadsheet.conflict.get", "충돌 전 값, 상대 사용자 값, 제출 값과 현재 서버 값을 비교 조회합니다.", "range.read", requiredProps("conflict_id", "string")),
	tool("spreadsheet.conflict.resolve", "동일 셀 충돌을 현재 값 유지 또는 상대 사용자 값 복원으로 멱등 해소하고 버전 이력에 남깁니다.", "range.write", conflictResolutionSchema()),
	tool("spreadsheet.ai.config.get", "활성화 여부, 모델과 셀·변경 한도를 비밀정보 없이 조회합니다.", "ai.use", nil),
	tool("spreadsheet.ai.action.plan", "선택 범위만 사내 LLM Gateway에 전달하여 수식·요약·이상치·정제 계획과 비파괴 미리보기를 만듭니다.", "ai.use", aiPlanSchema()),
	tool("spreadsheet.ai.action.list", "현재 사용자가 생성한 워크북 AI 작업 이력을 조회합니다.", "ai.use", requiredProps("workbook_id", "string")),
	tool("spreadsheet.ai.action.get", "AI 계획, 변경 미리보기, 모델·도구 감사 이력과 적용 상태를 조회합니다.", "ai.use", requiredProps("action_id", "string")),
	tool("spreadsheet.ai.action.approve", "미리 본 AI 수식 또는 데이터 정제 계획을 revision과 워크북 버전을 확인한 뒤 원자적으로 적용합니다.", "ai.use", aiExecutionSchema()),
	tool("spreadsheet.ai.action.undo", "승인된 AI 작업을 사용자별 작업 이력으로 안전하게 되돌립니다.", "ai.use", aiExecutionSchema()),
	tool("spreadsheet.automation.list", "워크북의 수동·셀 변경·Cron 스케줄 자동화 정의, revision과 다음 실행 시각을 조회합니다.", "automation.read", requiredProps("workbook_id", "string")),
	tool("spreadsheet.automation.get", "자동화 트리거와 셀 작업 정의를 조회합니다.", "automation.read", requiredProps("automation_id", "string")),
	tool("spreadsheet.automation.create", "수동, 셀 변경 또는 시간대 기반 Cron 트리거와 원자적 값·수식 작업을 멱등 생성합니다.", "automation.write", automationCreateSchema()),
	tool("spreadsheet.automation.update", "자동화 정의와 활성 상태를 revision 기반으로 변경합니다.", "automation.write", automationUpdateSchema()),
	tool("spreadsheet.automation.delete", "자동화를 revision 기반으로 비활성화하고 삭제합니다.", "automation.write", automationDeleteSchema()),
	tool("spreadsheet.automation.test", "최신 서버 셀을 사용해 쓰기 없이 자동화 변경 미리보기를 검증합니다.", "automation.read", requiredProps("automation_id", "string")),
	tool("spreadsheet.automation.run", "test 응답의 automation_revision과 base_version을 검증해 자동화를 멱등 실행하고 하나의 서버 권위 셀 작업으로 반영합니다.", "automation.run", automationRunSchema()),
	tool("spreadsheet.automation.webhook.invoke", "개인 API 키로 인증한 JSON webhook payload의 digest만 감사에 보존하고 자동화를 멱등 실행합니다.", "automation.webhook.invoke", automationWebhookSchema()),
	tool("spreadsheet.automation.run.list", "자동화 실행·실패·Undo 이력을 조회합니다.", "automation.read", requiredProps("automation_id", "string")),
	tool("spreadsheet.automation.run.undo", "성공한 자동화 실행을 후속 변경과 충돌하지 않는 범위에서 되돌립니다.", "automation.run", automationUndoSchema()),
	tool("spreadsheet.import.preview", "Base64 CSV, TSV 또는 XLSX를 저장 전에 검사합니다.", "import.write", requiredProps2("file_name", "string", "data_base64", "string")),
	tool("spreadsheet.import.execute", "파일을 하나의 원자적 트랜잭션으로 워크북에 가져옵니다.", "import.write", requiredProps2("file_name", "string", "data_base64", "string")),
	tool("spreadsheet.export.execute", "워크북을 CSV, TSV, JSON 또는 XLSX Base64 파일로 내보냅니다.", "export.read", requiredProps2("workbook_id", "string", "format", "string")),
	tool("spreadsheet.version.create", "현재 워크북 스냅샷 버전을 생성합니다.", "version.write", requiredProps("workbook_id", "string")),
	tool("spreadsheet.version.list", "워크북 버전 목록을 조회합니다.", "version.read", requiredProps("workbook_id", "string")),
	tool("spreadsheet.version.restore", "선택한 버전을 최신 버전으로 복원합니다.", "version.write", requiredProps("version_id", "string")),
	tool("profile.preferences.get", "현재 사용자의 개인 설정을 조회합니다.", "profile.read", nil),
	tool("profile.preferences.update", "현재 사용자의 개인 설정을 저장합니다.", "profile.write", requiredProps("values", "object")),
	tool("profile.api_key.list", "현재 사용자의 API 키 메타데이터를 조회합니다.", "api_keys.read", nil),
	tool("profile.api_key.create", "새 API 키를 만들고 원문을 한 번 반환합니다.", "api_keys.write", requiredProps2("name", "string", "scopes", "array")),
	tool("profile.api_key.update", "API 키 이름, scope, 만료를 변경합니다.", "api_keys.write", requiredProps("key_id", "string")),
	tool("profile.api_key.revoke", "API 키를 폐기합니다.", "api_keys.write", requiredProps("key_id", "string")),
	tool("profile.api_key.rotate", "기존 키를 폐기하고 같은 정책의 새 키를 발급합니다.", "api_keys.write", requiredProps("key_id", "string")),
	tool("admin.settings.list", "전체 시스템 설정을 조회합니다.", "admin.*", nil),
	tool("admin.settings.upsert", "시스템 설정을 추가하거나 변경하고 버전을 생성합니다.", "admin.*", requiredProps2("key", "string", "value_type", "string")),
	tool("admin.settings.delete", "시스템 설정을 삭제하고 버전을 생성합니다.", "admin.*", requiredProps("key", "string")),
	tool("admin.settings.validate", "현재 시스템 설정 전체를 검증합니다.", "admin.*", nil),
	tool("admin.settings.test", "PostgreSQL과 활성화된 OIDC 연결을 시험합니다.", "admin.*", nil),
	tool("admin.settings.version.list", "설정 변경 버전 목록을 조회합니다.", "admin.*", nil),
	tool("admin.settings.version.restore", "설정 스냅샷을 복원합니다.", "admin.*", requiredProps("revision", "number")),
	tool("admin.logs.list", "서버 구조화 로그를 검색합니다.", "admin.*", props("query", "string")),
	tool("admin.logs.purge", "기준 시각 이전 서버 로그를 삭제합니다.", "admin.*", requiredProps("before", "string")),
}

func (s *Server) mcp(w http.ResponseWriter, r *http.Request) {
	var request mcpRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.JSONRPC != "2.0" {
		s.writeMCPError(w, request.ID, -32600, "JSON-RPC 2.0이 필요합니다.")
		return
	}
	switch request.Method {
	case "initialize":
		writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{"listChanged": false}}, "serverInfo": map[string]string{"name": "kanpic", "version": s.build.Version}}})
	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
	case "tools/list":
		writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"tools": mcpTools}})
	case "tools/call":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil {
			s.writeMCPError(w, request.ID, -32602, "도구 인수가 올바르지 않습니다.")
			return
		}
		result, err := s.callMCPTool(r, params.Name, params.Arguments)
		if err != nil {
			content, _ := json.Marshal(map[string]any{"error": err.Error()})
			writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"isError": true, "content": []map[string]string{{"type": "text", "text": string(content)}}}})
			return
		}
		content, _ := json.Marshal(result)
		writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"content": []map[string]string{{"type": "text", "text": string(content)}}, "structuredContent": result}})
	default:
		s.writeMCPError(w, request.ID, -32601, "지원하지 않는 MCP 메서드입니다.")
	}
}

func (s *Server) callMCPTool(r *http.Request, name string, args map[string]any) (any, error) {
	definition, found := findMCPTool(name)
	if !found {
		return nil, errors.New("unknown tool: " + name)
	}
	if principal, ok := apiPrincipal(r); ok && !principal.Allows(definition.Meta["required_scope"].(string)) {
		return nil, errors.New("insufficient scope: " + definition.Meta["required_scope"].(string))
	}
	if err := s.authorizeMCPTool(r, name, args); err != nil {
		return nil, err
	}
	ctx, actor := r.Context(), actorID(r)
	switch name {
	case "platform.version.get":
		return s.build, nil
	case "platform.auth.config":
		config, err := s.auth.Config(ctx)
		return map[string]any{
			"oidc_enabled":             config.Enabled,
			"bootstrap_login_enabled":  s.auth.BootstrapEnabled(),
			"issuer_url":               config.IssuerURL,
			"client_id":                config.ClientID,
			"client_secret_configured": strings.TrimSpace(config.ClientSecret) != "",
		}, err
	case "spreadsheet.workbook.list":
		return s.repository.ListWorkbooks(ctx, stringArg(args, "workspace_id"), s.accessPrincipal(r))
	case "spreadsheet.workbook.get":
		return s.repository.GetWorkbook(ctx, stringArg(args, "workbook_id"))
	case "spreadsheet.workbook.search":
		var input workbook.SearchWorkbookInput
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		return s.repository.SearchWorkbook(ctx, stringArg(args, "workbook_id"), input)
	case "spreadsheet.workbook.replace":
		var input workbook.ReplaceWorkbookInput
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		input.ActorID = actor
		result, err := workbook.ReplaceWorkbookCells(ctx, s.repository, stringArg(args, "workbook_id"), input)
		if err != nil {
			return nil, err
		}
		for _, sheet := range result.Sheets {
			if sheet.Operation.Duplicate {
				continue
			}
			s.collab.PublishOperation(sheet.Operation.WorkbookID, sheet.SheetID, actor, input.ClientID, sheet.Cells, sheet.Operation)
		}
		return result, nil
	case "spreadsheet.template.list":
		return map[string]any{"items": workbook.TemplateCatalog()}, nil
	case "spreadsheet.workbook.create":
		var input workbook.CreateWorkbookInput
		decodeMCP(args, &input)
		input.OwnerID = actor
		templateID := strings.TrimSpace(stringArg(args, "template_id"))
		template, hasTemplate := workbook.Template{}, false
		if templateID != "" {
			if template, hasTemplate = workbook.TemplateByID(templateID); !hasTemplate {
				return nil, fmt.Errorf("%w: 알 수 없는 템플릿입니다", workbook.ErrInvalid)
			}
			if strings.TrimSpace(input.Title) == "" {
				input.Title = template.Name
			}
		}
		created, err := s.repository.CreateWorkbook(ctx, input)
		if err != nil || !hasTemplate {
			return created, err
		}
		if err := workbook.ApplyTemplate(ctx, s.repository, created, template, actor); err != nil {
			return nil, err
		}
		return s.repository.GetWorkbook(ctx, created.ID)
	case "spreadsheet.workbook.duplicate":
		var input workbook.DuplicateWorkbookInput
		decodeMCP(args, &input)
		input.OwnerID = actor
		return s.repository.DuplicateWorkbook(ctx, stringArg(args, "workbook_id"), input)
	case "spreadsheet.workbook.update":
		var input workbook.UpdateWorkbookInput
		decodeMCP(args, &input)
		item, err := s.repository.UpdateWorkbook(ctx, stringArg(args, "workbook_id"), input)
		if err == nil {
			s.collab.PublishVersion(item.ID, actor, "", "", item.Version)
		}
		return item, err
	case "spreadsheet.workbook.delete":
		return okResult(s.repository.DeleteWorkbook(ctx, stringArg(args, "workbook_id"), actor))
	case "spreadsheet.share.list":
		return s.sharingResult(ctx, r, stringArg(args, "workbook_id"))
	case "spreadsheet.share.grant":
		var input workbook.ShareInput
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		input.ActorID = actor
		workbookID := stringArg(args, "workbook_id")
		if _, err := s.repository.PutWorkbookShare(ctx, workbookID, input); err != nil {
			return nil, err
		}
		return s.sharingResult(ctx, r, workbookID)
	case "spreadsheet.share.revoke":
		workbookID := stringArg(args, "workbook_id")
		if err := s.repository.DeleteWorkbookShare(ctx, workbookID, stringArg(args, "share_id")); err != nil {
			return nil, err
		}
		return s.sharingResult(ctx, r, workbookID)
	case "spreadsheet.share.link":
		var input workbook.UpdateSharingInput
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		input.ActorID = actor
		workbookID := stringArg(args, "workbook_id")
		if err := s.enforceSharingPolicy(ctx, input.LinkAccess); err != nil {
			return nil, err
		}
		if _, err := s.repository.UpdateWorkbookSharing(ctx, workbookID, input); err != nil {
			return nil, err
		}
		return s.sharingResult(ctx, r, workbookID)
	case "spreadsheet.share.transfer_ownership":
		var input workbook.TransferOwnershipInput
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		input.ActorID = actor
		workbookID := stringArg(args, "workbook_id")
		if _, err := s.repository.TransferWorkbookOwnership(ctx, workbookID, input); err != nil {
			return nil, err
		}
		return s.sharingResult(ctx, r, workbookID)
	case "spreadsheet.access_request.list":
		items, err := s.repository.ListAccessRequests(ctx, stringArg(args, "workbook_id"), false)
		return map[string]any{"items": items}, err
	case "spreadsheet.access_request.decide":
		var input workbook.DecideAccessRequestInput
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		input.ActorID = actor
		return s.repository.DecideAccessRequest(ctx, stringArg(args, "request_id"), input)
	case "spreadsheet.department.list":
		items, err := s.repository.ListDepartments(ctx)
		return map[string]any{"items": items}, err
	case "spreadsheet.department.create":
		var input workbook.CreateDepartmentInput
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		input.ActorID = actor
		return s.repository.CreateDepartment(ctx, input)
	case "spreadsheet.department.update":
		var input workbook.UpdateDepartmentInput
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		input.ActorID = actor
		return s.repository.UpdateDepartment(ctx, stringArg(args, "department_id"), input)
	case "spreadsheet.department.delete":
		return okResult(s.repository.DeleteDepartment(ctx, stringArg(args, "department_id")))
	case "spreadsheet.department.add_members":
		var input workbook.DepartmentMembersInput
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		input.ActorID = actor
		return s.repository.AddDepartmentMembers(ctx, stringArg(args, "department_id"), input)
	case "spreadsheet.department.remove_member":
		return s.repository.RemoveDepartmentMember(ctx, stringArg(args, "department_id"), stringArg(args, "user_id"))
	case "spreadsheet.sheet.list":
		wb, err := s.repository.GetWorkbook(ctx, stringArg(args, "workbook_id"))
		return wb.Sheets, err
	case "spreadsheet.sheet.create":
		var input workbook.CreateSheetInput
		decodeMCP(args, &input)
		item, err := s.repository.CreateSheet(ctx, stringArg(args, "workbook_id"), input)
		if err == nil {
			s.publishCurrentVersion(ctx, item.WorkbookID, actor, "")
		}
		return item, err
	case "spreadsheet.sheet.duplicate":
		var input workbook.DuplicateSheetInput
		decodeMCP(args, &input)
		item, err := s.repository.DuplicateSheet(ctx, stringArg(args, "sheet_id"), input)
		if err == nil {
			s.publishCurrentVersion(ctx, item.WorkbookID, actor, "")
		}
		return item, err
	case "spreadsheet.sheet.update":
		var input workbook.UpdateSheetInput
		decodeMCP(args, &input)
		item, err := s.repository.UpdateSheet(ctx, stringArg(args, "sheet_id"), input)
		if err == nil {
			s.publishCurrentVersion(ctx, item.WorkbookID, actor, "")
		}
		return item, err
	case "spreadsheet.sheet.delete":
		sheetID := stringArg(args, "sheet_id")
		workbookID := s.workbookIDForSheet(ctx, sheetID)
		err := s.repository.DeleteSheet(ctx, sheetID)
		if err == nil {
			s.publishCurrentVersion(ctx, workbookID, actor, "")
		}
		return okResult(err)
	case "spreadsheet.workbook.trash":
		items, err := s.repository.ListDeletedWorkbooks(ctx, stringArg(args, "workspace_id"), s.accessPrincipal(r))
		return map[string]any{"items": items}, err
	case "spreadsheet.workbook.restore":
		workbookID := stringArg(args, "workbook_id")
		if err := s.assertTrashOwner(ctx, r, workbookID); err != nil {
			return nil, err
		}
		return s.repository.RestoreWorkbook(ctx, workbookID, actor)
	case "spreadsheet.workbook.purge":
		workbookID := stringArg(args, "workbook_id")
		if err := s.assertTrashOwner(ctx, r, workbookID); err != nil {
			return nil, err
		}
		return okResult(s.repository.PurgeWorkbook(ctx, workbookID))
	case "spreadsheet.workbook.favorite":
		workbookID := stringArg(args, "workbook_id")
		favorite, _ := args["favorite"].(bool)
		if err := s.repository.SetWorkbookFavorite(ctx, workbookID, actor, favorite); err != nil {
			return nil, err
		}
		return map[string]any{"workbook_id": workbookID, "favorite": favorite}, nil
	case "spreadsheet.sheet.stats":
		items, err := s.repository.SheetStats(ctx, stringArg(args, "workbook_id"))
		return map[string]any{"items": items}, err
	case "spreadsheet.sheet.copy":
		var input workbook.CopySheetInput
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		input.ActorID = actor
		target, err := s.repository.ResolveWorkbookAccess(ctx, input.TargetWorkbookID, s.accessPrincipal(r))
		if err != nil {
			return nil, err
		}
		if !target.CanWrite {
			return nil, fmt.Errorf("%w: 대상 워크북에 대한 편집 권한이 없습니다", workbook.ErrForbidden)
		}
		return s.repository.CopySheetToWorkbook(ctx, stringArg(args, "sheet_id"), input)
	case "spreadsheet.named_range.list":
		return s.repository.ListNamedRanges(ctx, stringArg(args, "workbook_id"))
	case "spreadsheet.named_range.get":
		return s.repository.GetNamedRange(ctx, stringArg(args, "named_range_id"))
	case "spreadsheet.named_range.create":
		var input workbook.CreateNamedRangeInput
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		item, err := s.repository.CreateNamedRange(ctx, stringArg(args, "workbook_id"), actor, input)
		if err == nil {
			s.collab.PublishVersion(item.WorkbookID, actor, "", "", item.WorkbookVersion)
		}
		return item, err
	case "spreadsheet.named_range.update":
		var input workbook.UpdateNamedRangeInput
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		item, err := s.repository.UpdateNamedRange(ctx, stringArg(args, "named_range_id"), actor, input)
		if err == nil {
			s.collab.PublishVersion(item.WorkbookID, actor, "", "", item.WorkbookVersion)
		}
		return item, err
	case "spreadsheet.named_range.delete":
		id := stringArg(args, "named_range_id")
		item, err := s.repository.GetNamedRange(ctx, id)
		if err != nil {
			return nil, err
		}
		var expected *int64
		if value, found := args["expected_revision"]; found && value != nil {
			revision, revisionErr := numberArg(args, "expected_revision")
			if revisionErr != nil || revision < 1 {
				return nil, fmt.Errorf("expected_revision must be a positive integer")
			}
			expected = &revision
		}
		if err := s.repository.DeleteNamedRange(ctx, id, actor, expected); err != nil {
			return nil, err
		}
		s.publishCurrentVersion(ctx, item.WorkbookID, actor, "")
		return map[string]any{"ok": true}, nil
	case "spreadsheet.range.read":
		selected, err := cellrange.Parse(stringArg(args, "range"))
		if err != nil {
			return nil, err
		}
		return s.repository.ReadRange(ctx, stringArg(args, "sheet_id"), selected)
	case "spreadsheet.range.write", "spreadsheet.range.paste", "spreadsheet.range.fill":
		var input struct {
			SheetID        string               `json:"sheet_id"`
			BaseVersion    int64                `json:"base_version"`
			IdempotencyKey string               `json:"idempotency_key"`
			ClientID       string               `json:"client_id"`
			Cells          []workbook.CellInput `json:"cells"`
		}
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		limit, operationType := workbook.MaxBatchCells, ""
		if name == "spreadsheet.range.paste" || name == "spreadsheet.range.fill" {
			limit = workbook.MaxPasteCells
			operationType = map[string]string{"spreadsheet.range.paste": "cells.paste", "spreadsheet.range.fill": "cells.fill"}[name]
		}
		if len(input.Cells) == 0 || len(input.Cells) > limit {
			return nil, fmt.Errorf("cells must contain 1 to %d entries", limit)
		}
		result, err := s.repository.ApplyCells(ctx, workbook.CellMutation{SheetID: input.SheetID, ActorID: actor, BaseVersion: input.BaseVersion, IdempotencyKey: input.IdempotencyKey, ClientID: input.ClientID, Cells: input.Cells, OperationType: operationType})
		if err == nil && !result.Duplicate {
			s.collab.PublishOperation(result.WorkbookID, result.SheetID, actor, input.ClientID, input.Cells, result)
			s.triggerCellAutomations(r, result, input.Cells)
		}
		return result, err
	case "spreadsheet.range.format":
		var input rangeFormatRequest
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		input.Range = stringArg(args, "range")
		result, cells, err := s.applyRangeFormat(ctx, stringArg(args, "sheet_id"), actor, input)
		if err == nil && !result.Duplicate && result.AppliedCells > 0 {
			s.collab.PublishOperation(result.WorkbookID, result.SheetID, actor, input.ClientID, cells, result)
		}
		return result, err
	case "spreadsheet.range.merge", "spreadsheet.range.unmerge":
		var input rangeMergeRequest
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		input.Range = stringArg(args, "range")
		result, cells, err := s.applyRangeMerge(ctx, stringArg(args, "sheet_id"), actor, input, name == "spreadsheet.range.merge")
		if err == nil && !result.Duplicate && result.AppliedCells > 0 {
			s.collab.PublishOperation(result.WorkbookID, result.SheetID, actor, input.ClientID, cells, result)
		}
		return result, err
	case "spreadsheet.range.sort":
		var input rangeSortRequest
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		input.Range = stringArg(args, "range")
		result, cells, err := s.applyRangeSort(ctx, stringArg(args, "sheet_id"), actor, input)
		if err == nil && !result.Duplicate && result.AppliedCells > 0 {
			s.collab.PublishOperation(result.WorkbookID, result.SheetID, actor, input.ClientID, cells, result)
			s.triggerCellAutomations(r, result, cells)
		}
		return result, err
	case "spreadsheet.structure.apply":
		var input workbook.StructuralMutation
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		input.ActorID = actor
		result, err := s.repository.ApplyStructure(ctx, input)
		if err == nil && !result.Duplicate {
			s.collab.PublishStructure(result, actor, input.ClientID)
		}
		return result, err
	case "spreadsheet.layout.apply":
		var input workbook.SheetLayoutMutation
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		input.ActorID = actor
		result, err := s.repository.ApplySheetLayout(ctx, input)
		if err == nil && !result.Duplicate {
			s.collab.PublishVersion(result.WorkbookID, actor, input.ClientID, result.OperationID, result.ServerVersion)
		}
		return result, err
	case "spreadsheet.filter_view.list":
		return s.repository.ListFilterViews(ctx, stringArg(args, "sheet_id"), actor)
	case "spreadsheet.filter_view.get":
		return s.repository.GetFilterView(ctx, stringArg(args, "filter_view_id"), actor)
	case "spreadsheet.filter_view.create":
		var input workbook.CreateFilterViewInput
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		return s.repository.CreateFilterView(ctx, stringArg(args, "sheet_id"), actor, input)
	case "spreadsheet.filter_view.update":
		var input workbook.UpdateFilterViewInput
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		return s.repository.UpdateFilterView(ctx, stringArg(args, "filter_view_id"), actor, input)
	case "spreadsheet.filter_view.delete":
		return okResult(s.repository.DeleteFilterView(ctx, stringArg(args, "filter_view_id"), actor))
	case "spreadsheet.filter_view.evaluate":
		return s.applyFilterView(ctx, stringArg(args, "filter_view_id"), actor)
	case "spreadsheet.data_validation.list":
		return s.repository.ListDataValidations(ctx, stringArg(args, "sheet_id"))
	case "spreadsheet.data_validation.get":
		return s.repository.GetDataValidation(ctx, stringArg(args, "validation_id"))
	case "spreadsheet.data_validation.create":
		var input workbook.CreateDataValidationInput
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		item, err := s.repository.CreateDataValidation(ctx, stringArg(args, "sheet_id"), actor, input)
		if err == nil {
			s.collab.PublishVersion(item.WorkbookID, actor, "", "", item.WorkbookVersion)
		}
		return item, err
	case "spreadsheet.data_validation.update":
		var input workbook.UpdateDataValidationInput
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		item, err := s.repository.UpdateDataValidation(ctx, stringArg(args, "validation_id"), actor, input)
		if err == nil {
			s.collab.PublishVersion(item.WorkbookID, actor, "", "", item.WorkbookVersion)
		}
		return item, err
	case "spreadsheet.data_validation.delete":
		id := stringArg(args, "validation_id")
		item, err := s.repository.GetDataValidation(ctx, id)
		if err != nil {
			return nil, err
		}
		var expected *int64
		if value, found := args["expected_revision"]; found && value != nil {
			revision, err := numberArg(args, "expected_revision")
			if err != nil || revision < 1 {
				return nil, fmt.Errorf("expected_revision must be a positive integer")
			}
			expected = &revision
		}
		if err := s.repository.DeleteDataValidation(ctx, id, actor, expected); err != nil {
			return nil, err
		}
		s.publishCurrentVersion(ctx, item.WorkbookID, actor, "")
		return map[string]any{"ok": true}, nil
	case "spreadsheet.data_validation.evaluate":
		return s.applyDataValidation(ctx, stringArg(args, "validation_id"))
	case "spreadsheet.conditional_format.list":
		return s.repository.ListConditionalFormats(ctx, stringArg(args, "sheet_id"))
	case "spreadsheet.conditional_format.get":
		return s.repository.GetConditionalFormat(ctx, stringArg(args, "conditional_format_id"))
	case "spreadsheet.conditional_format.evaluate":
		selected, err := cellrange.Parse(stringArg(args, "range"))
		if err != nil {
			return nil, err
		}
		return s.repository.EvaluateConditionalFormats(ctx, stringArg(args, "sheet_id"), selected)
	case "spreadsheet.conditional_format.create":
		var input workbook.CreateConditionalFormatInput
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		item, err := s.repository.CreateConditionalFormat(ctx, stringArg(args, "sheet_id"), actor, input)
		if err == nil {
			s.collab.PublishVersion(item.WorkbookID, actor, "", "", item.WorkbookVersion)
		}
		return item, err
	case "spreadsheet.conditional_format.update":
		var input workbook.UpdateConditionalFormatInput
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		item, err := s.repository.UpdateConditionalFormat(ctx, stringArg(args, "conditional_format_id"), actor, input)
		if err == nil {
			s.collab.PublishVersion(item.WorkbookID, actor, "", "", item.WorkbookVersion)
		}
		return item, err
	case "spreadsheet.conditional_format.delete":
		id := stringArg(args, "conditional_format_id")
		item, err := s.repository.GetConditionalFormat(ctx, id)
		if err != nil {
			return nil, err
		}
		var expected *int64
		if value, found := args["expected_revision"]; found && value != nil {
			revision, revisionErr := numberArg(args, "expected_revision")
			if revisionErr != nil || revision < 1 {
				return nil, fmt.Errorf("expected_revision must be a positive integer")
			}
			expected = &revision
		}
		if err := s.repository.DeleteConditionalFormat(ctx, id, actor, expected); err != nil {
			return nil, err
		}
		s.publishCurrentVersion(ctx, item.WorkbookID, actor, "")
		return map[string]any{"ok": true}, nil
	case "spreadsheet.comment.list":
		var input struct {
			WorkbookID      string `json:"workbook_id"`
			SheetID         string `json:"sheet_id"`
			IncludeResolved bool   `json:"include_resolved"`
		}
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		return s.repository.ListCommentThreads(ctx, input.WorkbookID, input.SheetID, input.IncludeResolved)
	case "spreadsheet.comment.get":
		return s.repository.GetCommentThread(ctx, stringArg(args, "comment_id"))
	case "spreadsheet.comment.create":
		var input workbook.CreateCommentThreadInput
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		thread, err := s.repository.CreateCommentThread(ctx, stringArg(args, "workbook_id"), actor, input)
		if err == nil {
			s.publishComment(thread, actor, "created")
		}
		return thread, err
	case "spreadsheet.comment.reply":
		var input workbook.CreateCommentReplyInput
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		thread, err := s.repository.AddCommentReply(ctx, stringArg(args, "comment_id"), actor, input)
		if err == nil {
			s.publishComment(thread, actor, "replied")
		}
		return thread, err
	case "spreadsheet.comment.resolve":
		var input workbook.UpdateCommentThreadInput
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		thread, err := s.repository.UpdateCommentThread(ctx, stringArg(args, "comment_id"), actor, input)
		if err == nil {
			s.publishComment(thread, actor, "status_changed")
		}
		return thread, err
	case "spreadsheet.comment.delete":
		thread, err := s.repository.GetCommentThread(ctx, stringArg(args, "comment_id"))
		if err != nil {
			return nil, err
		}
		if err := s.repository.DeleteCommentThread(ctx, thread.ID, actor); err != nil {
			return nil, err
		}
		s.collab.PublishComment(thread.WorkbookID, actor, map[string]any{"thread_id": thread.ID, "action": "deleted"})
		return map[string]any{"ok": true}, nil
	case "spreadsheet.comment.message.update":
		var input workbook.UpdateCommentMessageInput
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		thread, err := s.repository.UpdateCommentMessage(ctx, stringArg(args, "message_id"), actor, input)
		if err == nil {
			s.publishComment(thread, actor, "message_updated")
		}
		return thread, err
	case "spreadsheet.comment.message.delete":
		revision, err := numberArg(args, "expected_revision")
		if err != nil {
			return nil, err
		}
		thread, err := s.repository.DeleteCommentMessage(ctx, stringArg(args, "message_id"), actor, revision)
		if err == nil {
			s.publishComment(thread, actor, "message_deleted")
		}
		return thread, err
	case "spreadsheet.notification.list":
		var input struct {
			UnreadOnly bool `json:"unread_only"`
			Limit      int  `json:"limit"`
		}
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		return s.repository.ListMentionNotifications(ctx, actorAliases(r), input.UnreadOnly, input.Limit)
	case "spreadsheet.notification.mark_read":
		return s.repository.MarkMentionNotificationRead(ctx, stringArg(args, "notification_id"), actorAliases(r))
	case "spreadsheet.chart.list":
		return s.repository.ListCharts(ctx, stringArg(args, "workbook_id"), stringArg(args, "sheet_id"))
	case "spreadsheet.chart.get":
		return s.repository.GetChart(ctx, stringArg(args, "chart_id"))
	case "spreadsheet.chart.data":
		return s.repository.GetChartData(ctx, stringArg(args, "chart_id"))
	case "spreadsheet.chart.create":
		var input workbook.CreateChartInput
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		item, err := s.repository.CreateChart(ctx, stringArg(args, "workbook_id"), actor, input)
		if err == nil {
			s.collab.PublishVersion(item.WorkbookID, actor, "", "", item.WorkbookVersion)
		}
		return item, err
	case "spreadsheet.chart.update":
		var input workbook.UpdateChartInput
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		item, err := s.repository.UpdateChart(ctx, stringArg(args, "chart_id"), actor, input)
		if err == nil {
			s.collab.PublishVersion(item.WorkbookID, actor, "", "", item.WorkbookVersion)
		}
		return item, err
	case "spreadsheet.chart.delete":
		item, err := s.repository.GetChart(ctx, stringArg(args, "chart_id"))
		if err != nil {
			return nil, err
		}
		var expected *int64
		if value, found := args["expected_revision"]; found && value != nil {
			revision, revisionErr := numberArg(args, "expected_revision")
			if revisionErr != nil || revision < 1 {
				return nil, fmt.Errorf("expected_revision must be a positive integer")
			}
			expected = &revision
		}
		if err := s.repository.DeleteChart(ctx, item.ID, actor, expected); err != nil {
			return nil, err
		}
		s.publishCurrentVersion(ctx, item.WorkbookID, actor, "")
		return map[string]any{"ok": true}, nil
	case "spreadsheet.pivot.list":
		return s.repository.ListPivots(ctx, stringArg(args, "workbook_id"), stringArg(args, "sheet_id"))
	case "spreadsheet.pivot.get":
		return s.repository.GetPivot(ctx, stringArg(args, "pivot_id"))
	case "spreadsheet.pivot.data":
		return s.repository.GetPivotData(ctx, stringArg(args, "pivot_id"))
	case "spreadsheet.pivot.drilldown":
		var input workbook.PivotDrilldownInput
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		return s.repository.PivotDrilldown(ctx, stringArg(args, "pivot_id"), input)
	case "spreadsheet.pivot.create":
		var input workbook.CreatePivotInput
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		item, err := s.repository.CreatePivot(ctx, stringArg(args, "workbook_id"), actor, input)
		if err == nil {
			s.collab.PublishVersion(item.WorkbookID, actor, "", "", item.WorkbookVersion)
		}
		return item, err
	case "spreadsheet.pivot.update":
		var input workbook.UpdatePivotInput
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		item, err := s.repository.UpdatePivot(ctx, stringArg(args, "pivot_id"), actor, input)
		if err == nil {
			s.collab.PublishVersion(item.WorkbookID, actor, "", "", item.WorkbookVersion)
		}
		return item, err
	case "spreadsheet.pivot.refresh":
		return s.repository.RefreshPivot(ctx, stringArg(args, "pivot_id"), actor)
	case "spreadsheet.pivot.delete":
		item, err := s.repository.GetPivot(ctx, stringArg(args, "pivot_id"))
		if err != nil {
			return nil, err
		}
		var expected *int64
		if value, found := args["expected_revision"]; found && value != nil {
			revision, revisionErr := numberArg(args, "expected_revision")
			if revisionErr != nil || revision < 1 {
				return nil, fmt.Errorf("expected_revision must be a positive integer")
			}
			expected = &revision
		}
		if err := s.repository.DeletePivot(ctx, item.ID, actor, expected); err != nil {
			return nil, err
		}
		s.publishCurrentVersion(ctx, item.WorkbookID, actor, "")
		return map[string]any{"ok": true}, nil
	case "spreadsheet.formula.set":
		var input struct {
			SheetID        string `json:"sheet_id"`
			Row            int    `json:"row"`
			Column         int    `json:"column"`
			Formula        string `json:"formula"`
			BaseVersion    int64  `json:"base_version"`
			IdempotencyKey string `json:"idempotency_key"`
		}
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		cells := []workbook.CellInput{{Row: input.Row, Column: input.Column, Formula: input.Formula}}
		result, err := s.repository.ApplyCells(ctx, workbook.CellMutation{SheetID: input.SheetID, ActorID: actor, BaseVersion: input.BaseVersion, IdempotencyKey: input.IdempotencyKey, Cells: cells})
		if err == nil && !result.Duplicate {
			s.collab.PublishOperation(result.WorkbookID, result.SheetID, actor, "", cells, result)
			s.triggerCellAutomations(r, result, cells)
		}
		return result, err
	case "spreadsheet.formula.evaluate":
		cells, _ := args["cells"].(map[string]any)
		return s.formula.Evaluate(stringArg(args, "formula"), cells), nil
	case "spreadsheet.formula.explain":
		return s.getFormulaInfo(ctx, stringArg(args, "sheet_id"), stringArg(args, "address"))
	case "spreadsheet.operation.undo":
		clientID := stringArg(args, "client_id")
		result, err := s.repository.UndoOperation(ctx, workbook.UndoOperationInput{OperationID: stringArg(args, "operation_id"), ActorID: actor, ClientID: clientID, IdempotencyKey: stringArg(args, "idempotency_key")})
		if err == nil && !result.Duplicate {
			s.collab.PublishOperation(result.WorkbookID, result.SheetID, actor, clientID, nil, result)
		}
		return result, err
	case "spreadsheet.conflict.list":
		var input struct {
			WorkbookID      string `json:"workbook_id"`
			IncludeResolved bool   `json:"include_resolved"`
		}
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		return s.repository.ListCellConflicts(ctx, input.WorkbookID, input.IncludeResolved)
	case "spreadsheet.conflict.get":
		return s.repository.GetCellConflict(ctx, stringArg(args, "conflict_id"))
	case "spreadsheet.conflict.resolve":
		var input workbook.ResolveCellConflictInput
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		input.ActorID = actor
		result, err := s.repository.ResolveCellConflict(ctx, stringArg(args, "conflict_id"), input)
		if err == nil && !result.Operation.Duplicate {
			cell := result.Conflict.CurrentCell
			s.collab.PublishOperation(result.Conflict.WorkbookID, result.Conflict.SheetID, actor, input.ClientID, []workbook.CellInput{{Row: result.Conflict.Row, Column: result.Conflict.Column, Value: cell.Value, Formula: cell.Formula, Style: cell.Style}}, result.Operation)
		}
		return result, err
	case "spreadsheet.ai.config.get":
		if s.ai == nil {
			return nil, ai.ErrDisabled
		}
		return s.ai.PublicConfig(ctx)
	case "spreadsheet.ai.action.plan":
		if s.ai == nil {
			return nil, ai.ErrDisabled
		}
		if err := requireMCPScopes(r, "range.read"); err != nil {
			return nil, err
		}
		var input ai.PlanInput
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		input.ActorID = actor
		return s.ai.Plan(ctx, input)
	case "spreadsheet.ai.action.list":
		if s.ai == nil {
			return nil, ai.ErrDisabled
		}
		limit, _ := numberArg(args, "limit")
		return s.ai.List(ctx, stringArg(args, "workbook_id"), actor, int(limit))
	case "spreadsheet.ai.action.get":
		if s.ai == nil {
			return nil, ai.ErrDisabled
		}
		return s.ai.Get(ctx, stringArg(args, "action_id"), actor)
	case "spreadsheet.ai.action.approve", "spreadsheet.ai.action.undo":
		if s.ai == nil {
			return nil, ai.ErrDisabled
		}
		var input ai.ApprovalInput
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		input.ActorID = actor
		required := []string{"range.write"}
		if name == "spreadsheet.ai.action.approve" {
			action, err := s.ai.Get(ctx, stringArg(args, "action_id"), actor)
			if err != nil {
				return nil, err
			}
			required = ai.RequiredApprovalScopes(action)
		}
		if err := requireMCPScopes(r, required...); err != nil {
			return nil, err
		}
		var result ai.ExecutionResult
		var err error
		if name == "spreadsheet.ai.action.approve" {
			result, err = s.ai.Approve(ctx, stringArg(args, "action_id"), input)
		} else {
			result, err = s.ai.Undo(ctx, stringArg(args, "action_id"), input)
		}
		if err == nil && !result.Operation.Duplicate {
			s.collab.PublishOperation(result.Action.WorkbookID, result.Action.SheetID, actor, input.ClientID, result.Changes, result.Operation)
			s.triggerCellAutomations(r, result.Operation, result.Changes)
		}
		return result, err
	case "spreadsheet.automation.list":
		if s.automations == nil {
			return nil, automation.ErrDisabled
		}
		return s.automations.List(ctx, stringArg(args, "workbook_id"))
	case "spreadsheet.automation.get":
		if s.automations == nil {
			return nil, automation.ErrDisabled
		}
		return s.automations.Get(ctx, stringArg(args, "automation_id"))
	case "spreadsheet.automation.create":
		if s.automations == nil {
			return nil, automation.ErrDisabled
		}
		var input automation.CreateInput
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		return s.automations.Create(ctx, stringArg(args, "workbook_id"), actor, input)
	case "spreadsheet.automation.update":
		if s.automations == nil {
			return nil, automation.ErrDisabled
		}
		var input automation.UpdateInput
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		return s.automations.Update(ctx, stringArg(args, "automation_id"), actor, input)
	case "spreadsheet.automation.delete":
		if s.automations == nil {
			return nil, automation.ErrDisabled
		}
		revision, err := numberArg(args, "expected_revision")
		if err != nil || revision < 1 {
			return nil, fmt.Errorf("expected_revision must be a positive integer")
		}
		return okResult(s.automations.Delete(ctx, stringArg(args, "automation_id"), actor, revision))
	case "spreadsheet.automation.test":
		if s.automations == nil {
			return nil, automation.ErrDisabled
		}
		if err := requireMCPScopes(r, "range.read"); err != nil {
			return nil, err
		}
		return s.automations.Preview(ctx, stringArg(args, "automation_id"))
	case "spreadsheet.automation.run":
		if s.automations == nil {
			return nil, automation.ErrDisabled
		}
		item, err := s.automations.Get(ctx, stringArg(args, "automation_id"))
		if err != nil {
			return nil, err
		}
		if err := requireMCPScopes(r, automation.RequiredActionScope(item.Action.Type)); err != nil {
			return nil, err
		}
		var input automation.RunInput
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		input.ActorID = actor
		result, err := s.automations.Run(ctx, item.ID, input)
		if err == nil {
			s.publishAutomationResult(actor, input.ClientID, result)
		}
		return result, err
	case "spreadsheet.automation.webhook.invoke":
		if s.automations == nil {
			return nil, automation.ErrDisabled
		}
		principal, ok := apiPrincipal(r)
		if !ok {
			return nil, errors.New("API key authentication is required for webhook invocation")
		}
		payload := []byte{}
		if value, exists := args["payload"]; exists {
			var err error
			payload, err = json.Marshal(value)
			if err != nil {
				return nil, fmt.Errorf("invalid webhook payload: %w", err)
			}
		}
		if len(payload) > maxAutomationWebhookPayload {
			return nil, errors.New("webhook payload exceeds 1MiB")
		}
		digest := sha256.Sum256(payload)
		clientID := stringArg(args, "client_id")
		if clientID == "" {
			clientID = "webhook:" + principal.KeyID
		}
		input := automation.RunInput{ActorID: principal.UserID, ClientID: clientID, IdempotencyKey: stringArg(args, "idempotency_key"), TriggerType: automation.TriggerWebhook, TriggerKeyID: principal.KeyID, PayloadDigest: hex.EncodeToString(digest[:]), PayloadBytes: len(payload)}
		result, err := s.automations.Run(ctx, stringArg(args, "automation_id"), input)
		if err == nil {
			s.publishAutomationResult(principal.UserID, input.ClientID, result)
		}
		return result, err
	case "spreadsheet.automation.run.list":
		if s.automations == nil {
			return nil, automation.ErrDisabled
		}
		limit, _ := numberArg(args, "limit")
		return s.automations.ListRuns(ctx, stringArg(args, "automation_id"), int(limit))
	case "spreadsheet.automation.run.undo":
		if s.automations == nil {
			return nil, automation.ErrDisabled
		}
		if err := requireMCPScopes(r, "range.write"); err != nil {
			return nil, err
		}
		var input automation.RunInput
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		input.ActorID = actor
		result, err := s.automations.Undo(ctx, stringArg(args, "run_id"), input)
		if err == nil {
			s.publishAutomationResult(actor, input.ClientID, result)
		}
		return result, err
	case "spreadsheet.import.preview":
		data, err := base64.StdEncoding.DecodeString(stringArg(args, "data_base64"))
		if err != nil {
			return nil, err
		}
		return s.files.Preview(ctx, stringArg(args, "file_name"), data, s.maxExpandedBytes(r))
	case "spreadsheet.import.execute":
		data, err := base64.StdEncoding.DecodeString(stringArg(args, "data_base64"))
		if err != nil {
			return nil, err
		}
		idempotencyKey := stringArg(args, "idempotency_key")
		if idempotencyKey == "" {
			return nil, errors.New("idempotency_key is required")
		}
		return s.files.Import(ctx, importexport.ImportRequest{FileName: stringArg(args, "file_name"), Data: data, WorkspaceID: stringArg(args, "workspace_id"), ActorID: actor, IdempotencyKey: idempotencyKey, MaxExpandedBytes: s.maxExpandedBytes(r)})
	case "spreadsheet.export.execute":
		exported, err := s.files.Export(ctx, importexport.ExportRequest{WorkbookID: stringArg(args, "workbook_id"), SheetID: stringArg(args, "sheet_id"), Format: stringArg(args, "format")})
		if err != nil {
			return nil, err
		}
		return encodeExportForMCP(exported), nil
	case "spreadsheet.version.create":
		return s.repository.CreateVersion(ctx, stringArg(args, "workbook_id"), stringArg(args, "name"), actor)
	case "spreadsheet.version.list":
		return s.repository.ListVersions(ctx, stringArg(args, "workbook_id"))
	case "spreadsheet.version.restore":
		result, err := s.repository.RestoreVersion(ctx, stringArg(args, "version_id"), actor)
		if err == nil {
			s.collab.PublishVersion(result.WorkbookID, actor, "", result.OperationID, result.ServerVersion)
		}
		return result, err
	case "profile.preferences.get":
		return s.settings.GetPreferences(ctx, actor)
	case "profile.preferences.update":
		values, _ := args["values"].(map[string]any)
		return s.settings.PutPreferences(ctx, actor, values)
	case "profile.api_key.list":
		return s.keys.List(ctx, actor, false)
	case "profile.api_key.create":
		var input apikey.CreateInput
		decodeMCP(args, &input)
		return s.keys.Create(ctx, actor, input)
	case "profile.api_key.update":
		var input apikey.UpdateInput
		decodeMCP(args, &input)
		return s.keys.Update(ctx, stringArg(args, "key_id"), actor, input, false)
	case "profile.api_key.revoke":
		return okResult(s.keys.Revoke(ctx, stringArg(args, "key_id"), actor, false))
	case "profile.api_key.rotate":
		return s.keys.Rotate(ctx, stringArg(args, "key_id"), actor, false)
	case "admin.settings.list":
		if err := s.requireMCPAdmin(r); err != nil {
			return nil, err
		}
		return s.settings.List(ctx, false)
	case "admin.settings.upsert":
		if err := s.requireMCPAdmin(r); err != nil {
			return nil, err
		}
		var input settings.Setting
		if err := decodeMCP(args, &input); err != nil {
			return nil, err
		}
		return s.settings.Put(ctx, input, actor)
	case "admin.settings.delete":
		if err := s.requireMCPAdmin(r); err != nil {
			return nil, err
		}
		return okResult(s.settings.Delete(ctx, stringArg(args, "key"), actor))
	case "admin.settings.validate":
		if err := s.requireMCPAdmin(r); err != nil {
			return nil, err
		}
		return s.settings.Validate(ctx)
	case "admin.settings.test":
		if err := s.requireMCPAdmin(r); err != nil {
			return nil, err
		}
		return s.settings.Test(ctx)
	case "admin.settings.version.list":
		if err := s.requireMCPAdmin(r); err != nil {
			return nil, err
		}
		return s.settings.Versions(ctx)
	case "admin.settings.version.restore":
		if err := s.requireMCPAdmin(r); err != nil {
			return nil, err
		}
		revision, err := numberArg(args, "revision")
		if err != nil {
			return nil, err
		}
		return okResult(s.settings.Restore(ctx, revision, actor))
	case "admin.logs.list":
		if err := s.requireMCPAdmin(r); err != nil {
			return nil, err
		}
		limit, _ := numberArg(args, "limit")
		return s.logs.List(ctx, stringArg(args, "level"), stringArg(args, "query"), int(limit))
	case "admin.logs.purge":
		if err := s.requireMCPAdmin(r); err != nil {
			return nil, err
		}
		before, err := time.Parse(time.RFC3339, stringArg(args, "before"))
		if err != nil {
			return nil, err
		}
		count, err := s.logs.PurgeBefore(ctx, before)
		return map[string]any{"deleted": count}, err
	}
	return nil, errors.New("tool is not implemented")
}

func (s *Server) requireMCPAdmin(r *http.Request) error {
	if principal, ok := apiPrincipal(r); ok {
		if principal.Allows("admin.*") {
			return nil
		}
		return errors.New("admin scope is required")
	}
	if user, ok := sessionUser(r); ok && s.auth.IsAdmin(r.Context(), user) {
		return nil
	}
	if actorID(r) == "local-user" {
		return nil
	}
	return errors.New("administrator permission is required")
}

func requireMCPScopes(r *http.Request, scopes ...string) error {
	principal, ok := apiPrincipal(r)
	if !ok {
		return nil
	}
	for _, scope := range scopes {
		if !principal.Allows(scope) {
			return errors.New("insufficient scope: " + scope)
		}
	}
	return nil
}

func (s *Server) writeMCPError(w http.ResponseWriter, id json.RawMessage, code int, message string) {
	writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message}})
}

func tool(name, description, scope string, schema map[string]any) mcpTool {
	if schema == nil {
		schema = map[string]any{"type": "object", "properties": map[string]any{}}
	}
	return mcpTool{Name: name, Description: description, InputSchema: schema, Meta: map[string]any{"required_scope": scope}}
}
func props(name, kind string) map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{name: map[string]any{"type": kind}}}
}
func requiredProps(name, kind string) map[string]any {
	schema := props(name, kind)
	schema["required"] = []string{name}
	return schema
}
func requiredProps2(a, ak, b, bk string) map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{a: map[string]any{"type": ak}, b: map[string]any{"type": bk}}, "required": []string{a, b}}
}
func requiredProps3(a, ak, b, bk, c, ck string) map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{a: map[string]any{"type": ak}, b: map[string]any{"type": bk}, c: map[string]any{"type": ck}}, "required": []string{a, b, c}}
}
func requiredProps4(a, ak, b, bk, c, ck, d, dk string) map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{a: map[string]any{"type": ak}, b: map[string]any{"type": bk}, c: map[string]any{"type": ck}, d: map[string]any{"type": dk}}, "required": []string{a, b, c, d}}
}

func workbookCreateSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"title":        map[string]any{"type": "string"},
		"workspace_id": map[string]any{"type": "string"},
		"template_id":  map[string]any{"type": "string"},
	}}
}

func shareGrantSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"workbook_id":     map[string]any{"type": "string", "minLength": 1},
		"principal_type":  map[string]any{"type": "string", "enum": []string{workbook.PrincipalUser, workbook.PrincipalDepartment, workbook.PrincipalRole}},
		"principal_id":    map[string]any{"type": "string", "minLength": 1, "maxLength": workbook.MaxPrincipalIDRunes},
		"principal_label": map[string]any{"type": "string"},
		"role":            map[string]any{"type": "string", "enum": []string{string(workbook.RoleViewer), string(workbook.RoleCommenter), string(workbook.RoleEditor)}},
	}, "required": []string{"workbook_id", "principal_type", "principal_id", "role"}}
}

func shareLinkSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"workbook_id":     map[string]any{"type": "string", "minLength": 1},
		"link_access":     map[string]any{"type": "string", "enum": []string{workbook.LinkAccessRestricted, workbook.LinkAccessOrganization, workbook.LinkAccessAnyone}},
		"link_role":       map[string]any{"type": "string", "enum": []string{string(workbook.RoleViewer), string(workbook.RoleCommenter), string(workbook.RoleEditor)}},
		"sharing_locked":  map[string]any{"type": "boolean"},
		"viewer_can_copy": map[string]any{"type": "boolean"},
	}, "required": []string{"workbook_id"}}
}

func accessRequestDecisionSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"workbook_id": map[string]any{"type": "string", "minLength": 1},
		"request_id":  map[string]any{"type": "string", "minLength": 1},
		"approve":     map[string]any{"type": "boolean"},
		"role":        map[string]any{"type": "string", "enum": []string{string(workbook.RoleViewer), string(workbook.RoleCommenter), string(workbook.RoleEditor)}},
	}, "required": []string{"workbook_id", "request_id", "approve"}}
}

func departmentSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"name":        map[string]any{"type": "string", "minLength": 1, "maxLength": workbook.MaxDepartmentNameRunes},
		"parent_id":   map[string]any{"type": "string"},
		"description": map[string]any{"type": "string", "maxLength": workbook.MaxDepartmentDescriptionRunes},
	}, "required": []string{"name"}}
}

func workbookSearchSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"workbook_id":   map[string]any{"type": "string", "minLength": 1},
			"query":         map[string]any{"type": "string", "minLength": 1, "maxLength": workbook.MaxSearchQueryRunes},
			"sheet_id":      map[string]any{"type": "string"},
			"match_case":    map[string]any{"type": "boolean"},
			"whole_cell":    map[string]any{"type": "boolean"},
			"use_regex":     map[string]any{"type": "boolean"},
			"skip_formulas": map[string]any{"type": "boolean"},
			"limit":         map[string]any{"type": "integer", "minimum": 1, "maximum": workbook.MaxSearchLimit},
			"offset":        map[string]any{"type": "integer", "minimum": 0, "maximum": workbook.MaxSearchOffset},
		},
		"required": []string{"workbook_id", "query"},
	}
}

func workbookReplaceSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"workbook_id":     map[string]any{"type": "string", "minLength": 1},
			"query":           map[string]any{"type": "string", "minLength": 1, "maxLength": workbook.MaxSearchQueryRunes},
			"replacement":     map[string]any{"type": "string", "maxLength": workbook.MaxReplacementRunes},
			"sheet_id":        map[string]any{"type": "string"},
			"range":           map[string]any{"type": "string"},
			"match_case":      map[string]any{"type": "boolean"},
			"whole_cell":      map[string]any{"type": "boolean"},
			"use_regex":       map[string]any{"type": "boolean"},
			"skip_formulas":   map[string]any{"type": "boolean"},
			"preview":         map[string]any{"type": "boolean"},
			"client_id":       map[string]any{"type": "string"},
			"idempotency_key": map[string]any{"type": "string"},
		},
		"required": []string{"workbook_id", "query"},
	}
}

func commentListSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"workbook_id": map[string]any{"type": "string", "minLength": 1}, "sheet_id": map[string]any{"type": "string"}, "include_resolved": map[string]any{"type": "boolean"},
	}, "required": []string{"workbook_id"}}
}

func commentCreateSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"workbook_id": map[string]any{"type": "string", "minLength": 1}, "sheet_id": map[string]any{"type": "string", "minLength": 1}, "range": map[string]any{"type": "string"},
		"content": map[string]any{"type": "string", "minLength": 1, "maxLength": workbook.MaxCommentContentRunes}, "idempotency_key": map[string]any{"type": "string", "minLength": 1},
	}, "required": []string{"workbook_id", "sheet_id", "range", "content", "idempotency_key"}}
}

func commentReplySchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"comment_id": map[string]any{"type": "string", "minLength": 1}, "content": map[string]any{"type": "string", "minLength": 1, "maxLength": workbook.MaxCommentContentRunes}, "idempotency_key": map[string]any{"type": "string", "minLength": 1},
	}, "required": []string{"comment_id", "content", "idempotency_key"}}
}

func commentResolveSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"comment_id": map[string]any{"type": "string", "minLength": 1}, "resolved": map[string]any{"type": "boolean"}, "expected_revision": map[string]any{"type": "integer", "minimum": 1},
	}, "required": []string{"comment_id", "resolved", "expected_revision"}}
}

func commentMessageUpdateSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"message_id": map[string]any{"type": "string", "minLength": 1}, "content": map[string]any{"type": "string", "minLength": 1, "maxLength": workbook.MaxCommentContentRunes}, "expected_revision": map[string]any{"type": "integer", "minimum": 1},
	}, "required": []string{"message_id", "content", "expected_revision"}}
}

func commentMessageDeleteSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"message_id": map[string]any{"type": "string", "minLength": 1}, "expected_revision": map[string]any{"type": "integer", "minimum": 1},
	}, "required": []string{"message_id", "expected_revision"}}
}

func notificationListSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"unread_only": map[string]any{"type": "boolean"}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 200},
	}}
}

func chartListSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"workbook_id": map[string]any{"type": "string", "minLength": 1}, "sheet_id": map[string]any{"type": "string"},
	}, "required": []string{"workbook_id"}}
}

func chartSchema(create bool) map[string]any {
	position := map[string]any{"type": "object", "properties": map[string]any{
		"x": map[string]any{"type": "integer", "minimum": 0}, "y": map[string]any{"type": "integer", "minimum": 0},
		"width": map[string]any{"type": "integer", "minimum": 240, "maximum": 1600}, "height": map[string]any{"type": "integer", "minimum": 160, "maximum": 1200},
	}, "required": []string{"x", "y", "width", "height"}}
	properties := map[string]any{
		"chart_id": map[string]any{"type": "string"}, "workbook_id": map[string]any{"type": "string"}, "sheet_id": map[string]any{"type": "string"}, "source_sheet_id": map[string]any{"type": "string"},
		"idempotency_key": map[string]any{"type": "string", "minLength": 1}, "type": map[string]any{"type": "string", "enum": []string{"bar", "line", "area", "pie", "scatter", "histogram"}},
		"title": map[string]any{"type": "string", "maxLength": 200}, "source_range": map[string]any{"type": "string"},
		"first_row_headers": map[string]any{"type": "boolean"}, "first_column_labels": map[string]any{"type": "boolean"},
		"legend_position": map[string]any{"type": "string", "enum": []string{"none", "top", "right", "bottom", "left"}},
		"x_axis_title":    map[string]any{"type": "string", "maxLength": 100}, "y_axis_title": map[string]any{"type": "string", "maxLength": 100},
		"position": position, "expected_revision": map[string]any{"type": "integer", "minimum": 1},
	}
	required := []string{"chart_id"}
	if create {
		delete(properties, "chart_id")
		delete(properties, "expected_revision")
		required = []string{"workbook_id", "sheet_id", "source_sheet_id", "idempotency_key", "type", "source_range"}
	} else {
		delete(properties, "workbook_id")
		delete(properties, "idempotency_key")
	}
	return map[string]any{"type": "object", "properties": properties, "required": required}
}

func chartDeleteSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"chart_id": map[string]any{"type": "string"}, "expected_revision": map[string]any{"type": "integer", "minimum": 1}}, "required": []string{"chart_id"}}
}

func pivotListSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"workbook_id": map[string]any{"type": "string", "minLength": 1}, "sheet_id": map[string]any{"type": "string"},
	}, "required": []string{"workbook_id"}}
}

func pivotSchema(create bool) map[string]any {
	customGroup := map[string]any{"type": "object", "properties": map[string]any{
		"name": map[string]any{"type": "string", "minLength": 1}, "values": map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "string"}},
	}, "required": []string{"name", "values"}}
	dimension := map[string]any{"type": "object", "properties": map[string]any{
		"column": map[string]any{"type": "integer", "minimum": 1}, "name": map[string]any{"type": "string"},
		"group":    map[string]any{"type": "string", "enum": []string{"none", "year", "quarter", "month", "day", "number", "custom"}},
		"interval": map[string]any{"type": "number", "exclusiveMinimum": 0}, "custom_groups": map[string]any{"type": "array", "items": customGroup},
	}, "required": []string{"column"}}
	value := map[string]any{"type": "object", "properties": map[string]any{
		"column": map[string]any{"type": "integer", "minimum": 1}, "name": map[string]any{"type": "string"},
		"aggregation": map[string]any{"type": "string", "enum": []string{"sum", "average", "count", "min", "max"}},
	}, "required": []string{"column", "aggregation"}}
	filter := map[string]any{"type": "object", "properties": map[string]any{
		"column":   map[string]any{"type": "integer", "minimum": 1},
		"operator": map[string]any{"type": "string", "enum": []string{"equals", "not_equals", "contains", "greater_than", "greater_or_equal", "less_than", "less_or_equal", "in", "is_blank", "not_blank"}},
		"value":    map[string]any{"type": "string"}, "values": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
	}, "required": []string{"column", "operator"}}
	calculated := map[string]any{"type": "object", "properties": map[string]any{
		"name": map[string]any{"type": "string", "minLength": 1}, "formula": map[string]any{"type": "string", "pattern": "^="},
	}, "required": []string{"name", "formula"}}
	properties := map[string]any{
		"pivot_id": map[string]any{"type": "string"}, "workbook_id": map[string]any{"type": "string"}, "sheet_id": map[string]any{"type": "string"}, "source_sheet_id": map[string]any{"type": "string"},
		"idempotency_key": map[string]any{"type": "string", "minLength": 1}, "name": map[string]any{"type": "string", "minLength": 1, "maxLength": 200}, "source_range": map[string]any{"type": "string"},
		"first_row_headers": map[string]any{"type": "boolean"}, "rows": map[string]any{"type": "array", "maxItems": 6, "items": dimension}, "columns": map[string]any{"type": "array", "maxItems": 6, "items": dimension},
		"values": map[string]any{"type": "array", "minItems": 1, "maxItems": 10, "items": value}, "filters": map[string]any{"type": "array", "maxItems": 10, "items": filter},
		"calculated_fields": map[string]any{"type": "array", "maxItems": 5, "items": calculated}, "refresh_mode": map[string]any{"type": "string", "enum": []string{"auto", "manual"}},
		"expected_revision": map[string]any{"type": "integer", "minimum": 1},
	}
	required := []string{"pivot_id"}
	if create {
		delete(properties, "pivot_id")
		delete(properties, "expected_revision")
		required = []string{"workbook_id", "sheet_id", "source_sheet_id", "idempotency_key", "name", "source_range", "values"}
	} else {
		delete(properties, "workbook_id")
		delete(properties, "idempotency_key")
	}
	return map[string]any{"type": "object", "properties": properties, "required": required}
}

func pivotDeleteSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"pivot_id": map[string]any{"type": "string"}, "expected_revision": map[string]any{"type": "integer", "minimum": 1}}, "required": []string{"pivot_id"}}
}

func pivotDrilldownSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"pivot_id": map[string]any{"type": "string"}, "row_key": map[string]any{"type": "string"}, "column_key": map[string]any{"type": "string"},
		"offset": map[string]any{"type": "integer", "minimum": 0}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 500},
	}, "required": []string{"pivot_id"}}
}

func rangeFormatSchema() map[string]any {
	border := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"preset": map[string]any{"type": "string", "enum": []string{"all", "outer", "inner", "horizontal", "vertical", "top", "right", "bottom", "left", "none"}},
			"style":  map[string]any{"type": "string", "enum": []string{"none", "thin", "medium", "thick", "dashed", "dotted", "double"}},
			"color":  map[string]any{"type": "string", "pattern": "^#[0-9A-Fa-f]{6}$"},
		},
		"required": []string{"preset"},
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"sheet_id": map[string]any{"type": "string"}, "range": map[string]any{"type": "string"},
			"style": map[string]any{"type": "object"}, "border": border,
			"base_version": map[string]any{"type": "integer"}, "idempotency_key": map[string]any{"type": "string"}, "client_id": map[string]any{"type": "string"},
		},
		"required": []string{"sheet_id", "range", "idempotency_key"},
		"anyOf":    []any{map[string]any{"required": []string{"style"}}, map[string]any{"required": []string{"border"}}},
	}
}
func conflictListSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"workbook_id":      map[string]any{"type": "string", "minLength": 1},
			"include_resolved": map[string]any{"type": "boolean"},
		},
		"required": []string{"workbook_id"},
	}
}

func conflictResolutionSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"conflict_id":       map[string]any{"type": "string", "minLength": 1},
			"idempotency_key":   map[string]any{"type": "string", "minLength": 1},
			"client_id":         map[string]any{"type": "string"},
			"expected_revision": map[string]any{"type": "integer", "minimum": 1},
			"resolution":        map[string]any{"type": "string", "enum": []string{workbook.ConflictKeepCurrent, workbook.ConflictRestorePrevious}},
		},
		"required": []string{"conflict_id", "idempotency_key", "expected_revision", "resolution"},
	}
}

func aiPlanSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"workbook_id":     map[string]any{"type": "string"},
			"sheet_id":        map[string]any{"type": "string"},
			"range":           map[string]any{"type": "string"},
			"request":         map[string]any{"type": "string", "maxLength": 4000},
			"mode":            map[string]any{"type": "string", "enum": []string{"formula", "explain", "fix", "summarize", "anomaly", "clean"}},
			"base_version":    map[string]any{"type": "number", "minimum": 1},
			"idempotency_key": map[string]any{"type": "string"},
			"client_id":       map[string]any{"type": "string"},
		},
		"required": []string{"workbook_id", "sheet_id", "range", "request", "mode", "base_version", "idempotency_key"},
	}
}

func aiExecutionSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action_id":         map[string]any{"type": "string"},
			"expected_revision": map[string]any{"type": "number", "minimum": 1},
			"idempotency_key":   map[string]any{"type": "string"},
			"client_id":         map[string]any{"type": "string"},
		},
		"required": []string{"action_id", "expected_revision", "idempotency_key"},
	}
}

func automationTriggerSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"type":     map[string]any{"type": "string", "enum": []string{automation.TriggerManual, automation.TriggerCellChange, automation.TriggerSchedule, automation.TriggerWebhook}},
			"sheet_id": map[string]any{"type": "string"},
			"range":    map[string]any{"type": "string"},
			"cron":     map[string]any{"type": "string", "description": "표준 5필드 Cron 또는 @hourly/@daily/@weekly/@monthly/@yearly"},
			"timezone": map[string]any{"type": "string", "description": "IANA 시간대. 생략하면 UTC"},
		},
		"required": []string{"type"},
	}
}

func automationWebhookSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"automation_id":   map[string]any{"type": "string", "minLength": 1},
			"idempotency_key": map[string]any{"type": "string", "minLength": 1, "maxLength": 200},
			"client_id":       map[string]any{"type": "string"},
			"payload":         map[string]any{"description": "최대 1MiB JSON 값. 원문은 저장하지 않고 SHA-256과 byte 수만 기록합니다."},
		},
		"required": []string{"automation_id", "idempotency_key"},
	}
}

func automationActionSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"type":     map[string]any{"type": "string", "enum": []string{automation.ActionSetValue, automation.ActionSetFormula, automation.ActionClear}},
			"sheet_id": map[string]any{"type": "string", "minLength": 1},
			"range":    map[string]any{"type": "string", "minLength": 1},
			"value":    map[string]any{},
			"formula":  map[string]any{"type": "string", "maxLength": 8192},
		},
		"required": []string{"type", "sheet_id", "range"},
	}
}

func automationCreateSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"workbook_id":     map[string]any{"type": "string", "minLength": 1},
			"name":            map[string]any{"type": "string", "minLength": 1, "maxLength": 120},
			"enabled":         map[string]any{"type": "boolean"},
			"trigger":         automationTriggerSchema(),
			"action":          automationActionSchema(),
			"idempotency_key": map[string]any{"type": "string", "minLength": 1, "maxLength": 200},
		},
		"required": []string{"workbook_id", "name", "enabled", "trigger", "action", "idempotency_key"},
	}
}

func automationUpdateSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"automation_id":     map[string]any{"type": "string", "minLength": 1},
			"name":              map[string]any{"type": "string", "minLength": 1, "maxLength": 120},
			"enabled":           map[string]any{"type": "boolean"},
			"trigger":           automationTriggerSchema(),
			"action":            automationActionSchema(),
			"expected_revision": map[string]any{"type": "integer", "minimum": 1},
		},
		"required": []string{"automation_id", "name", "enabled", "trigger", "action", "expected_revision"},
	}
}

func automationDeleteSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"automation_id":     map[string]any{"type": "string", "minLength": 1},
			"expected_revision": map[string]any{"type": "integer", "minimum": 1},
		},
		"required": []string{"automation_id", "expected_revision"},
	}
}

func automationRunSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"automation_id":         map[string]any{"type": "string", "minLength": 1},
			"expected_revision":     map[string]any{"type": "integer", "minimum": 1},
			"expected_base_version": map[string]any{"type": "integer", "minimum": 1},
			"idempotency_key":       map[string]any{"type": "string", "minLength": 1, "maxLength": 200},
			"client_id":             map[string]any{"type": "string"},
		},
		"required": []string{"automation_id", "expected_revision", "expected_base_version", "idempotency_key"},
	}
}

func automationUndoSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"run_id":          map[string]any{"type": "string", "minLength": 1},
			"idempotency_key": map[string]any{"type": "string", "minLength": 1, "maxLength": 200},
			"client_id":       map[string]any{"type": "string"},
		},
		"required": []string{"run_id", "idempotency_key"},
	}
}

func structureSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"sheet_id":        map[string]any{"type": "string", "minLength": 1},
			"base_version":    map[string]any{"type": "integer", "minimum": 1},
			"idempotency_key": map[string]any{"type": "string", "minLength": 1},
			"client_id":       map[string]any{"type": "string"},
			"axis":            map[string]any{"type": "string", "enum": []string{"row", "column"}},
			"action":          map[string]any{"type": "string", "enum": []string{"insert", "delete"}},
			"index":           map[string]any{"type": "integer", "minimum": 1},
			"count":           map[string]any{"type": "integer", "minimum": 1, "maximum": workbook.MaxStructuralCount},
		},
		"required": []string{"sheet_id", "base_version", "idempotency_key", "axis", "action", "index", "count"},
	}
}
func sheetLayoutSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"sheet_id":          map[string]any{"type": "string", "minLength": 1},
			"idempotency_key":   map[string]any{"type": "string", "minLength": 1},
			"client_id":         map[string]any{"type": "string"},
			"expected_revision": map[string]any{"type": "integer", "minimum": 1},
			"action":            map[string]any{"type": "string", "enum": []string{"resize", "reset_size", "hide", "show", "show_all", "freeze"}},
			"axis":              map[string]any{"type": "string", "enum": []string{"row", "column"}},
			"start":             map[string]any{"type": "integer", "minimum": 1},
			"count":             map[string]any{"type": "integer", "minimum": 1, "maximum": workbook.MaxLayoutSpan},
			"size":              map[string]any{"type": "number"},
			"frozen_rows":       map[string]any{"type": "integer", "minimum": 0, "maximum": workbook.MaxFrozenRows},
			"frozen_columns":    map[string]any{"type": "integer", "minimum": 0, "maximum": workbook.MaxFrozenColumns},
		},
		"required": []string{"sheet_id", "idempotency_key", "expected_revision", "action"},
	}
}
func filterViewSchema(create bool) map[string]any {
	operators := []string{"values", "equals", "not_equals", "contains", "not_contains", "starts_with", "ends_with", "greater_than", "greater_or_equal", "less_than", "less_or_equal", "is_blank", "is_not_blank", "background_color", "text_color"}
	criterion := map[string]any{"type": "object", "properties": map[string]any{
		"column": map[string]any{"type": "integer", "minimum": 1}, "operator": map[string]any{"type": "string", "enum": operators},
		"value": map[string]any{}, "values": map[string]any{"type": "array", "maxItems": 1000}, "color": map[string]any{"type": "string", "pattern": "^#[0-9A-Fa-f]{6}$"}, "case_sensitive": map[string]any{"type": "boolean"},
	}, "required": []string{"column", "operator"}}
	properties := map[string]any{
		"name": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}, "range": map[string]any{"type": "string"},
		"header_rows": map[string]any{"type": "integer", "minimum": 0}, "criteria": map[string]any{"type": "array", "items": criterion}, "active": map[string]any{"type": "boolean"},
	}
	required := []string{"filter_view_id"}
	properties["filter_view_id"] = map[string]any{"type": "string"}
	if create {
		delete(properties, "filter_view_id")
		properties["sheet_id"] = map[string]any{"type": "string"}
		properties["idempotency_key"] = map[string]any{"type": "string", "minLength": 1}
		required = []string{"sheet_id", "idempotency_key", "name", "range"}
	}
	return map[string]any{"type": "object", "properties": properties, "required": required}
}

func dataValidationSchema(create bool) map[string]any {
	option := map[string]any{"type": "object", "properties": map[string]any{
		"value": map[string]any{}, "label": map[string]any{"type": "string", "maxLength": 128}, "color": map[string]any{"type": "string", "pattern": "^#[0-9A-Fa-f]{6}$"},
	}, "required": []string{"value"}}
	properties := map[string]any{
		"validation_id": map[string]any{"type": "string"}, "sheet_id": map[string]any{"type": "string"}, "idempotency_key": map[string]any{"type": "string", "minLength": 1},
		"range": map[string]any{"type": "string"}, "rule_type": map[string]any{"type": "string", "enum": []string{"list", "number", "date", "custom_formula"}},
		"operator": map[string]any{"type": "string", "enum": []string{"in_list", "between", "not_between", "equal", "not_equal", "greater_than", "greater_or_equal", "less_than", "less_or_equal", "custom"}},
		"options":  map[string]any{"type": "array", "minItems": 1, "maxItems": workbook.MaxValidationOptions, "items": option}, "value": map[string]any{}, "value2": map[string]any{},
		"formula": map[string]any{"type": "string", "maxLength": 2000}, "allow_blank": map[string]any{"type": "boolean"}, "reject_input": map[string]any{"type": "boolean"},
		"show_dropdown": map[string]any{"type": "boolean"}, "display_style": map[string]any{"type": "string", "enum": []string{"chip", "arrow", "plain"}}, "help_text": map[string]any{"type": "string", "maxLength": 500},
		"expected_revision": map[string]any{"type": "integer", "minimum": 1},
	}
	required := []string{"validation_id"}
	if create {
		delete(properties, "validation_id")
		delete(properties, "expected_revision")
		required = []string{"sheet_id", "idempotency_key", "range", "rule_type"}
	} else {
		delete(properties, "sheet_id")
		delete(properties, "idempotency_key")
	}
	return map[string]any{"type": "object", "properties": properties, "required": required}
}

func dataValidationDeleteSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"validation_id": map[string]any{"type": "string"}, "expected_revision": map[string]any{"type": "integer", "minimum": 1}}, "required": []string{"validation_id"}}
}

func conditionalFormatSchema(create bool) map[string]any {
	color := map[string]any{"type": "string", "pattern": "^#[0-9A-Fa-f]{6}$"}
	properties := map[string]any{
		"conditional_format_id": map[string]any{"type": "string"},
		"sheet_id":              map[string]any{"type": "string"},
		"idempotency_key":       map[string]any{"type": "string", "minLength": 1},
		"name":                  map[string]any{"type": "string", "maxLength": 200},
		"range":                 map[string]any{"type": "string"},
		"rule_type":             map[string]any{"type": "string", "enum": []string{"value", "duplicate", "color_scale", "data_bar"}},
		"operator":              map[string]any{"type": "string", "enum": []string{"equals", "not_equals", "greater_than", "greater_or_equal", "less_than", "less_or_equal", "between", "not_between", "contains", "not_contains", "is_blank", "not_blank", "duplicate", "unique"}},
		"value":                 map[string]any{},
		"value2":                map[string]any{},
		"style":                 map[string]any{"type": "object"},
		"min_color":             color,
		"mid_color":             color,
		"max_color":             color,
		"bar_color":             color,
		"priority":              map[string]any{"type": "integer", "minimum": 1, "maximum": 1000},
		"stop_if_true":          map[string]any{"type": "boolean"},
		"expected_revision":     map[string]any{"type": "integer", "minimum": 1},
	}
	required := []string{"conditional_format_id"}
	if create {
		delete(properties, "conditional_format_id")
		delete(properties, "expected_revision")
		required = []string{"sheet_id", "idempotency_key", "range", "rule_type"}
	} else {
		delete(properties, "sheet_id")
		delete(properties, "idempotency_key")
	}
	return map[string]any{"type": "object", "properties": properties, "required": required}
}

func conditionalFormatEvaluationSchema() map[string]any {
	return requiredProps2("sheet_id", "string", "range", "string")
}

func conditionalFormatDeleteSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"conditional_format_id": map[string]any{"type": "string"}, "expected_revision": map[string]any{"type": "integer", "minimum": 1}}, "required": []string{"conditional_format_id"}}
}

func namedRangeSchema(create bool) map[string]any {
	properties := map[string]any{
		"named_range_id": map[string]any{"type": "string"}, "workbook_id": map[string]any{"type": "string"}, "sheet_id": map[string]any{"type": "string"},
		"idempotency_key": map[string]any{"type": "string", "minLength": 1}, "name": map[string]any{"type": "string", "minLength": 1, "maxLength": 255},
		"range": map[string]any{"type": "string"}, "expected_revision": map[string]any{"type": "integer", "minimum": 1},
	}
	required := []string{"named_range_id"}
	if create {
		delete(properties, "named_range_id")
		delete(properties, "expected_revision")
		required = []string{"workbook_id", "sheet_id", "idempotency_key", "name", "range"}
	} else {
		delete(properties, "workbook_id")
		delete(properties, "idempotency_key")
	}
	return map[string]any{"type": "object", "properties": properties, "required": required}
}

func namedRangeDeleteSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"named_range_id": map[string]any{"type": "string"}, "expected_revision": map[string]any{"type": "integer", "minimum": 1}}, "required": []string{"named_range_id"}}
}
func findMCPTool(name string) (mcpTool, bool) {
	for _, item := range mcpTools {
		if item.Name == name {
			return item, true
		}
	}
	return mcpTool{}, false
}
func decodeMCP(args map[string]any, target any) error {
	data, err := json.Marshal(args)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}
func stringArg(args map[string]any, key string) string { value, _ := args[key].(string); return value }
func numberArg(args map[string]any, key string) (int64, error) {
	switch value := args[key].(type) {
	case float64:
		return int64(value), nil
	case json.Number:
		return value.Int64()
	case string:
		return strconv.ParseInt(value, 10, 64)
	case nil:
		return 0, nil
	default:
		return 0, fmt.Errorf("%s must be a number", key)
	}
}
func okResult(err error) (any, error) { return map[string]any{"ok": err == nil}, err }

var _ = strings.TrimSpace
