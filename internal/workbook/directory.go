package workbook

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	UserStatusActive    = "active"
	UserStatusSuspended = "suspended"

	MaxDisplayNameRunes = 120
	MaxUserNoteRunes    = 300
	MaxRoleNameRunes    = 80
	MaxRolesPerUser     = 50
)

// DirectoryUser is what an administrator manages: identity comes from the
// identity provider, while status, kanpic roles and notes live here.
type DirectoryUser struct {
	UserID      string     `json:"user_id"`
	DisplayName string     `json:"display_name,omitempty"`
	Email       string     `json:"email,omitempty"`
	Status      string     `json:"status"`
	Note        string     `json:"note,omitempty"`
	Roles       []string   `json:"roles"`
	Departments []string   `json:"departments,omitempty"`
	OwnedBooks  int        `json:"owned_workbooks"`
	CreatedBy   string     `json:"created_by,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	LastSeenAt  *time.Time `json:"last_seen_at,omitempty"`
}

// UserAccessProfile is the per-request slice of the directory: whether the
// account may act at all and which kanpic roles it carries.
type UserAccessProfile struct {
	UserID    string   `json:"user_id"`
	Status    string   `json:"status"`
	Roles     []string `json:"roles"`
	Suspended bool     `json:"suspended"`
	Known     bool     `json:"known"`
}

// UserSummary is the public part of a directory entry: enough to recognise a
// colleague in a comment, a mention or a presence cursor.
type UserSummary struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name,omitempty"`
	Email       string `json:"email,omitempty"`
}

const MaxUserLookupIDs = 200

type UpsertUserInput struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name,omitempty"`
	Email       string `json:"email,omitempty"`
	Note        string `json:"note,omitempty"`
	ActorID     string `json:"-"`
}

type UpdateUserInput struct {
	DisplayName *string `json:"display_name,omitempty"`
	Email       *string `json:"email,omitempty"`
	Note        *string `json:"note,omitempty"`
	Status      *string `json:"status,omitempty"`
	ActorID     string  `json:"-"`
}

func normalizeUserID(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || utf8.RuneCountInString(trimmed) > MaxPrincipalIDRunes {
		return "", fmt.Errorf("%w: user_id must contain 1 to %d characters", ErrInvalid, MaxPrincipalIDRunes)
	}
	return trimmed, nil
}

func normalizeRoleName(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || utf8.RuneCountInString(trimmed) > MaxRoleNameRunes {
		return "", fmt.Errorf("%w: role must contain 1 to %d characters", ErrInvalid, MaxRoleNameRunes)
	}
	if strings.ContainsAny(trimmed, " \t\n") {
		return "", fmt.Errorf("%w: role must not contain whitespace", ErrInvalid)
	}
	return trimmed, nil
}

func validateUpsertUser(input UpsertUserInput) (UpsertUserInput, error) {
	userID, err := normalizeUserID(input.UserID)
	if err != nil {
		return UpsertUserInput{}, err
	}
	input.UserID = userID
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.Email = strings.TrimSpace(input.Email)
	input.Note = strings.TrimSpace(input.Note)
	if utf8.RuneCountInString(input.DisplayName) > MaxDisplayNameRunes {
		return UpsertUserInput{}, fmt.Errorf("%w: display_name exceeds %d characters", ErrInvalid, MaxDisplayNameRunes)
	}
	if utf8.RuneCountInString(input.Email) > MaxPrincipalIDRunes {
		return UpsertUserInput{}, fmt.Errorf("%w: email exceeds %d characters", ErrInvalid, MaxPrincipalIDRunes)
	}
	if utf8.RuneCountInString(input.Note) > MaxUserNoteRunes {
		return UpsertUserInput{}, fmt.Errorf("%w: note exceeds %d characters", ErrInvalid, MaxUserNoteRunes)
	}
	return input, nil
}

