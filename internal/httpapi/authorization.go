package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"kanpic/internal/workbook"
)

type accessContextKey struct{}
type principalCacheKey struct{}

// resourceCollections maps the first path segment after /api/v1 to the resource
// kind the identifier that follows belongs to. Keeping the table here means one
// authorization pass protects every workbook-scoped route, including routes that
// only receive a nested identifier such as a chart or a comment message.
var resourceCollections = map[string]string{
	"workbooks":           "workbookId",
	"sheets":              "sheetId",
	"charts":              "chartId",
	"pivots":              "pivotId",
	"named-ranges":        "namedRangeId",
	"comments":            "commentId",
	"comment-messages":    "messageId",
	"conflicts":           "conflictId",
	"versions":            "versionId",
	"operations":          "operationId",
	"filter-views":        "filterViewId",
	"data-validations":    "dataValidationId",
	"conditional-formats": "conditionalFormatId",
	"automations":         "automationId",
	"automation-runs":     "automationRunId",
	"access-requests":     "accessRequestId",
	// 덱은 프레젠테이션 서비스의 공용 계정 아래 있으므로, 여기 없으면 로그인한
	// 누구나 남의 워크북에서 나온 덱을 내려받을 수 있다.
	"presentations": "presentationId",
}

type authorizationTarget struct {
	kind       string
	id         string
	capability workbook.Capability
}

// accessPrincipal returns the identity an authorization decision is made for.
// The middleware resolves it once per request, including kanpic roles from the
// user directory, so handlers never recompute or re-query it.
func (s *Server) accessPrincipal(r *http.Request) workbook.AccessPrincipal {
	if principal, ok := r.Context().Value(principalCacheKey{}).(workbook.AccessPrincipal); ok {
		return principal
	}
	return s.identityPrincipal(r)
}

// identityPrincipal reads the identity from the session, the API key or the
// development actor header without consulting the directory.
func (s *Server) identityPrincipal(r *http.Request) workbook.AccessPrincipal {
	if user, ok := sessionUser(r); ok {
		admin := s.auth != nil && s.auth.IsAdmin(r.Context(), user)
		return workbook.AccessPrincipal{UserID: user.ID, Email: user.Email, Roles: user.Roles, Admin: admin, Authenticated: true}
	}
	if principal, ok := apiPrincipal(r); ok {
		return workbook.AccessPrincipal{UserID: principal.UserID, Admin: principal.Allows("admin.*"), Authenticated: true}
	}
	return workbook.AccessPrincipal{UserID: actorID(r), Authenticated: true}
}

// capabilityForRequest translates a route into the minimum role it needs.
func capabilityForRequest(r *http.Request) workbook.Capability {
	path := r.URL.Path
	readOnly := r.Method == http.MethodGet || r.Method == http.MethodHead
	switch {
	case strings.HasPrefix(path, "/ws/"):
		return workbook.CapabilityRead
	// Asking for access is exactly what a user without access needs to do.
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/access-requests"):
		return ""
	// A star is personal state, so anybody who can read may set it.
	case strings.HasSuffix(path, "/favorite"):
		return workbook.CapabilityRead
	// The trash is guarded against the deleted workbook list instead, because a
	// deleted workbook has no resolvable sharing.
	case strings.HasSuffix(path, "/restore") || strings.HasSuffix(path, "/purge") || strings.HasSuffix(path, "/trash"):
		return ""
	// Copying a sheet reads the source; the target is authorized separately.
	case strings.HasSuffix(path, "/copy"):
		return workbook.CapabilityRead
	// 덱을 만드는 것은 내보내기와 같다. 워크북은 그대로이므로 읽을 수 있으면
	// 만들 수 있다.
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/presentations"):
		return workbook.CapabilityRead
	// Previewing an automation is a read-only operation even though the REST
	// action uses POST so it can share the action endpoint shape with :run.
	case r.Method == http.MethodPost && strings.HasSuffix(path, ":test") && strings.Contains(path, "/automations/"):
		return workbook.CapabilityRead

	case strings.Contains(path, "/sharing") || strings.Contains(path, "/shares") || strings.Contains(path, "/access-requests"):
		if readOnly {
			return workbook.CapabilityRead
		}
		return workbook.CapabilityManage
	case readOnly:
		return workbook.CapabilityRead
	// Comment threads and replies are the only writes a commenter may perform.
	case strings.Contains(path, "/comments") || strings.Contains(path, "/comment-messages") || strings.Contains(path, "/replies"):
		return workbook.CapabilityComment
	// Deleting a workbook outright, like transferring it, stays with the owner.
	case r.Method == http.MethodDelete && workbookDeletePath(path):
		return workbook.CapabilityManage
	default:
		return workbook.CapabilityWrite
	}
}

