package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"kanpic/internal/ai"
	"kanpic/internal/analytics"
	"kanpic/internal/apikey"
	"kanpic/internal/auth"
	"kanpic/internal/automation"
	"kanpic/internal/buildinfo"
	"kanpic/internal/collaboration"
	"kanpic/internal/formula"
	"kanpic/internal/importexport"
	"kanpic/internal/mail"
	"kanpic/internal/observability"
	"kanpic/internal/settings"
	"kanpic/internal/workbook"
	"kanpic/pkg/cellrange"
	"kanpic/pkg/identity"
)

const maxJSONBody = 2 << 20

type Server struct {
	repository  workbook.Repository
	logger      *slog.Logger
	settings    *settings.Repository
	keys        *apikey.Repository
	auth        *auth.Service
	logs        *observability.Store
	build       buildinfo.BuildInfo
	formula     *formula.Evaluator
	files       *importexport.Service
	collab      *collaboration.Hub
	ai          ai.Orchestrator
	automations automation.ServiceAPI
	mail        *mail.Service
	violations  *analytics.Recorder
}

func New(repository workbook.Repository, logger *slog.Logger) http.Handler {
	return NewPlatform(repository, nil, nil, nil, nil, logger)
}

func NewPlatform(repository workbook.Repository, settingRepository *settings.Repository, keys *apikey.Repository, authService *auth.Service, logs *observability.Store, logger *slog.Logger) http.Handler {
	return NewPlatformWithAI(repository, settingRepository, keys, authService, logs, nil, logger)
}

func NewPlatformWithAI(repository workbook.Repository, settingRepository *settings.Repository, keys *apikey.Repository, authService *auth.Service, logs *observability.Store, aiService ai.Orchestrator, logger *slog.Logger) http.Handler {
	return NewPlatformWithServices(repository, settingRepository, keys, authService, logs, aiService, nil, logger)
}

// PlatformOption configures optional services without changing the signature
// every caller already uses.
type PlatformOption func(*Server)

// WithMail wires the notification mailer.
func WithMail(service *mail.Service) PlatformOption {
	return func(s *Server) { s.mail = service }
}

