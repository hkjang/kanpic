package workbook

import (
	"context"
	"errors"

	"kanpic/pkg/cellrange"
)

const (
	MaxBatchCells = 1_000
	MaxPasteCells = 10_000
	// Sorting rewrites every cell in the range, so it used to sit under the
	// paste limit and refuse anything past ~3,300 rows of three columns. A
	// sort is one deliberate action on data the user already has, not a
	// stream of edits, so it gets its own ceiling.
	MaxSortCells = 60_000
)

var (
	ErrNotFound        = errors.New("resource not found")
	ErrInvalid         = errors.New("invalid input")
	ErrVersionAhead    = errors.New("base version is newer than the server version")
	ErrVersionConflict = errors.New("base version does not match the server version")
	ErrDuplicateName   = errors.New("name already exists")
	ErrValidation      = errors.New("data validation failed")
	ErrRevision        = errors.New("revision conflict")
	ErrForbidden       = errors.New("access denied")
)

type Repository interface {
	CreateWorkbook(context.Context, CreateWorkbookInput) (Workbook, error)
	ListWorkbooks(context.Context, string, AccessPrincipal) ([]Workbook, error)
	BrowseWorkbooks(context.Context, AccessPrincipal, WorkbookQuery) (WorkbookPage, error)

	// One cell's edit history, read out of the operation log.
	CellHistory(context.Context, string, int, int, int) (CellHistory, error)

	// Cross-workbook connections (IMPORTRANGE).
	ListConnections(context.Context, string) (WorkbookConnections, error)
	RefreshConnections(context.Context, string, string) (WorkbookConnections, error)

	// Sharing, departments and access requests.
	WorkbookIDForResource(context.Context, string, string) (string, error)
	ResolveWorkbookAccess(context.Context, string, AccessPrincipal) (WorkbookAccess, error)
	GetWorkbookSharing(context.Context, string) (WorkbookSharing, error)
	UpdateWorkbookSharing(context.Context, string, UpdateSharingInput) (WorkbookSharing, error)
	PutWorkbookShare(context.Context, string, ShareInput) (WorkbookShare, error)
	DeleteWorkbookShare(context.Context, string, string) error
	TransferWorkbookOwnership(context.Context, string, TransferOwnershipInput) (WorkbookSharing, error)
	CreateDepartment(context.Context, CreateDepartmentInput) (Department, error)
	AddDepartmentManagers(context.Context, string, DepartmentMembersInput) (Department, error)
	RemoveDepartmentManager(context.Context, string, string) (Department, error)
	ManagedMembers(context.Context, string) ([]string, error)
	GetDepartment(context.Context, string) (Department, error)
	ListDepartments(context.Context) ([]Department, error)
	ListDepartmentsForUser(context.Context, string) ([]Department, error)
	UpdateDepartment(context.Context, string, UpdateDepartmentInput) (Department, error)
	DeleteDepartment(context.Context, string) error
	AddDepartmentMembers(context.Context, string, DepartmentMembersInput) (Department, error)
	RemoveDepartmentMember(context.Context, string, string) (Department, error)
	UserAccessProfile(context.Context, string) (UserAccessProfile, error)
	EnsureUser(context.Context, string, string, string) error
	ListUsers(context.Context) ([]DirectoryUser, error)
	LookupUsers(context.Context, []string) ([]UserSummary, error)
	SearchUsers(context.Context, string, int) ([]UserSummary, error)
	AdminOverview(context.Context) (AdminOverview, error)
	GovernedWorkbooks(context.Context, string, int) ([]GovernedWorkbook, error)
	WorkbooksOwnedBy(context.Context, string) ([]GovernedWorkbook, error)
	GetUser(context.Context, string) (DirectoryUser, error)
	UpsertUser(context.Context, UpsertUserInput) (DirectoryUser, error)
	UpdateUser(context.Context, string, UpdateUserInput) (DirectoryUser, error)
	GrantUserRole(context.Context, string, string, string) (DirectoryUser, error)
	RevokeUserRole(context.Context, string, string) (DirectoryUser, error)
	SetWorkbookFavorite(context.Context, string, string, bool) error
	WorkbookFavorites(context.Context, string) (map[string]bool, error)
	ListDeletedWorkbooks(context.Context, string, AccessPrincipal) ([]Workbook, error)
	RestoreWorkbook(context.Context, string, string) (Workbook, error)
	PurgeWorkbook(context.Context, string) error
	SheetStats(context.Context, string) ([]SheetStats, error)
	CopySheetToWorkbook(context.Context, string, CopySheetInput) (Sheet, error)
	CreateAccessRequest(context.Context, string, CreateAccessRequestInput) (AccessRequest, error)
	ListAccessRequests(context.Context, string, bool) ([]AccessRequest, error)
	DecideAccessRequest(context.Context, string, DecideAccessRequestInput) (AccessRequest, error)
	GetWorkbook(context.Context, string) (Workbook, error)
	DuplicateWorkbook(context.Context, string, DuplicateWorkbookInput) (Workbook, error)
	UpdateWorkbook(context.Context, string, UpdateWorkbookInput) (Workbook, error)
	DeleteWorkbook(context.Context, string, string) error

	CreateSheet(context.Context, string, CreateSheetInput) (Sheet, error)
	DuplicateSheet(context.Context, string, DuplicateSheetInput) (Sheet, error)
	UpdateSheet(context.Context, string, UpdateSheetInput) (Sheet, error)
	DeleteSheet(context.Context, string, string) (SheetDeletion, error)
	CreateFilterView(context.Context, string, string, CreateFilterViewInput) (FilterView, error)
	ListFilterViews(context.Context, string, string) ([]FilterView, error)
	GetFilterView(context.Context, string, string) (FilterView, error)
	UpdateFilterView(context.Context, string, string, UpdateFilterViewInput) (FilterView, error)
	DeleteFilterView(context.Context, string, string) error
	CreateDataValidation(context.Context, string, string, CreateDataValidationInput) (DataValidation, error)
	ListDataValidations(context.Context, string) ([]DataValidation, error)
	GetDataValidation(context.Context, string) (DataValidation, error)
	UpdateDataValidation(context.Context, string, string, UpdateDataValidationInput) (DataValidation, error)
	DeleteDataValidation(context.Context, string, string, *int64) error
	CreateProtectedRange(context.Context, string, string, CreateProtectedRangeInput) (ProtectedRange, error)
	ListProtectedRanges(context.Context, string) ([]ProtectedRange, error)
	UpdateProtectedRange(context.Context, string, string, UpdateProtectedRangeInput) (ProtectedRange, error)
	DeleteProtectedRange(context.Context, string) error
	CreateConditionalFormat(context.Context, string, string, CreateConditionalFormatInput) (ConditionalFormat, error)
	ListConditionalFormats(context.Context, string) ([]ConditionalFormat, error)
	GetConditionalFormat(context.Context, string) (ConditionalFormat, error)
	EvaluateConditionalFormats(context.Context, string, cellrange.Range) (ConditionalFormatEvaluation, error)
	UpdateConditionalFormat(context.Context, string, string, UpdateConditionalFormatInput) (ConditionalFormat, error)
	DeleteConditionalFormat(context.Context, string, string, *int64) error
	CreateCommentThread(context.Context, string, string, CreateCommentThreadInput) (CommentThread, error)
	ListCommentThreads(context.Context, string, string, bool) ([]CommentThread, error)
	GetCommentThread(context.Context, string) (CommentThread, error)
	AddCommentReply(context.Context, string, string, CreateCommentReplyInput) (CommentThread, error)
	UpdateCommentMessage(context.Context, string, string, UpdateCommentMessageInput) (CommentThread, error)
	DeleteCommentMessage(context.Context, string, string, int64) (CommentThread, error)
	UpdateCommentThread(context.Context, string, string, UpdateCommentThreadInput) (CommentThread, error)
	DeleteCommentThread(context.Context, string, string) error
	ListMentionNotifications(context.Context, []string, bool, int) ([]MentionNotification, error)
	MarkMentionNotificationRead(context.Context, string, []string) (MentionNotification, error)
	CreateChart(context.Context, string, string, CreateChartInput) (Chart, error)
	ListCharts(context.Context, string, string) ([]Chart, error)
	GetChart(context.Context, string) (Chart, error)
	GetChartData(context.Context, string) (ChartData, error)
	UpdateChart(context.Context, string, string, UpdateChartInput) (Chart, error)
	DeleteChart(context.Context, string, string, *int64) error
	CreatePivot(context.Context, string, string, CreatePivotInput) (Pivot, error)
	ListPivots(context.Context, string, string) ([]Pivot, error)
	GetPivot(context.Context, string) (Pivot, error)
	GetPivotData(context.Context, string) (PivotData, error)
	RefreshPivot(context.Context, string, string) (PivotData, error)
	PivotDrilldown(context.Context, string, PivotDrilldownInput) (PivotDrilldownResult, error)
	UpdatePivot(context.Context, string, string, UpdatePivotInput) (Pivot, error)
	DeletePivot(context.Context, string, string, *int64) error
	CreateWatchRule(context.Context, string, string, CreateWatchRuleInput) (WatchRule, error)
	ListWatchRules(context.Context, string, string) ([]WatchRule, error)
	SheetWatchRules(context.Context, string) ([]WatchRule, error)
	GetWatchRule(context.Context, string) (WatchRule, error)
	UpdateWatchRule(context.Context, string, string, UpdateWatchRuleInput) (WatchRule, error)
	DeleteWatchRule(context.Context, string, string, *int64) error

	// 이름을 가진 표. =SUM(매출표[금액]) 이 가리키는 것이다.
	CreateSheetTable(context.Context, string, string, CreateSheetTableInput) (SheetTable, error)
	ListSheetTables(context.Context, string) ([]SheetTable, error)
	GetSheetTable(context.Context, string) (SheetTable, error)
	UpdateSheetTable(context.Context, string, string, UpdateSheetTableInput) (SheetTable, error)
	DeleteSheetTable(context.Context, string, string, *int64) error

	// 가정 한 벌에 붙인 이름. 회의에서 두 안을 나란히 놓고 보는 그것이다.
	CreateScenario(context.Context, string, string, CreateScenarioInput) (Scenario, error)
	ListScenarios(context.Context, string) ([]Scenario, error)
	GetScenario(context.Context, string) (Scenario, error)
	UpdateScenario(context.Context, string, string, UpdateScenarioInput) (Scenario, error)
	DeleteScenario(context.Context, string, string, *int64) error
	CreateNamedFunction(context.Context, string, string, CreateNamedFunctionInput) (NamedFunction, error)
	ListNamedFunctions(context.Context, string) ([]NamedFunction, error)
	GetNamedFunction(context.Context, string) (NamedFunction, error)
	UpdateNamedFunction(context.Context, string, string, UpdateNamedFunctionInput) (NamedFunction, error)
	DeleteNamedFunction(context.Context, string, string, *int64) error
	CreateNamedRange(context.Context, string, string, CreateNamedRangeInput) (NamedRange, error)
	ListNamedRanges(context.Context, string) ([]NamedRange, error)
	GetNamedRange(context.Context, string) (NamedRange, error)
	UpdateNamedRange(context.Context, string, string, UpdateNamedRangeInput) (NamedRange, error)
	DeleteNamedRange(context.Context, string, string, *int64) error
	ApplyStructure(context.Context, StructuralMutation) (MutationResult, error)
	ApplySheetLayout(context.Context, SheetLayoutMutation) (SheetLayoutResult, error)

	ApplyCells(context.Context, CellMutation) (MutationResult, error)
	UndoOperation(context.Context, UndoOperationInput) (MutationResult, error)
	ListCellConflicts(context.Context, string, bool) ([]CellConflict, error)
	GetCellConflict(context.Context, string) (CellConflict, error)
	ResolveCellConflict(context.Context, string, ResolveCellConflictInput) (CellConflictResolutionResult, error)
	ReadRange(context.Context, string, cellrange.Range) ([]Cell, error)
	SearchWorkbook(context.Context, string, SearchWorkbookInput) (WorkbookSearchResult, error)

	CreateVersion(context.Context, string, string, string) (Version, error)
	ListVersions(context.Context, string) ([]Version, error)
	RestoreVersion(context.Context, string, string) (MutationResult, error)

	ImportWorkbook(context.Context, ImportWorkbookInput) (Workbook, error)
	ReadAllCells(context.Context, string) ([]Cell, error)
}