func workbookDeletePath(path string) bool {
	trimmed := strings.Trim(strings.TrimPrefix(path, "/api/v1/"), "/")
	segments := strings.Split(trimmed, "/")
	return len(segments) == 2 && segments[0] == "workbooks"
}

// authorizationTargetFor finds the workbook-scoped resource a request addresses.
// Path values are unavailable before routing, so the identifier is read from the
// URL and any ":action" suffix is removed.
func authorizationTargetFor(r *http.Request) (authorizationTarget, bool) {
	path := r.URL.Path
	if strings.HasPrefix(path, "/ws/workbooks/") {
		id := strings.Trim(strings.TrimPrefix(path, "/ws/workbooks/"), "/")
		if id == "" {
			return authorizationTarget{}, false
		}
		return authorizationTarget{kind: "workbookId", id: id, capability: workbook.CapabilityRead}, true
	}
	if !strings.HasPrefix(path, "/api/v1/") {
		return authorizationTarget{}, false
	}
	segments := strings.Split(strings.Trim(strings.TrimPrefix(path, "/api/v1/"), "/"), "/")
	if len(segments) < 2 {
		return authorizationTarget{}, false
	}
	collection, identifier := segments[0], segments[1]
	if collection == "ai" && len(segments) >= 3 && segments[1] == "actions" {
		collection, identifier = "ai-actions", segments[2]
	}
	kind, ok := resourceCollections[collection]
	if collection == "ai-actions" {
		kind, ok = "aiActionId", true
	}
	if !ok {
		return authorizationTarget{}, false
	}
	if index := strings.Index(identifier, ":"); index >= 0 {
		identifier = identifier[:index]
	}
	if strings.TrimSpace(identifier) == "" {
		return authorizationTarget{}, false
	}
	capability := capabilityForRequest(r)
	if capability == "" {
		return authorizationTarget{}, false
	}
	return authorizationTarget{kind: kind, id: identifier, capability: capability}, true
}

// resolveTargetWorkbook finds the workbook a target belongs to. Automations and
// AI actions live in their own services, so they are asked first and fall back
// to the workbook repository.
func (s *Server) resolveTargetWorkbook(ctx context.Context, r *http.Request, target authorizationTarget) (string, bool, error) {
	switch target.kind {
	case "automationId":
		if s.automations != nil {
			if item, err := s.automations.Get(ctx, target.id); err == nil && item.WorkbookID != "" {
				return item.WorkbookID, true, nil
			}
		}
	case "automationRunId":
		if s.automations != nil {
			if workbookID, err := s.automations.GetRunWorkbookID(ctx, target.id); err == nil && workbookID != "" {
				return workbookID, true, nil
			}
		}
	case "presentationId":
		if s.presentations != nil {
			return s.resolvePresentationWorkbook(ctx, target.id)
		}
		return "", false, workbook.ErrNotFound
	case "aiActionId":
		if s.ai != nil {
			if item, err := s.ai.Get(ctx, actorID(r), target.id); err == nil && item.WorkbookID != "" {
				return item.WorkbookID, true, nil
			}
		}
	}
	workbookID, err := s.repository.WorkbookIDForResource(ctx, target.kind, target.id)
	if err != nil {
		return "", false, err
	}
	return workbookID, true, nil
}

// resolvePresentationWorkbook finds the workbook a deck was made from. A deck
// kanpic has no record of belongs to nobody and is refused rather than passed
// through to the provider.
func (s *Server) resolvePresentationWorkbook(ctx context.Context, id string) (string, bool, error) {
	workbookID, err := s.presentations.WorkbookIDFor(ctx, id)
	if err != nil || workbookID == "" {
		return "", false, workbook.ErrNotFound
	}
	return workbookID, true, nil
}

// authorizeWorkbookRequest enforces the workbook sharing rules for one request
// and stores the resolved access for handlers that need the effective role.
func (s *Server) authorizeWorkbookRequest(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	target, ok := authorizationTargetFor(r)
	if !ok {
		return r, true
	}
	workbookID, resolved, err := s.resolveTargetWorkbook(r.Context(), r, target)
	if err != nil {
		// Legacy AI fakes cannot always trace actions back to a workbook; automation
		// resources are intentionally fail-closed because their service has no
		// separate actor-level authorization pass.
		if target.kind == "aiActionId" {
			return r, true
		}
		s.writeError(w, r, err)
		return r, false
	}
	if !resolved {
		return r, true
	}
	principal := s.accessPrincipal(r)
	access, err := s.repository.ResolveWorkbookAccess(r.Context(), workbookID, principal)
	if err != nil {
		s.writeError(w, r, err)
		return r, false
	}
	if !access.Role.Allows(target.capability) {
		// Managing sharing is also granted to editors unless the owner locked it.
		if !(target.capability == workbook.CapabilityManage && access.CanManage) {
			s.writeAccessDenied(w, r, access, target.capability)
			return r, false
		}
	}
	return r.WithContext(context.WithValue(r.Context(), accessContextKey{}, access)), true
}