func NewPlatformWithServices(repository workbook.Repository, settingRepository *settings.Repository, keys *apikey.Repository, authService *auth.Service, logs *observability.Store, aiService ai.Orchestrator, automationService automation.ServiceAPI, logger *slog.Logger, options ...PlatformOption) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{repository: repository, logger: logger, settings: settingRepository, keys: keys, auth: authService, logs: logs, build: buildinfo.Current(), formula: formula.New(), files: importexport.New(repository), collab: collaboration.New(repository, logger), ai: aiService, automations: automationService, violations: analytics.NewRecorder()}
	for _, option := range options {
		option(s)
	}
	if automationService != nil {
		s.collab.SetMutationListener(func(ctx context.Context, result workbook.MutationResult, cells []workbook.CellInput, actor, _ string) {
			s.triggerCellAutomationsContext(ctx, result, cells, actor)
		})
		if scheduled, ok := automationService.(interface {
			SetScheduledExecutionListener(func(automation.ExecutionResult))
		}); ok {
			scheduled.SetScheduledExecutionListener(func(result automation.ExecutionResult) {
				s.publishAutomationResult(result.Run.ActorID, "scheduler", result)
			})
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /api/v1/version", s.versionInfo)
	mux.HandleFunc("GET /api/v1/workbooks", s.listWorkbooks)
	mux.HandleFunc("POST /api/v1/workbooks", s.createWorkbook)
	mux.HandleFunc("GET /api/v1/workbooks/{workbookId}", s.getWorkbook)
	mux.HandleFunc("GET /api/v1/workbooks/{workbookId}/search", s.searchWorkbook)
	mux.HandleFunc("POST /api/v1/workbooks/{workbookId}/search:replace", s.replaceWorkbook)
	mux.HandleFunc("PATCH /api/v1/workbooks/{workbookId}", s.updateWorkbook)
	mux.HandleFunc("DELETE /api/v1/workbooks/{workbookId}", s.deleteWorkbook)
	mux.HandleFunc("POST /api/v1/workbooks/{workbookId}/duplicate", s.duplicateWorkbook)
	mux.HandleFunc("GET /api/v1/workbooks/trash", s.listDeletedWorkbooks)
	mux.HandleFunc("POST /api/v1/workbooks/{workbookId}/restore", s.restoreWorkbook)
	mux.HandleFunc("DELETE /api/v1/workbooks/{workbookId}/purge", s.purgeWorkbook)
	mux.HandleFunc("PUT /api/v1/workbooks/{workbookId}/favorite", s.setWorkbookFavorite)
	mux.HandleFunc("GET /api/v1/workbooks/{workbookId}/sheet-stats", s.sheetStats)
	mux.HandleFunc("POST /api/v1/sheets/{sheetId}/copy", s.copySheet)
	mux.HandleFunc("GET /api/v1/workbooks/{workbookId}/sharing", s.getWorkbookSharing)
	mux.HandleFunc("PATCH /api/v1/workbooks/{workbookId}/sharing", s.updateWorkbookSharing)
	mux.HandleFunc("POST /api/v1/workbooks/{workbookId}/sharing:transfer-ownership", s.transferWorkbookOwnership)
	mux.HandleFunc("PUT /api/v1/workbooks/{workbookId}/shares", s.putWorkbookShare)
	mux.HandleFunc("DELETE /api/v1/workbooks/{workbookId}/shares/{shareId}", s.deleteWorkbookShare)
	mux.HandleFunc("GET /api/v1/workbooks/{workbookId}/access-requests", s.listAccessRequests)
	mux.HandleFunc("POST /api/v1/workbooks/{workbookId}/access-requests", s.createAccessRequest)
	mux.HandleFunc("POST /api/v1/access-requests/{requestAction}", s.decideAccessRequest)
	mux.HandleFunc("GET /api/v1/users:lookup", s.lookupUsers)
	mux.HandleFunc("GET /api/v1/formula/functions", s.listFormulaFunctions)
	mux.HandleFunc("POST "+cspReportPath, s.receiveCSPReport)
	mux.HandleFunc("GET /api/v1/admin/analytics/violations", s.listAnalyticsViolations)
	mux.HandleFunc("DELETE /api/v1/admin/analytics/violations", s.clearAnalyticsViolations)
	mux.HandleFunc("POST /api/v1/admin/analytics/violations:allow", s.allowAnalyticsHost)
	mux.HandleFunc("GET /api/v1/templates", s.listTemplates)
	mux.HandleFunc("GET /api/v1/admin/overview", s.adminOverview)
	mux.HandleFunc("GET /api/v1/admin/workbooks", s.governedWorkbooks)
	mux.HandleFunc("GET /api/v1/admin/users", s.listUsers)
	mux.HandleFunc("POST /api/v1/admin/users", s.createUser)
	mux.HandleFunc("GET /api/v1/admin/users/{userId}", s.getUser)
	mux.HandleFunc("PATCH /api/v1/admin/users/{userId}", s.updateUser)
	mux.HandleFunc("POST /api/v1/admin/users/{userId}/roles", s.grantUserRole)
	mux.HandleFunc("DELETE /api/v1/admin/users/{userId}/roles/{role}", s.revokeUserRole)
	mux.HandleFunc("DELETE /api/v1/admin/users/{userId}/sessions", s.revokeUserSessions)
	mux.HandleFunc("GET /api/v1/departments", s.listDepartments)
	mux.HandleFunc("POST /api/v1/departments", s.createDepartment)
	mux.HandleFunc("GET /api/v1/departments/{departmentId}", s.getDepartment)
	mux.HandleFunc("PATCH /api/v1/departments/{departmentId}", s.updateDepartment)
	mux.HandleFunc("DELETE /api/v1/departments/{departmentId}", s.deleteDepartment)
	mux.HandleFunc("POST /api/v1/departments/{departmentId}/members", s.addDepartmentMembers)
	mux.HandleFunc("DELETE /api/v1/departments/{departmentId}/members/{memberId}", s.removeDepartmentMember)
	mux.HandleFunc("GET /api/v1/me/departments", s.listMyDepartments)
	mux.HandleFunc("POST /api/v1/workbooks/{workbookId}/sheets", s.createSheet)
	mux.HandleFunc("GET /api/v1/workbooks/{workbookId}/comments", s.listCommentThreads)
	mux.HandleFunc("POST /api/v1/workbooks/{workbookId}/comments", s.createCommentThread)
	mux.HandleFunc("GET /api/v1/comments/{commentId}", s.getCommentThread)
	mux.HandleFunc("PATCH /api/v1/comments/{commentId}", s.updateCommentThread)
	mux.HandleFunc("DELETE /api/v1/comments/{commentId}", s.deleteCommentThread)
	mux.HandleFunc("POST /api/v1/comments/{commentId}/replies", s.createCommentReply)
	mux.HandleFunc("PATCH /api/v1/comment-messages/{messageId}", s.updateCommentMessage)
	mux.HandleFunc("DELETE /api/v1/comment-messages/{messageId}", s.deleteCommentMessage)
	mux.HandleFunc("GET /api/v1/workbooks/{workbookId}/charts", s.listCharts)
	mux.HandleFunc("POST /api/v1/workbooks/{workbookId}/charts", s.createChart)
	mux.HandleFunc("GET /api/v1/charts/{chartId}", s.getChart)
	mux.HandleFunc("GET /api/v1/charts/{chartId}/data", s.getChartData)
	mux.HandleFunc("PATCH /api/v1/charts/{chartId}", s.updateChart)
	mux.HandleFunc("DELETE /api/v1/charts/{chartId}", s.deleteChart)
	mux.HandleFunc("GET /api/v1/workbooks/{workbookId}/pivots", s.listPivots)
	mux.HandleFunc("POST /api/v1/workbooks/{workbookId}/pivots", s.createPivot)
	mux.HandleFunc("GET /api/v1/pivots/{pivotId}", s.getPivot)
	mux.HandleFunc("GET /api/v1/pivots/{pivotId}/data", s.getPivotData)
	mux.HandleFunc("GET /api/v1/pivots/{pivotId}/drilldown", s.drilldownPivot)
	mux.HandleFunc("POST /api/v1/pivots/{pivotId}/refresh", s.refreshPivot)
	mux.HandleFunc("PATCH /api/v1/pivots/{pivotId}", s.updatePivot)
	mux.HandleFunc("DELETE /api/v1/pivots/{pivotId}", s.deletePivot)
	mux.HandleFunc("GET /api/v1/me/notifications", s.listMentionNotifications)
	mux.HandleFunc("PATCH /api/v1/me/notifications/{notificationId}", s.markMentionNotificationRead)
	mux.HandleFunc("GET /api/v1/workbooks/{workbookId}/named-ranges", s.listNamedRanges)
	mux.HandleFunc("POST /api/v1/workbooks/{workbookId}/named-ranges", s.createNamedRange)
	mux.HandleFunc("GET /api/v1/named-ranges/{namedRangeId}", s.getNamedRange)
	mux.HandleFunc("PATCH /api/v1/named-ranges/{namedRangeId}", s.updateNamedRange)
	mux.HandleFunc("DELETE /api/v1/named-ranges/{namedRangeId}", s.deleteNamedRange)
	mux.HandleFunc("POST /api/v1/sheets/{sheetId}/duplicate", s.duplicateSheet)
	mux.HandleFunc("PATCH /api/v1/sheets/{sheetId}", s.updateSheet)
	mux.HandleFunc("DELETE /api/v1/sheets/{sheetId}", s.deleteSheet)
	mux.HandleFunc("PATCH /api/v1/sheets/{sheetId}/cells:batch", s.applyCells)
	mux.HandleFunc("PATCH /api/v1/sheets/{sheetId}/cells:paste", s.pasteCells)
	mux.HandleFunc("PATCH /api/v1/sheets/{sheetId}/cells:fill", s.fillCells)
	mux.HandleFunc("PATCH /api/v1/sheets/{sheetId}/structure:apply", s.applyStructure)
	mux.HandleFunc("PATCH /api/v1/sheets/{sheetId}/layout:apply", s.applySheetLayout)
	mux.HandleFunc("PATCH /api/v1/sheets/{sheetId}/ranges:format", s.formatRange)
	mux.HandleFunc("PATCH /api/v1/sheets/{sheetId}/ranges:merge", s.mergeRange)
	mux.HandleFunc("PATCH /api/v1/sheets/{sheetId}/ranges:unmerge", s.unmergeRange)
	mux.HandleFunc("PATCH /api/v1/sheets/{sheetId}/ranges:sort", s.sortRange)
	mux.HandleFunc("GET /api/v1/sheets/{sheetId}/ranges/{range}", s.readRange)
	mux.HandleFunc("GET /api/v1/sheets/{sheetId}/filter-views", s.listFilterViews)
	mux.HandleFunc("POST /api/v1/sheets/{sheetId}/filter-views", s.createFilterView)
	mux.HandleFunc("GET /api/v1/filter-views/{filterViewId}", s.getFilterView)
	mux.HandleFunc("PATCH /api/v1/filter-views/{filterViewId}", s.updateFilterView)
	mux.HandleFunc("DELETE /api/v1/filter-views/{filterViewId}", s.deleteFilterView)
	mux.HandleFunc("POST /api/v1/filter-views/{filterAction}", s.evaluateFilterView)
	mux.HandleFunc("GET /api/v1/sheets/{sheetId}/data-validations", s.listDataValidations)
	mux.HandleFunc("POST /api/v1/sheets/{sheetId}/data-validations", s.createDataValidation)
	mux.HandleFunc("GET /api/v1/data-validations/{validationId}", s.getDataValidation)
	mux.HandleFunc("PATCH /api/v1/data-validations/{validationId}", s.updateDataValidation)
	mux.HandleFunc("DELETE /api/v1/data-validations/{validationId}", s.deleteDataValidation)
	mux.HandleFunc("POST /api/v1/data-validations/{validationAction}", s.evaluateDataValidation)
	mux.HandleFunc("GET /api/v1/sheets/{sheetId}/conditional-formats", s.listConditionalFormats)
	mux.HandleFunc("POST /api/v1/sheets/{sheetId}/conditional-formats", s.createConditionalFormat)
	mux.HandleFunc("GET /api/v1/sheets/{sheetId}/conditional-formats:evaluate", s.evaluateConditionalFormats)
	mux.HandleFunc("GET /api/v1/conditional-formats/{conditionalFormatId}", s.getConditionalFormat)
	mux.HandleFunc("PATCH /api/v1/conditional-formats/{conditionalFormatId}", s.updateConditionalFormat)
	mux.HandleFunc("DELETE /api/v1/conditional-formats/{conditionalFormatId}", s.deleteConditionalFormat)
	mux.HandleFunc("POST /api/v1/workbooks/{workbookId}/versions", s.createVersion)
	mux.HandleFunc("GET /api/v1/workbooks/{workbookId}/versions", s.listVersions)
	// net/http wildcards cannot contain a literal suffix. The action-form API
	// remains /versions/{id}:restore and is validated by the handler.
	mux.HandleFunc("POST /api/v1/versions/{versionAction}", s.restoreVersion)
	mux.HandleFunc("POST /api/v1/operations/{operationAction}", s.undoOperation)
	mux.HandleFunc("GET /api/v1/workbooks/{workbookId}/conflicts", s.listCellConflicts)
	mux.HandleFunc("GET /api/v1/conflicts/{conflictId}", s.getCellConflict)
	mux.HandleFunc("POST /api/v1/conflicts/{conflictAction}", s.resolveCellConflict)
	mux.HandleFunc("POST /api/v1/formulas:evaluate", s.evaluateFormula)
	mux.HandleFunc("GET /api/v1/sheets/{sheetId}/formulas/{address}", s.formulaInfo)
	mux.HandleFunc("POST /api/v1/imports:preview", s.previewImport)
	mux.HandleFunc("POST /api/v1/imports", s.executeImport)
	mux.HandleFunc("POST /api/v1/exports", s.executeExport)
	mux.HandleFunc("GET /ws/v1/workbooks/{workbookId}", s.webSocket)
	if settingRepository != nil {
		mux.HandleFunc("GET /api/v1/admin/settings", s.listSettings)
		mux.HandleFunc("PUT /api/v1/admin/settings/{key}", s.putSetting)
		mux.HandleFunc("DELETE /api/v1/admin/settings/{key}", s.deleteSetting)
		mux.HandleFunc("POST /api/v1/admin/settings:validate", s.validateSettings)
		mux.HandleFunc("POST /api/v1/admin/settings:test", s.testSettings)
		mux.HandleFunc("GET /api/v1/admin/settings/versions", s.listSettingVersions)
		mux.HandleFunc("POST /api/v1/admin/settings/versions/{versionAction}", s.restoreSettingVersion)
		mux.HandleFunc("GET /api/v1/me/preferences", s.getPreferences)
		mux.HandleFunc("PUT /api/v1/me/preferences", s.putPreferences)
	}
	if logs != nil {
		mux.HandleFunc("GET /api/v1/admin/logs", s.listLogs)
		mux.HandleFunc("POST /api/v1/admin/logs:purge", s.purgeLogs)
	}
	if keys != nil {
		mux.HandleFunc("GET /api/v1/me/api-keys", s.listMyKeys)
		mux.HandleFunc("POST /api/v1/me/api-keys", s.createKey)
		mux.HandleFunc("PATCH /api/v1/me/api-keys/{keyId}", s.updateKey)
		mux.HandleFunc("DELETE /api/v1/me/api-keys/{keyId}", s.revokeKey)
		mux.HandleFunc("POST /api/v1/me/api-keys/{keyAction}", s.rotateKey)
		mux.HandleFunc("GET /api/v1/admin/api-keys", s.listAllKeys)
	}
	if authService != nil {
		mux.HandleFunc("GET /api/v1/auth/config", s.authConfig)
		mux.HandleFunc("GET /auth/login", s.login)
		mux.HandleFunc("GET /auth/callback", s.authCallback)
		mux.HandleFunc("POST /auth/bootstrap/login", s.bootstrapLogin)
		mux.HandleFunc("GET /api/v1/session", s.session)
		mux.HandleFunc("POST /auth/logout", s.logout)
	}
	if aiService != nil {
		mux.HandleFunc("GET /api/v1/ai/config", s.aiConfig)
		mux.HandleFunc("GET /api/v1/admin/ai/actions", s.adminAIHistory)
		mux.HandleFunc("GET /api/v1/admin/ai/actions/{actionId}", s.adminAIAction)
		mux.HandleFunc("DELETE /api/v1/admin/ai/actions", s.adminPurgeAIHistory)
		mux.HandleFunc("GET /api/v1/admin/mail/deliveries", s.adminMailDeliveries)
		mux.HandleFunc("POST /api/v1/admin/mail:test", s.adminSendTestMail)
		mux.HandleFunc("POST /api/v1/ai/actions:plan", s.planAIAction)
		mux.HandleFunc("POST /api/v1/ai/prompt:preview", s.previewAIPrompt)
		mux.HandleFunc("GET /api/v1/workbooks/{workbookId}/ai/actions", s.listAIActions)
		mux.HandleFunc("GET /api/v1/ai/actions/{actionId}", s.getAIAction)
		mux.HandleFunc("POST /api/v1/ai/actions/{actionAction}", s.executeAIAction)
		if _, ok := aiService.(ai.WorkbookAgent); ok {
			mux.HandleFunc("GET /api/v1/workbooks/{workbookId}/agent/context", s.getAgentContext)
			mux.HandleFunc("POST /api/v1/workbooks/{workbookId}/agent/messages", s.sendAgentMessage)
			mux.HandleFunc("GET /api/v1/workbooks/{workbookId}/agent/runs", s.listAgentRuns)
			mux.HandleFunc("GET /api/v1/agent/runs/{runId}", s.getAgentRun)
			mux.HandleFunc("GET /api/v1/agent/runs/{runId}/plan", s.getAgentPlan)
			mux.HandleFunc("POST /api/v1/agent/runs/{runId}/approve", s.approveAgentRun)
			mux.HandleFunc("POST /api/v1/agent/runs/{runId}/cancel", s.cancelAgentRun)
			mux.HandleFunc("POST /api/v1/changesets/{changeSetId}/rollback", s.rollbackAgentChangeSet)
			// Keep the original colon-style preview endpoints for clients that
			// adopted the feature before the public Workbook Agent contract.
			mux.HandleFunc("POST /api/v1/agent/runs/{runAction}", s.executeAgentRun)
			mux.HandleFunc("POST /api/v1/changesets/{changeSetAction}", s.rollbackAgentChangeSet)
		}
	}
	if automationService != nil {
		mux.HandleFunc("GET /api/v1/workbooks/{workbookId}/automations", s.listAutomations)
		mux.HandleFunc("POST /api/v1/workbooks/{workbookId}/automations", s.createAutomation)
		mux.HandleFunc("GET /api/v1/automations/{automationId}", s.getAutomation)
		mux.HandleFunc("PATCH /api/v1/automations/{automationId}", s.updateAutomation)
		mux.HandleFunc("DELETE /api/v1/automations/{automationId}", s.deleteAutomation)
		mux.HandleFunc("POST /api/v1/automations/{automationAction}", s.executeAutomationAction)
		mux.HandleFunc("GET /api/v1/automations/{automationId}/runs", s.listAutomationRuns)
		mux.HandleFunc("POST /api/v1/automation-runs/{runAction}", s.undoAutomationRun)
	}
	mux.HandleFunc("POST /mcp", s.mcp)
	if directory := staticDirectory(); directory != "" {
		static := s.spaHandler(directory)
		mux.Handle("GET /", static)
	}
	return s.middleware(mux)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "kanpic-api"})
}