func validateUpdateUser(input UpdateUserInput) (UpdateUserInput, error) {
	if input.Status != nil {
		status := strings.TrimSpace(strings.ToLower(*input.Status))
		if status != UserStatusActive && status != UserStatusSuspended {
			return UpdateUserInput{}, fmt.Errorf("%w: status must be active or suspended", ErrInvalid)
		}
		input.Status = &status
	}
	if input.DisplayName != nil {
		trimmed := strings.TrimSpace(*input.DisplayName)
		if utf8.RuneCountInString(trimmed) > MaxDisplayNameRunes {
			return UpdateUserInput{}, fmt.Errorf("%w: display_name exceeds %d characters", ErrInvalid, MaxDisplayNameRunes)
		}
		input.DisplayName = &trimmed
	}
	if input.Email != nil {
		trimmed := strings.TrimSpace(*input.Email)
		if utf8.RuneCountInString(trimmed) > MaxPrincipalIDRunes {
			return UpdateUserInput{}, fmt.Errorf("%w: email exceeds %d characters", ErrInvalid, MaxPrincipalIDRunes)
		}
		input.Email = &trimmed
	}
	if input.Note != nil {
		trimmed := strings.TrimSpace(*input.Note)
		if utf8.RuneCountInString(trimmed) > MaxUserNoteRunes {
			return UpdateUserInput{}, fmt.Errorf("%w: note exceeds %d characters", ErrInvalid, MaxUserNoteRunes)
		}
		input.Note = &trimmed
	}
	if input.DisplayName == nil && input.Email == nil && input.Note == nil && input.Status == nil {
		return UpdateUserInput{}, fmt.Errorf("%w: no user changes were provided", ErrInvalid)
	}
	return input, nil
}

// AdminOverview is the console landing summary: how many people and workbooks
// exist and where the sharing risks are.
type AdminOverview struct {
	Users              int `json:"users"`
	ActiveUsers        int `json:"active_users"`
	SuspendedUsers     int `json:"suspended_users"`
	Departments        int `json:"departments"`
	Workbooks          int `json:"workbooks"`
	TrashedWorkbooks   int `json:"trashed_workbooks"`
	SharedWorkbooks    int `json:"shared_workbooks"`
	OrganizationShared int `json:"organization_shared"`
	AnyoneShared       int `json:"anyone_shared"`
	OrphanWorkbooks    int `json:"orphan_workbooks"`
	// 잠든 것들. 무엇이 잠들어 있는지 알아야 정리할지 남길지 정할 수 있다.
	DormantWorkbooks int `json:"dormant_workbooks"`
	DormantUsers     int `json:"dormant_users"`
	PendingRequests  int `json:"pending_access_requests"`
	Shares           int `json:"shares"`
}

// GovernedWorkbook is one row of the administrator workbook list: who owns it,
// how widely it is shared and whether the owner is still active.
type GovernedWorkbook struct {
	ID            string     `json:"id"`
	Title         string     `json:"title"`
	OwnerID       string     `json:"owner_id"`
	OwnerName     string     `json:"owner_name,omitempty"`
	OwnerStatus   string     `json:"owner_status,omitempty"`
	LinkAccess    string     `json:"link_access"`
	LinkRole      ShareRole  `json:"link_role"`
	ShareCount    int        `json:"share_count"`
	SheetCount    int        `json:"sheet_count"`
	Version       int64      `json:"version"`
	UpdatedAt     time.Time  `json:"updated_at"`
	DeletedAt     *time.Time `json:"deleted_at,omitempty"`
	PendingAccess int        `json:"pending_access_requests"`
}

// WorkbookGovernanceFilter selects which rows an administrator wants to see.
const (
	GovernanceFilterAll          = "all"
	GovernanceFilterOrganization = "organization"
	GovernanceFilterAnyone       = "anyone"
	GovernanceFilterOrphan       = "orphan"
	GovernanceFilterTrashed      = "trashed"
	// GovernanceFilterDormant 는 오래 손대지 않은 워크북이다. 무엇이 잠들어
	// 있는지 알아야 정리할지 남길지 정할 수 있다.
	GovernanceFilterDormant = "dormant"
)

// DormantWorkbookDays 는 "오래 손대지 않았다" 고 볼 날 수다. 한 해를 넘기면
// 그 해의 자료는 이미 마감된 것이 보통이다.
const DormantWorkbookDays = 365

// DormantUserDays 는 "오래 들어오지 않았다" 고 볼 날 수다. 석 달이면 분기
// 하나가 통째로 지난 것이다.
const DormantUserDays = 90

func ValidGovernanceFilter(value string) bool {
	switch value {
	case "", GovernanceFilterAll, GovernanceFilterOrganization, GovernanceFilterAnyone, GovernanceFilterOrphan, GovernanceFilterTrashed, GovernanceFilterDormant:
		return true
	default:
		return false
	}
}