func (s *Server) writeAccessDenied(w http.ResponseWriter, r *http.Request, access workbook.WorkbookAccess, capability workbook.Capability) {
	message := "이 워크북에 대한 권한이 없습니다."
	code := "workbook_access_denied"
	if access.Role != workbook.RoleNone {
		switch capability {
		case workbook.CapabilityComment:
			message = "댓글을 작성할 권한이 없습니다. 보기 전용으로 공유되었습니다."
		case workbook.CapabilityWrite:
			message = "편집 권한이 없습니다. 소유자에게 편집자 권한을 요청하세요."
			code = "workbook_read_only"
		case workbook.CapabilityManage:
			message = "공유 설정은 소유자만 변경할 수 있습니다."
			code = "workbook_sharing_locked"
		}
	}
	writeJSON(w, http.StatusForbidden, map[string]any{"error": map[string]any{
		"code": code, "message": message, "trace_id": w.Header().Get("X-Trace-ID"),
		"access": access,
	}})
}

// writeCopyDenied reports a blocked download, print or copy for a viewer.
func (s *Server) writeCopyDenied(w http.ResponseWriter, r *http.Request, access workbook.WorkbookAccess) {
	writeJSON(w, http.StatusForbidden, map[string]any{"error": map[string]any{
		"code": "workbook_copy_disabled", "message": "소유자가 뷰어의 내보내기와 복사를 제한했습니다.",
		"trace_id": w.Header().Get("X-Trace-ID"), "access": access,
	}})
}

// workbookAccessFrom returns the access resolved by the authorization pass.
func workbookAccessFrom(r *http.Request) (workbook.WorkbookAccess, bool) {
	access, ok := r.Context().Value(accessContextKey{}).(workbook.WorkbookAccess)
	return access, ok
}

// authorizeWorkbookID checks a workbook identified inside the request body, such
// as an export request, and returns the resolved access.
func (s *Server) authorizeWorkbookID(w http.ResponseWriter, r *http.Request, workbookID string, capability workbook.Capability) (workbook.WorkbookAccess, bool) {
	if strings.TrimSpace(workbookID) == "" {
		s.writeError(w, r, workbook.ErrNotFound)
		return workbook.WorkbookAccess{}, false
	}
	access, err := s.repository.ResolveWorkbookAccess(r.Context(), workbookID, s.accessPrincipal(r))
	if err != nil {
		s.writeError(w, r, err)
		return workbook.WorkbookAccess{}, false
	}
	if !access.Role.Allows(capability) {
		s.writeAccessDenied(w, r, access, capability)
		return workbook.WorkbookAccess{}, false
	}
	return access, true
}

// mcpResourceArgs maps MCP tool arguments to the resource they identify so the
// tool surface enforces the same sharing rules as REST.
var mcpResourceArgs = []struct{ arg, kind string }{
	{"workbook_id", "workbookId"},
	{"sheet_id", "sheetId"},
	{"source_sheet_id", "sheetId"},
	{"chart_id", "chartId"},
	{"pivot_id", "pivotId"},
	{"named_range_id", "namedRangeId"},
	{"comment_id", "commentId"},
	{"message_id", "messageId"},
	{"conflict_id", "conflictId"},
	{"version_id", "versionId"},
	{"operation_id", "operationId"},
	{"filter_view_id", "filterViewId"},
	{"data_validation_id", "dataValidationId"},
	{"validation_id", "dataValidationId"},
	{"conditional_format_id", "conditionalFormatId"},
	{"automation_id", "automationId"},
	{"run_id", "automationRunId"},
	{"action_id", "aiActionId"},
	{"request_id", "accessRequestId"},
}