func (s *Server) createWorkbook(w http.ResponseWriter, r *http.Request) {
	var input struct {
		workbook.CreateWorkbookInput
		TemplateID string `json:"template_id,omitempty"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.OwnerID == "" {
		input.OwnerID = actorID(r)
	}
	template, hasTemplate := workbook.Template{}, false
	if strings.TrimSpace(input.TemplateID) != "" {
		template, hasTemplate = workbook.TemplateByID(input.TemplateID)
		if !hasTemplate {
			s.writeError(w, r, fmt.Errorf("%w: 알 수 없는 템플릿입니다", workbook.ErrInvalid))
			return
		}
		if strings.TrimSpace(input.Title) == "" {
			input.Title = template.Name
		}
	}
	created, err := s.repository.CreateWorkbook(r.Context(), input.CreateWorkbookInput)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if hasTemplate {
		if err := workbook.ApplyTemplate(r.Context(), s.repository, created, template, actorID(r)); err != nil {
			s.writeError(w, r, err)
			return
		}
		// The template writes cells, sheets and layout, so the workbook is read
		// back to return the version and sheets the client should start from.
		if filled, err := s.repository.GetWorkbook(r.Context(), created.ID); err == nil {
			created = filled
		}
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) listTemplates(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": workbook.TemplateCatalog()})
}

func (s *Server) listWorkbooks(w http.ResponseWriter, r *http.Request) {
	items, err := s.repository.ListWorkbooks(r.Context(), r.URL.Query().Get("workspace_id"), s.accessPrincipal(r))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getWorkbook(w http.ResponseWriter, r *http.Request) {
	item, err := s.repository.GetWorkbook(r.Context(), r.PathValue("workbookId"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if access, ok := workbookAccessFrom(r); ok {
		item.AccessRole, item.AccessSource = access.Role, access.Source
	}
	if sharing, err := s.repository.GetWorkbookSharing(r.Context(), item.ID); err == nil {
		item.SharedCount = len(sharing.Shares)
	}
	// Favourites are personal, so the flag always reflects the caller.
	if favorites, err := s.repository.WorkbookFavorites(r.Context(), actorID(r)); err == nil {
		item.Favorite = favorites[item.ID]
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) duplicateWorkbook(w http.ResponseWriter, r *http.Request) {
	var input workbook.DuplicateWorkbookInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if access, ok := workbookAccessFrom(r); ok && !access.CanCopy {
		s.writeCopyDenied(w, r, access)
		return
	}
	input.OwnerID = actorID(r)
	item, err := s.repository.DuplicateWorkbook(r.Context(), r.PathValue("workbookId"), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) updateWorkbook(w http.ResponseWriter, r *http.Request) {
	var input workbook.UpdateWorkbookInput
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := s.repository.UpdateWorkbook(r.Context(), r.PathValue("workbookId"), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.collab.PublishVersion(item.ID, actorID(r), "", "", item.Version)
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deleteWorkbook(w http.ResponseWriter, r *http.Request) {
	if err := s.repository.DeleteWorkbook(r.Context(), r.PathValue("workbookId"), actorID(r)); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) createSheet(w http.ResponseWriter, r *http.Request) {
	var input workbook.CreateSheetInput
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := s.repository.CreateSheet(r.Context(), r.PathValue("workbookId"), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.publishCurrentVersion(r.Context(), item.WorkbookID, actorID(r), "")
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) updateSheet(w http.ResponseWriter, r *http.Request) {
	var input workbook.UpdateSheetInput
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := s.repository.UpdateSheet(r.Context(), r.PathValue("sheetId"), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.publishCurrentVersion(r.Context(), item.WorkbookID, actorID(r), "")
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) duplicateSheet(w http.ResponseWriter, r *http.Request) {
	var input workbook.DuplicateSheetInput
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := s.repository.DuplicateSheet(r.Context(), r.PathValue("sheetId"), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.publishCurrentVersion(r.Context(), item.WorkbookID, actorID(r), "")
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) deleteSheet(w http.ResponseWriter, r *http.Request) {
	workbookID := s.workbookIDForSheet(r.Context(), r.PathValue("sheetId"))
	if err := s.repository.DeleteSheet(r.Context(), r.PathValue("sheetId")); err != nil {
		s.writeError(w, r, err)
		return
	}
	s.publishCurrentVersion(r.Context(), workbookID, actorID(r), "")
	w.WriteHeader(http.StatusNoContent)
}

type cellMutationRequest struct {
	BaseVersion    int64                `json:"base_version"`
	IdempotencyKey string               `json:"idempotency_key"`
	ClientID       string               `json:"client_id"`
	Cells          []workbook.CellInput `json:"cells"`
}

func (s *Server) applyCells(w http.ResponseWriter, r *http.Request) {
	s.applyCellsWithLimit(w, r, workbook.MaxBatchCells, "")
}

func (s *Server) pasteCells(w http.ResponseWriter, r *http.Request) {
	s.applyCellsWithLimit(w, r, workbook.MaxPasteCells, "cells.paste")
}

func (s *Server) fillCells(w http.ResponseWriter, r *http.Request) {
	s.applyCellsWithLimit(w, r, workbook.MaxPasteCells, "cells.fill")
}

func (s *Server) applyStructure(w http.ResponseWriter, r *http.Request) {
	var input workbook.StructuralMutation
	if !decodeJSON(w, r, &input) {
		return
	}
	input.SheetID = r.PathValue("sheetId")
	input.ActorID = actorID(r)
	if headerKey := strings.TrimSpace(r.Header.Get("Idempotency-Key")); headerKey != "" {
		input.IdempotencyKey = headerKey
	}
	result, err := s.repository.ApplyStructure(r.Context(), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if !result.Duplicate {
		s.collab.PublishStructure(result, input.ActorID, input.ClientID)
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) applySheetLayout(w http.ResponseWriter, r *http.Request) {
	var input workbook.SheetLayoutMutation
	if !decodeJSON(w, r, &input) {
		return
	}
	input.SheetID = r.PathValue("sheetId")
	input.ActorID = actorID(r)
	if headerKey := strings.TrimSpace(r.Header.Get("Idempotency-Key")); headerKey != "" {
		input.IdempotencyKey = headerKey
	}
	result, err := s.repository.ApplySheetLayout(r.Context(), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if !result.Duplicate {
		s.collab.PublishVersion(result.WorkbookID, input.ActorID, input.ClientID, result.OperationID, result.ServerVersion)
	}
	writeJSON(w, http.StatusOK, result)
}

type rangeFormatRequest struct {
	BaseVersion    int64                   `json:"base_version"`
	IdempotencyKey string                  `json:"idempotency_key"`
	ClientID       string                  `json:"client_id"`
	Range          string                  `json:"range"`
	Style          json.RawMessage         `json:"style,omitempty"`
	Border         *workbook.BorderCommand `json:"border,omitempty"`
}

type rangeMergeRequest struct {
	BaseVersion    int64  `json:"base_version"`
	IdempotencyKey string `json:"idempotency_key"`
	ClientID       string `json:"client_id"`
	Range          string `json:"range"`
}

type rangeSortRequest struct {
	BaseVersion    int64              `json:"base_version"`
	IdempotencyKey string             `json:"idempotency_key"`
	ClientID       string             `json:"client_id"`
	Range          string             `json:"range"`
	Keys           []workbook.SortKey `json:"keys"`
	HeaderRows     int                `json:"header_rows"`
	CaseSensitive  bool               `json:"case_sensitive"`
}

func (s *Server) formatRange(w http.ResponseWriter, r *http.Request) {
	var input rangeFormatRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	result, cells, err := s.applyRangeFormat(r.Context(), r.PathValue("sheetId"), actorID(r), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if !result.Duplicate && result.AppliedCells > 0 {
		s.collab.PublishOperation(result.WorkbookID, result.SheetID, actorID(r), input.ClientID, cells, result)
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) applyRangeFormat(ctx context.Context, sheetID, actor string, input rangeFormatRequest) (workbook.MutationResult, []workbook.CellInput, error) {
	selected, err := cellrange.Parse(input.Range)
	if err != nil {
		return workbook.MutationResult{}, nil, fmt.Errorf("%w: invalid range", workbook.ErrInvalid)
	}
	rows, columns := selected.End.Row-selected.Start.Row+1, selected.End.Column-selected.Start.Column+1
	if rows < 1 || columns < 1 || rows > workbook.MaxPasteCells || columns > workbook.MaxPasteCells || rows > workbook.MaxPasteCells/columns {
		return workbook.MutationResult{}, nil, fmt.Errorf("%w: formatted range must contain 1 to %d cells", workbook.ErrInvalid, workbook.MaxPasteCells)
	}
	if len(input.Style) == 0 && input.Border == nil {
		return workbook.MutationResult{}, nil, fmt.Errorf("%w: style or border is required", workbook.ErrInvalid)
	}
	if len(input.Style) > 0 {
		if err := workbook.ValidateStylePatch(input.Style); err != nil {
			return workbook.MutationResult{}, nil, err
		}
	}
	if input.Border != nil {
		input.Border.StartRow = selected.Start.Row
		input.Border.StartColumn = selected.Start.Column
		input.Border.EndRow = selected.End.Row
		input.Border.EndColumn = selected.End.Column
		if err := workbook.ValidateBorderCommand(*input.Border); err != nil {
			return workbook.MutationResult{}, nil, err
		}
	}
	cells := make([]workbook.CellInput, 0, rows*columns)
	for row := selected.Start.Row; row <= selected.End.Row; row++ {
		for column := selected.Start.Column; column <= selected.End.Column; column++ {
			cells = append(cells, workbook.CellInput{Row: row, Column: column})
		}
	}
	result, err := s.repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: actor, ClientID: input.ClientID, BaseVersion: input.BaseVersion, IdempotencyKey: input.IdempotencyKey, Cells: cells, StylePatch: input.Style, Border: input.Border, OperationType: "range.format"})
	return result, cells, err
}

func (s *Server) mergeRange(w http.ResponseWriter, r *http.Request) {
	s.changeRangeMerge(w, r, true)
}

func (s *Server) unmergeRange(w http.ResponseWriter, r *http.Request) {
	s.changeRangeMerge(w, r, false)
}

func (s *Server) changeRangeMerge(w http.ResponseWriter, r *http.Request, merge bool) {
	var input rangeMergeRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	result, cells, err := s.applyRangeMerge(r.Context(), r.PathValue("sheetId"), actorID(r), input, merge)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if !result.Duplicate && result.AppliedCells > 0 {
		s.collab.PublishOperation(result.WorkbookID, result.SheetID, actorID(r), input.ClientID, cells, result)
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) applyRangeMerge(ctx context.Context, sheetID, actor string, input rangeMergeRequest, merge bool) (workbook.MutationResult, []workbook.CellInput, error) {
	selected, err := cellrange.Parse(input.Range)
	if err != nil {
		return workbook.MutationResult{}, nil, fmt.Errorf("%w: invalid range", workbook.ErrInvalid)
	}
	existing, err := s.repository.ReadRange(ctx, sheetID, selected)
	if err != nil {
		return workbook.MutationResult{}, nil, err
	}
	cells, err := workbook.BuildMergeCells(existing, selected, merge)
	if err != nil {
		return workbook.MutationResult{}, nil, err
	}
	operationType := "range.merge"
	if !merge {
		operationType = "range.unmerge"
	}
	result, err := s.repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: actor, ClientID: input.ClientID, BaseVersion: input.BaseVersion, IdempotencyKey: input.IdempotencyKey, Cells: cells, OperationType: operationType})
	return result, cells, err
}

func (s *Server) sortRange(w http.ResponseWriter, r *http.Request) {
	var input rangeSortRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	result, cells, err := s.applyRangeSort(r.Context(), r.PathValue("sheetId"), actorID(r), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if !result.Duplicate && result.AppliedCells > 0 {
		s.collab.PublishOperation(result.WorkbookID, result.SheetID, actorID(r), input.ClientID, cells, result)
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) applyRangeSort(ctx context.Context, sheetID, actor string, input rangeSortRequest) (workbook.MutationResult, []workbook.CellInput, error) {
	selected, err := cellrange.Parse(input.Range)
	if err != nil {
		return workbook.MutationResult{}, nil, fmt.Errorf("%w: invalid range", workbook.ErrInvalid)
	}
	existing, err := s.repository.ReadRange(ctx, sheetID, selected)
	if err != nil {
		return workbook.MutationResult{}, nil, err
	}
	cells, err := workbook.BuildSortCells(existing, selected, workbook.SortOptions{Keys: input.Keys, HeaderRows: input.HeaderRows, CaseSensitive: input.CaseSensitive})
	if err != nil {
		return workbook.MutationResult{}, nil, err
	}
	result, err := s.repository.ApplyCells(ctx, workbook.CellMutation{SheetID: sheetID, ActorID: actor, ClientID: input.ClientID, BaseVersion: input.BaseVersion, IdempotencyKey: input.IdempotencyKey, Cells: cells, OperationType: "range.sort"})
	return result, cells, err
}

func (s *Server) applyCellsWithLimit(w http.ResponseWriter, r *http.Request, limit int, operationType string) {
	var input cellMutationRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	if len(input.Cells) == 0 || len(input.Cells) > limit {
		s.writeError(w, r, fmt.Errorf("%w: cells must contain 1 to %d entries", workbook.ErrInvalid, limit))
		return
	}
	result, err := s.repository.ApplyCells(r.Context(), workbook.CellMutation{
		SheetID: r.PathValue("sheetId"), ActorID: actorID(r), ClientID: input.ClientID,
		BaseVersion: input.BaseVersion, IdempotencyKey: input.IdempotencyKey, Cells: input.Cells, OperationType: operationType,
	})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if !result.Duplicate {
		s.collab.PublishOperation(result.WorkbookID, result.SheetID, actorID(r), input.ClientID, input.Cells, result)
		s.triggerCellAutomations(r, result, input.Cells)
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) undoOperation(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("operationAction")
	if !strings.HasSuffix(action, ":undo") {
		s.writeError(w, r, workbook.ErrNotFound)
		return
	}
	operationID := strings.TrimSuffix(action, ":undo")
	var input struct {
		IdempotencyKey string `json:"idempotency_key"`
		ClientID       string `json:"client_id"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if headerKey := strings.TrimSpace(r.Header.Get("Idempotency-Key")); headerKey != "" {
		input.IdempotencyKey = headerKey
	}
	result, err := s.repository.UndoOperation(r.Context(), workbook.UndoOperationInput{OperationID: operationID, ActorID: actorID(r), ClientID: input.ClientID, IdempotencyKey: input.IdempotencyKey})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if !result.Duplicate {
		s.collab.PublishOperation(result.WorkbookID, result.SheetID, actorID(r), input.ClientID, nil, result)
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) readRange(w http.ResponseWriter, r *http.Request) {
	selected, err := cellrange.Parse(r.PathValue("range"))
	if err != nil {
		s.writeError(w, r, fmt.Errorf("%w: %v", workbook.ErrInvalid, err))
		return
	}
	items, err := s.repository.ReadRange(r.Context(), r.PathValue("sheetId"), selected)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"range": selected, "items": items})
}

func (s *Server) createVersion(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := s.repository.CreateVersion(r.Context(), r.PathValue("workbookId"), input.Name, actorID(r))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) listVersions(w http.ResponseWriter, r *http.Request) {
	items, err := s.repository.ListVersions(r.Context(), r.PathValue("workbookId"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) restoreVersion(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("versionAction")
	if !strings.HasSuffix(action, ":restore") {
		http.NotFound(w, r)
		return
	}
	versionID := strings.TrimSuffix(action, ":restore")
	result, err := s.repository.RestoreVersion(r.Context(), versionID, actorID(r))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.collab.PublishVersion(result.WorkbookID, actorID(r), "", result.OperationID, result.ServerVersion)
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) webSocket(w http.ResponseWriter, r *http.Request) {
	canWrite := true
	if principal, ok := apiPrincipal(r); ok {
		canWrite = principal.Allows("range.write")
	}
	// Viewers and commenters join the session read-only.
	if access, ok := workbookAccessFrom(r); ok && !access.CanWrite {
		canWrite = false
	}
	s.collab.ServeHTTP(w, r, r.PathValue("workbookId"), actorID(r), canWrite)
}

func (s *Server) publishCurrentVersion(ctx context.Context, workbookID, actor, clientID string) {
	if workbookID == "" {
		return
	}
	book, err := s.repository.GetWorkbook(ctx, workbookID)
	if err == nil {
		s.collab.PublishVersion(workbookID, actor, clientID, "", book.Version)
	}
}

func (s *Server) workbookIDForSheet(ctx context.Context, sheetID string) string {
	books, err := s.repository.ListWorkbooks(ctx, "", workbook.AccessPrincipal{Admin: true, Authenticated: true})
	if err != nil {
		return ""
	}
	for _, candidate := range books {
		book, err := s.repository.GetWorkbook(ctx, candidate.ID)
		if err != nil {
			continue
		}
		for _, sheet := range book.Sheets {
			if sheet.ID == sheetID {
				return book.ID
			}
		}
	}
	return ""
}

// pagePolicy keeps the strict page policy and adds only what the configured
// tracking snippet needs, including a nonce for its inline code.
func (s *Server) pagePolicy(ctx context.Context, path, nonce string) string {
	return s.policyFor(s.analyticsConfig(ctx), path, nonce)
}

func (s *Server) policyFor(config analytics.Config, path, nonce string) string {
	scripts := []string{"'self'"}
	connects := []string{"'self'", "ws:", "wss:"}
	images := []string{"'self'", "data:"}
	if config.Active(path) {
		extraScripts, extraConnects, extraImages := config.PolicySources()
		scripts = append(scripts, "'nonce-"+nonce+"'")
		scripts = append(scripts, extraScripts...)
		connects = append(connects, extraConnects...)
		images = append(images, extraImages...)
	}
	policy := "default-src 'self'; script-src " + strings.Join(scripts, " ") +
		"; style-src 'self'; img-src " + strings.Join(images, " ") +
		"; connect-src " + strings.Join(connects, " ") +
		"; frame-ancestors 'none'; base-uri 'self'; form-action 'self'"
	if config.Active(path) {
		// While tracking is on, ask the browser to say what it refused. That
		// report is what turns a console error into a one-click fix.
		policy += "; report-uri " + cspReportPath
	}
	return policy
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		traceID := strings.TrimSpace(r.Header.Get("X-Trace-ID"))
		if traceID == "" {
			traceID = identity.New()
		}
		w.Header().Set("X-Trace-ID", traceID)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/ws/") || r.URL.Path == "/mcp" || r.URL.Path == "/healthz" {
			w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		} else {
			nonce := identity.New()
			r = r.WithContext(context.WithValue(r.Context(), nonceKey{}, nonce))
			w.Header().Set("Content-Security-Policy", s.pagePolicy(r.Context(), r.URL.Path, nonce))
		}
		w.Header().Set("Cache-Control", "no-store")
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Origin", "http://localhost:5173")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Idempotency-Key, X-Kanpic-Actor, X-Trace-ID")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Header.Get("Origin") == "http://localhost:5173" {
			w.Header().Set("Access-Control-Allow-Origin", "http://localhost:5173")
		}
		authenticated := false
		if s.keys != nil {
			authorization := strings.TrimSpace(r.Header.Get("Authorization"))
			if authorization != "" {
				if !strings.HasPrefix(authorization, "Bearer ") {
					writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]string{"code": "invalid_authorization", "message": "Bearer 인증이 필요합니다."}})
					return
				}
				principal, err := s.keys.Authenticate(r.Context(), strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer ")))
				if err != nil {
					writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]string{"code": "invalid_api_key", "message": "API 키가 유효하지 않거나 만료되었습니다."}})
					return
				}
				required := requiredScope(r)
				if !principal.Allows(required) {
					writeJSON(w, http.StatusForbidden, map[string]any{"error": map[string]string{"code": "insufficient_scope", "message": required + " scope가 필요합니다."}})
					return
				}
				r = r.WithContext(context.WithValue(r.Context(), principalContextKey{}, principal))
				authenticated = true
			}
		}
		if !authenticated && s.auth != nil {
			if cookie, err := r.Cookie(auth.SessionCookie); err == nil && cookie.Value != "" {
				if user, err := s.auth.Session(r.Context(), cookie.Value); err == nil {
					r = r.WithContext(context.WithValue(r.Context(), userContextKey{}, user))
					authenticated = true
				}
			}
			if !authenticated && isProtectedPath(r.URL.Path) {
				config, err := s.auth.Config(r.Context())
				if err != nil {
					writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": map[string]string{"code": "auth_config_unavailable", "message": "인증 설정을 읽을 수 없습니다."}})
					return
				}
				if config.Enabled || s.auth.BootstrapEnabled() {
					writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]string{"code": "authentication_required", "message": "로그인이 필요합니다."}})
					return
				}
			}
		}
		identified, active := s.resolveRequestPrincipal(w, r)
		if !active {
			s.logger.Info("http request", "method", r.Method, "path", r.URL.Path, "trace_id", traceID, "duration_ms", time.Since(started).Milliseconds())
			return
		}
		r = identified
		guarded, allowed := s.authorizeWorkbookRequest(w, r)
		if !allowed {
			s.logger.Info("http request", "method", r.Method, "path", r.URL.Path, "trace_id", traceID, "duration_ms", time.Since(started).Milliseconds())
			return
		}
		r = guarded
		next.ServeHTTP(w, r)
		s.logger.Info("http request", "method", r.Method, "path", r.URL.Path, "trace_id", traceID, "duration_ms", time.Since(started).Milliseconds())
	})
}