func mcpCapability(name string) workbook.Capability {
	switch {
	case name == "spreadsheet.automation.test":
		return workbook.CapabilityRead
	case strings.HasPrefix(name, "spreadsheet.share.") || strings.HasPrefix(name, "spreadsheet.department."):
		if strings.HasSuffix(name, ".list") || strings.HasSuffix(name, ".get") {
			return workbook.CapabilityRead
		}
		return workbook.CapabilityManage
	case strings.HasSuffix(name, ".list") || strings.HasSuffix(name, ".get") || strings.HasSuffix(name, ".read") ||
		strings.HasSuffix(name, ".data") || strings.HasSuffix(name, ".drilldown") || strings.HasSuffix(name, ".search") ||
		strings.HasSuffix(name, ".evaluate") || strings.HasSuffix(name, ".preview"):
		return workbook.CapabilityRead
	case strings.HasPrefix(name, "spreadsheet.comment.") || strings.HasPrefix(name, "spreadsheet.notification."):
		return workbook.CapabilityComment
	case strings.HasSuffix(name, ".transfer_ownership") || strings.HasSuffix(name, ".delete") && strings.HasPrefix(name, "spreadsheet.workbook."):
		return workbook.CapabilityManage
	default:
		return workbook.CapabilityWrite
	}
}

// authorizeMCPTool resolves the workbook a tool call targets and rejects calls
// the principal may not perform.
func (s *Server) authorizeMCPTool(r *http.Request, name string, args map[string]any) error {
	capability := mcpCapability(name)
	resolvedWorkbookID := ""
	for _, candidate := range mcpResourceArgs {
		value := strings.TrimSpace(stringArg(args, candidate.arg))
		if value == "" {
			continue
		}
		workbookID, resolved, err := s.resolveTargetWorkbook(r.Context(), r, authorizationTarget{kind: candidate.kind, id: value, capability: capability})
		if err != nil {
			if candidate.kind == "aiActionId" {
				// A few legacy AI service implementations cannot resolve an action.
				// Keep that compatibility path, but do not let it skip validation of
				// an already supplied workbook-scoped resource.
				if resolvedWorkbookID == "" {
					return nil
				}
				continue
			}
			return err
		}
		if !resolved {
			continue
		}
		access, err := s.repository.ResolveWorkbookAccess(r.Context(), workbookID, s.accessPrincipal(r))
		if err != nil {
			return err
		}
		if !access.Role.Allows(capability) && !(capability == workbook.CapabilityManage && access.CanManage) {
			return fmt.Errorf("%w: 이 워크북에 대한 %s 권한이 없습니다", workbook.ErrForbidden, capability)
		}
		if resolvedWorkbookID != "" && resolvedWorkbookID != workbookID {
			return fmt.Errorf("%w: MCP resource identifiers must belong to one workbook", workbook.ErrForbidden)
		}
		resolvedWorkbookID = workbookID
	}
	return nil
}

// resolveRequestPrincipal merges directory state into the request identity and
// rejects suspended accounts before any handler runs.
func (s *Server) resolveRequestPrincipal(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	if !isProtectedPath(r.URL.Path) {
		return r, true
	}
	principal := s.identityPrincipal(r)
	if strings.TrimSpace(principal.UserID) == "" {
		return r, true
	}
	if user, ok := sessionUser(r); ok {
		// Everybody who signs in shows up in the directory for administrators.
		_ = s.repository.EnsureUser(r.Context(), user.ID, user.DisplayName, user.Email)
	}
	profile, err := s.repository.UserAccessProfile(r.Context(), principal.UserID)
	if err != nil {
		s.writeError(w, r, err)
		return r, false
	}
	if profile.Suspended {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": map[string]string{
			"code": "account_suspended", "message": "정지된 계정입니다. 관리자에게 문의하세요.",
		}})
		return r, false
	}
	merged := mergeRoles(principal, profile.Roles)
	// Roles granted in the console can also carry administrator rights, so the
	// decision is re-evaluated against the merged role set.
	if !merged.Admin && s.auth != nil && len(profile.Roles) > 0 {
		merged.Admin = s.auth.HasAdminRole(r.Context(), merged.Roles)
	}
	return r.WithContext(context.WithValue(r.Context(), principalCacheKey{}, merged)), true
}

// mergeRoles combines identity provider roles with kanpic roles, ignoring
// duplicates so role-based sharing matches either source.
func mergeRoles(principal workbook.AccessPrincipal, local []string) workbook.AccessPrincipal {
	if len(local) == 0 {
		return principal
	}
	seen := make(map[string]struct{}, len(principal.Roles)+len(local))
	merged := make([]string, 0, len(principal.Roles)+len(local))
	for _, role := range append(append([]string{}, principal.Roles...), local...) {
		trimmed := strings.TrimSpace(role)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		merged = append(merged, trimmed)
	}
	principal.Roles = merged
	return principal
}