func (s *Server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	var validationFailure *workbook.ValidationFailure
	if errors.As(err, &validationFailure) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": map[string]any{"code": "validation_failed", "message": validationFailure.Error(), "trace_id": w.Header().Get("X-Trace-ID"), "violations": validationFailure.Violations}})
		return
	}
	status, code, message := http.StatusInternalServerError, "internal_error", "요청을 처리하지 못했습니다."
	switch {
	case errors.Is(err, workbook.ErrNotFound):
		status, code, message = http.StatusNotFound, "not_found", "요청한 리소스를 찾을 수 없습니다."
	case errors.Is(err, workbook.ErrInvalid):
		status, code, message = http.StatusBadRequest, "invalid_request", err.Error()
	case errors.Is(err, workbook.ErrDuplicateName):
		status, code, message = http.StatusConflict, "duplicate_name", "같은 이름이 이미 존재합니다."
	case errors.Is(err, workbook.ErrVersionAhead):
		status, code, message = http.StatusConflict, "invalid_base_version", "클라이언트 버전이 서버보다 최신입니다. 다시 동기화하세요."
	case errors.Is(err, workbook.ErrVersionConflict):
		status, code, message = http.StatusConflict, "version_conflict", "워크북 구조가 변경되었습니다. 최신 버전으로 다시 동기화하세요."
	case errors.Is(err, workbook.ErrForbidden):
		status, code, message = http.StatusForbidden, "workbook_access_denied", "이 워크북에 대한 권한이 없습니다."
	case errors.Is(err, workbook.ErrRevision):
		status, code, message = http.StatusConflict, "revision_conflict", "다른 사용자가 규칙을 변경했습니다. 다시 불러오세요."
	default:
		s.logger.Error("request failed", "error", err, "path", r.URL.Path)
	}
	writeJSON(w, status, map[string]any{"error": map[string]any{"code": code, "message": message, "trace_id": w.Header().Get("X-Trace-ID")}})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]string{"code": "invalid_json", "message": "JSON 요청 본문이 올바르지 않습니다."}})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func actorID(r *http.Request) string {
	if principal, ok := apiPrincipal(r); ok {
		return principal.UserID
	}
	if user, ok := sessionUser(r); ok {
		return user.ID
	}
	if id := strings.TrimSpace(r.Header.Get("X-Kanpic-Actor")); id != "" {
		return id
	}
	return "local-user"
}

type principalContextKey struct{}
type userContextKey struct{}

func apiPrincipal(r *http.Request) (apikey.Principal, bool) {
	principal, ok := r.Context().Value(principalContextKey{}).(apikey.Principal)
	return principal, ok
}

func sessionUser(r *http.Request) (auth.User, bool) {
	user, ok := r.Context().Value(userContextKey{}).(auth.User)
	return user, ok
}

func isProtectedPath(path string) bool {
	if path == "/healthz" || path == "/api/v1/version" || path == "/api/v1/auth/config" || path == "/api/v1/session" {
		return false
	}
	if strings.HasPrefix(path, "/auth/") {
		return false
	}
	return strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/ws/") || path == "/mcp"
}

func requiredScope(r *http.Request) string {
	path := r.URL.Path
	if path == "/healthz" || path == "/api/v1/version" {
		return ""
	}
	if strings.HasPrefix(path, "/ws/") {
		return "workbook.read"
	}
	if strings.HasPrefix(path, "/api/v1/admin/") {
		return "admin.*"
	}
	if strings.Contains(path, "/api-keys") {
		if r.Method == http.MethodGet {
			return "api_keys.read"
		}
		return "api_keys.write"
	}
	if strings.Contains(path, "/preferences") {
		if r.Method == http.MethodGet {
			return "profile.read"
		}
		return "profile.write"
	}
	if strings.HasSuffix(path, "users:lookup") || strings.HasSuffix(path, "/formula/functions") || strings.HasSuffix(path, "/templates") {
		return "workbook.read"
	}
	if strings.HasSuffix(path, "/favorite") || strings.HasSuffix(path, "/trash") {
		return "workbook.read"
	}
	if strings.Contains(path, "/departments") {
		if r.Method == http.MethodGet {
			return "workbook.read"
		}
		return "admin.*"
	}
	if strings.Contains(path, "/sharing") || strings.Contains(path, "/shares") || strings.Contains(path, "/access-requests") {
		if r.Method == http.MethodGet {
			return "workbook.read"
		}
		return "workbook.write"
	}
	if strings.Contains(path, "/comments") || strings.Contains(path, "/comment-messages") || strings.Contains(path, "/notifications") {
		if r.Method == http.MethodGet {
			return "comment.read"
		}
		return "comment.write"
	}
	if strings.Contains(path, "/charts") {
		if r.Method == http.MethodGet {
			return "chart.read"
		}
		return "chart.write"
	}
	if strings.Contains(path, "/pivots") {
		if r.Method == http.MethodGet {
			return "pivot.read"
		}
		return "pivot.write"
	}
	if strings.Contains(path, "/filter-views/") && strings.HasSuffix(path, ":evaluate") {
		return "range.read"
	}
	if strings.Contains(path, "/filter-views") {
		if r.Method == http.MethodGet {
			return "range.read"
		}
		return "range.write"
	}
	if strings.Contains(path, "/data-validations/") && strings.HasSuffix(path, ":evaluate") {
		return "range.read"
	}
	if strings.Contains(path, "/data-validations") {
		if r.Method == http.MethodGet {
			return "range.read"
		}
		return "range.write"
	}
	if strings.Contains(path, "/conditional-formats") {
		if r.Method == http.MethodGet {
			return "format.read"
		}
		return "format.write"
	}
	if strings.Contains(path, "/conflicts") {
		if r.Method == http.MethodGet {
			return "range.read"
		}
		return "range.write"
	}
	if strings.Contains(path, "/ai/") || strings.Contains(path, "/ai:") {
		return "ai.use"
	}
	if strings.Contains(path, "/agent/") || strings.Contains(path, "/changesets/") {
		return "ai.use"
	}
	if strings.Contains(path, "/automation-runs/") {
		return "automation.run"
	}
	if strings.Contains(path, "/automations") {
		if r.Method == http.MethodGet || strings.HasSuffix(path, ":test") {
			return "automation.read"
		}
		if strings.HasSuffix(path, ":webhook") {
			return "automation.webhook.invoke"
		}
		if strings.HasSuffix(path, ":run") {
			return "automation.run"
		}
		return "automation.write"
	}
	if strings.Contains(path, "ranges:format") || strings.Contains(path, "ranges:merge") || strings.Contains(path, "ranges:unmerge") || strings.Contains(path, "layout:apply") {
		return "format.write"
	}
	if strings.Contains(path, "ranges:sort") {
		return "range.write"
	}
	if strings.Contains(path, "structure:apply") || strings.HasSuffix(path, "search:replace") {
		return "range.write"
	}
	if strings.Contains(path, "/ranges/") {
		return "range.read"
	}
	if strings.Contains(path, "cells:") {
		return "range.write"
	}
	if strings.Contains(path, "/formulas") {
		return "formula.read"
	}
	if strings.Contains(path, "/imports") {
		return "import.write"
	}
	if strings.Contains(path, "/exports") {
		return "export.read"
	}
	if strings.Contains(path, "/versions") {
		if r.Method == http.MethodGet {
			return "version.read"
		}
		return "version.write"
	}
	if strings.Contains(path, "/operations") {
		return "range.write"
	}
	if strings.Contains(path, "/sheets") {
		if r.Method == http.MethodGet {
			return "workbook.read"
		}
		return "workbook.write"
	}
	if strings.Contains(path, "/workbooks") {
		if r.Method == http.MethodGet {
			return "workbook.read"
		}
		return "workbook.write"
	}
	if path == "/mcp" {
		return "mcp.use"
	}
	return ""
}

func staticDirectory() string {
	for _, candidate := range []string{"/app/web", "web/dist"} {
		if info, err := os.Stat(filepath.Join(candidate, "index.html")); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

// nonceKey carries the per-request script nonce from the middleware to the
// page handler so the policy header and the injected snippet agree.
type nonceKey struct{}

func requestNonce(r *http.Request) string {
	value, _ := r.Context().Value(nonceKey{}).(string)
	return value
}

// analyticsConfig reads the tracking settings. Failures are treated as "no
// tracking" so a settings outage never breaks the page.
func (s *Server) analyticsConfig(ctx context.Context) analytics.Config {
	if s.settings == nil {
		return analytics.Config{}
	}
	values, err := s.settings.Values(ctx)
	if err != nil {
		return analytics.Config{}
	}
	return analytics.ReadConfig(values)
}

// serveIndex injects the tracking snippet into the single page shell. The file
// is small and served with no-store, so it is read and rewritten per request
// rather than cached in a second place.
func (s *Server) serveIndex(w http.ResponseWriter, r *http.Request, directory string) {
	s.serveIndexWith(w, r, directory, s.analyticsConfig(r.Context()))
}

func (s *Server) serveIndexWith(w http.ResponseWriter, r *http.Request, directory string, config analytics.Config) {
	path := filepath.Join(directory, "index.html")
	page, err := os.ReadFile(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if config.Active(r.URL.Path) {
		if snippet := config.Snippet(requestNonce(r)); snippet != "" {
			page = injectSnippet(page, snippet, config.Placement)
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeContent(w, r, "index.html", time.Time{}, strings.NewReader(string(page)))
}

// injectSnippet places the markup just before the closing tag it belongs to,
// falling back to the end of the document when the tag is missing.
func injectSnippet(page []byte, snippet, placement string) []byte {
	marker := "</head>"
	if placement == "body" {
		marker = "</body>"
	}
	text := string(page)
	index := strings.LastIndex(strings.ToLower(text), marker)
	if index < 0 {
		return []byte(text + "\n" + snippet + "\n")
	}
	return []byte(text[:index] + snippet + "\n" + text[index:])
}

func (s *Server) spaHandler(directory string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := strings.TrimPrefix(filepath.Clean("/"+r.URL.Path), string(filepath.Separator))
		candidate := filepath.Join(directory, clean)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			if strings.HasPrefix(r.URL.Path, "/assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			http.ServeFile(w, r, candidate)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		s.serveIndex(w, r, directory)
	})
}
