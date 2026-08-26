package workbook

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// ShareRole is the Google Sheets style role a principal holds on a workbook.
// Owner is never assignable through a share row: it always follows
// workbooks.owner_id and is changed by transferring ownership.
type ShareRole string

const (
	RoleNone      ShareRole = ""
	RoleViewer    ShareRole = "viewer"
	RoleCommenter ShareRole = "commenter"
	RoleEditor    ShareRole = "editor"
	RoleOwner     ShareRole = "owner"
)

// Capability is the minimum role a request needs. Endpoints declare a
// capability so a single authorization layer can decide every route.
type Capability string

const (
	CapabilityRead    Capability = "read"
	CapabilityComment Capability = "comment"
	CapabilityWrite   Capability = "write"
	CapabilityManage  Capability = "manage"
)

// LinkAccess describes who reaches a workbook without an explicit share.
const (
	LinkAccessRestricted   = "restricted"
	LinkAccessOrganization = "organization"
	LinkAccessAnyone       = "anyone"
)

const (
	PrincipalUser       = "user"
	PrincipalDepartment = "department"
	PrincipalRole       = "role"

	AccessSourceOwner      = "owner"
	AccessSourceAdmin      = "admin"
	AccessSourceUser       = "user"
	AccessSourceDepartment = "department"
	AccessSourceRole       = "role"
	AccessSourceLink       = "link"

	AccessRequestPending  = "pending"
	AccessRequestApproved = "approved"
	AccessRequestDenied   = "denied"

	MaxDepartmentNameRunes        = 80
	MaxDepartmentDescriptionRunes = 300
	MaxDepartmentDepth            = 8
	MaxDepartmentMembersPerCall   = 200
	MaxWorkbookShares             = 500
	MaxAccessRequestMessageRunes  = 500
	MaxPrincipalIDRunes           = 320
)

func roleRank(role ShareRole) int {
	switch role {
	case RoleViewer:
		return 1
	case RoleCommenter:
		return 2
	case RoleEditor:
		return 3
	case RoleOwner:
		return 4
	default:
		return 0
	}
}

// Allows reports whether the role satisfies a capability. Only owners manage
// sharing, and every role above viewer inherits the lower capabilities.
func (role ShareRole) Allows(capability Capability) bool {
	switch capability {
	case CapabilityRead:
		return roleRank(role) >= roleRank(RoleViewer)
	case CapabilityComment:
		return roleRank(role) >= roleRank(RoleCommenter)
	case CapabilityWrite:
		return roleRank(role) >= roleRank(RoleEditor)
	case CapabilityManage:
		return roleRank(role) >= roleRank(RoleOwner)
	default:
		return false
	}
}

// AssignableShareRole rejects owner and unknown values so a share row can never
// grant management rights.
func AssignableShareRole(role ShareRole) bool {
	return role == RoleViewer || role == RoleCommenter || role == RoleEditor
}

func ValidLinkAccess(value string) bool {
	return value == LinkAccessRestricted || value == LinkAccessOrganization || value == LinkAccessAnyone
}

func ValidPrincipalType(value string) bool {
	return value == PrincipalUser || value == PrincipalDepartment || value == PrincipalRole
}

// AccessPrincipal is the identity an authorization decision is made for. It is
// assembled from the session, the API key principal or the development actor
// header by the API layer.
type AccessPrincipal struct {
	UserID        string   `json:"user_id"`
	Email         string   `json:"email,omitempty"`
	Roles         []string `json:"roles,omitempty"`
	Admin         bool     `json:"admin,omitempty"`
	Authenticated bool     `json:"authenticated,omitempty"`
}

func (principal AccessPrincipal) identities() []string {
	values := make([]string, 0, 2)
	if id := strings.TrimSpace(principal.UserID); id != "" {
		values = append(values, strings.ToLower(id))
	}
	if email := strings.TrimSpace(principal.Email); email != "" {
		lowered := strings.ToLower(email)
		if len(values) == 0 || values[0] != lowered {
			values = append(values, lowered)
		}
	}
	return values
}

func (principal AccessPrincipal) roleSet() []string {
	values := make([]string, 0, len(principal.Roles))
	for _, role := range principal.Roles {
		if trimmed := strings.TrimSpace(role); trimmed != "" {
			values = append(values, strings.ToLower(trimmed))
		}
	}
	return values
}

// WorkbookAccess is the effective decision for one principal and workbook.
type WorkbookAccess struct {
	WorkbookID  string    `json:"workbook_id"`
	ActorID     string    `json:"actor_id"`
	Role        ShareRole `json:"role"`
	Source      string    `json:"source,omitempty"`
	SourceLabel string    `json:"source_label,omitempty"`
	CanRead     bool      `json:"can_read"`
	CanComment  bool      `json:"can_comment"`
	CanWrite    bool      `json:"can_write"`
	CanManage   bool      `json:"can_manage"`
	CanCopy     bool      `json:"can_copy"`
	LinkAccess  string    `json:"link_access"`
	LinkRole    ShareRole `json:"link_role"`
	OwnerID     string    `json:"owner_id"`
}

// newWorkbookAccess derives the capability flags from the resolved role so
// callers never recompute them inconsistently.
func newWorkbookAccess(workbookID string, principal AccessPrincipal, sharing WorkbookSharing, role ShareRole, source, label string) WorkbookAccess {
	access := WorkbookAccess{
		WorkbookID: workbookID, ActorID: principal.UserID, Role: role, Source: source, SourceLabel: label,
		LinkAccess: sharing.LinkAccess, LinkRole: sharing.LinkRole, OwnerID: sharing.OwnerID,
	}
	if role == RoleNone {
		return access
	}
	access.CanRead = role.Allows(CapabilityRead)
	access.CanComment = role.Allows(CapabilityComment)
	access.CanWrite = role.Allows(CapabilityWrite)
	access.CanManage = role.Allows(CapabilityManage) || (!sharing.SharingLocked && role == RoleEditor)
	// Owners and editors always keep a copy; viewers and commenters follow the
	// workbook policy, matching the Sheets download and copy restriction.
	access.CanCopy = role.Allows(CapabilityWrite) || sharing.ViewerCanCopy
	return access
}

// WorkbookSharing is the sharing state stored on the workbook itself.
type WorkbookSharing struct {
	WorkbookID    string          `json:"workbook_id"`
	OwnerID       string          `json:"owner_id"`
	LinkAccess    string          `json:"link_access"`
	LinkRole      ShareRole       `json:"link_role"`
	SharingLocked bool            `json:"sharing_locked"`
	ViewerCanCopy bool            `json:"viewer_can_copy"`
	Shares        []WorkbookShare `json:"shares"`
}

type WorkbookShare struct {
	ID             string    `json:"id"`
	WorkbookID     string    `json:"workbook_id"`
	PrincipalType  string    `json:"principal_type"`
	PrincipalID    string    `json:"principal_id"`
	PrincipalLabel string    `json:"principal_label,omitempty"`
	Role           ShareRole `json:"role"`
	Revision       int64     `json:"revision"`
	CreatedBy      string    `json:"created_by,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type ShareInput struct {
	PrincipalType  string    `json:"principal_type"`
	PrincipalID    string    `json:"principal_id"`
	PrincipalLabel string    `json:"principal_label,omitempty"`
	Role           ShareRole `json:"role"`
	ActorID        string    `json:"-"`
}

type UpdateSharingInput struct {
	LinkAccess    *string    `json:"link_access,omitempty"`
	LinkRole      *ShareRole `json:"link_role,omitempty"`
	SharingLocked *bool      `json:"sharing_locked,omitempty"`
	ViewerCanCopy *bool      `json:"viewer_can_copy,omitempty"`
	ActorID       string     `json:"-"`
}

type TransferOwnershipInput struct {
	NewOwnerID    string `json:"new_owner_id"`
	KeepAsEditor  bool   `json:"keep_as_editor,omitempty"`
	ActorID       string `json:"-"`
	PreviousOwner string `json:"-"`
}

type Department struct {
	ID          string   `json:"id"`
	ParentID    string   `json:"parent_id,omitempty"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Path        string   `json:"path,omitempty"`
	Depth       int      `json:"depth"`
	MemberCount int      `json:"member_count"`
	Members     []string `json:"members,omitempty"`
	// Managers 는 이 부서를 맡은 사람들이다. 자기 부서와 그 아래 부서의
	// 구성원 계정만 다룰 수 있고, 워크북 소유권과 설정에는 손대지 못한다.
	Managers  []string  `json:"managers,omitempty"`
	Revision  int64     `json:"revision"`
	CreatedBy string    `json:"created_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type CreateDepartmentInput struct {
	Name        string `json:"name"`
	ParentID    string `json:"parent_id,omitempty"`
	Description string `json:"description,omitempty"`
	ActorID     string `json:"-"`
}

type UpdateDepartmentInput struct {
	Name             *string `json:"name,omitempty"`
	ParentID         *string `json:"parent_id,omitempty"`
	Description      *string `json:"description,omitempty"`
	ExpectedRevision int64   `json:"expected_revision"`
	ActorID          string  `json:"-"`
}

type DepartmentMembersInput struct {
	UserIDs []string `json:"user_ids"`
	ActorID string   `json:"-"`
}

type AccessRequest struct {
	ID            string     `json:"id"`
	WorkbookID    string     `json:"workbook_id"`
	WorkbookTitle string     `json:"workbook_title,omitempty"`
	RequesterID   string     `json:"requester_id"`
	RequesterMail string     `json:"requester_email,omitempty"`
	RequesterName string     `json:"requester_name,omitempty"`
	RequestedRole ShareRole  `json:"requested_role"`
	Message       string     `json:"message,omitempty"`
	Status        string     `json:"status"`
	DecidedBy     string     `json:"decided_by,omitempty"`
	DecidedAt     *time.Time `json:"decided_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

type CreateAccessRequestInput struct {
	RequestedRole ShareRole `json:"requested_role"`
	Message       string    `json:"message,omitempty"`
	RequesterID   string    `json:"-"`
	RequesterMail string    `json:"-"`
	RequesterName string    `json:"-"`
}

type DecideAccessRequestInput struct {
	Approve bool      `json:"approve"`
	Role    ShareRole `json:"role,omitempty"`
	ActorID string    `json:"-"`
}

func validateShareInput(input ShareInput) (ShareInput, error) {
	input.PrincipalType = strings.TrimSpace(strings.ToLower(input.PrincipalType))
	input.PrincipalID = strings.TrimSpace(input.PrincipalID)
	input.PrincipalLabel = strings.TrimSpace(input.PrincipalLabel)
	if !ValidPrincipalType(input.PrincipalType) {
		return ShareInput{}, fmt.Errorf("%w: principal_type must be user, department or role", ErrInvalid)
	}
	if input.PrincipalID == "" || utf8.RuneCountInString(input.PrincipalID) > MaxPrincipalIDRunes {
		return ShareInput{}, fmt.Errorf("%w: principal_id must contain 1 to %d characters", ErrInvalid, MaxPrincipalIDRunes)
	}
	if !AssignableShareRole(input.Role) {
		return ShareInput{}, fmt.Errorf("%w: role must be viewer, commenter or editor", ErrInvalid)
	}
	if utf8.RuneCountInString(input.PrincipalLabel) > MaxDepartmentNameRunes {
		return ShareInput{}, fmt.Errorf("%w: principal_label exceeds %d characters", ErrInvalid, MaxDepartmentNameRunes)
	}
	return input, nil
}

func validateSharingInput(input UpdateSharingInput) (UpdateSharingInput, error) {
	if input.LinkAccess != nil {
		value := strings.TrimSpace(strings.ToLower(*input.LinkAccess))
		if !ValidLinkAccess(value) {
			return UpdateSharingInput{}, fmt.Errorf("%w: link_access must be restricted, organization or anyone", ErrInvalid)
		}
		input.LinkAccess = &value
	}
	if input.LinkRole != nil && !AssignableShareRole(*input.LinkRole) {
		return UpdateSharingInput{}, fmt.Errorf("%w: link_role must be viewer, commenter or editor", ErrInvalid)
	}
	if input.LinkAccess == nil && input.LinkRole == nil && input.SharingLocked == nil && input.ViewerCanCopy == nil {
		return UpdateSharingInput{}, fmt.Errorf("%w: no sharing changes were provided", ErrInvalid)
	}
	return input, nil
}

func validateDepartmentName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" || utf8.RuneCountInString(trimmed) > MaxDepartmentNameRunes {
		return "", fmt.Errorf("%w: name must contain 1 to %d characters", ErrInvalid, MaxDepartmentNameRunes)
	}
	return trimmed, nil
}

func validateDepartmentDescription(description string) (string, error) {
	trimmed := strings.TrimSpace(description)
	if utf8.RuneCountInString(trimmed) > MaxDepartmentDescriptionRunes {
		return "", fmt.Errorf("%w: description exceeds %d characters", ErrInvalid, MaxDepartmentDescriptionRunes)
	}
	return trimmed, nil
}

func normalizeMemberIDs(input DepartmentMembersInput) ([]string, error) {
	seen := make(map[string]struct{}, len(input.UserIDs))
	members := make([]string, 0, len(input.UserIDs))
	for _, candidate := range input.UserIDs {
		trimmed := strings.TrimSpace(candidate)
		if trimmed == "" {
			continue
		}
		if utf8.RuneCountInString(trimmed) > MaxPrincipalIDRunes {
			return nil, fmt.Errorf("%w: user_id exceeds %d characters", ErrInvalid, MaxPrincipalIDRunes)
		}
		key := strings.ToLower(trimmed)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		members = append(members, trimmed)
	}
	if len(members) == 0 || len(members) > MaxDepartmentMembersPerCall {
		return nil, fmt.Errorf("%w: user_ids must contain 1 to %d entries", ErrInvalid, MaxDepartmentMembersPerCall)
	}
	return members, nil
}

func validateAccessRequestInput(input CreateAccessRequestInput) (CreateAccessRequestInput, error) {
	if input.RequestedRole == RoleNone {
		input.RequestedRole = RoleViewer
	}
	if !AssignableShareRole(input.RequestedRole) {
		return CreateAccessRequestInput{}, fmt.Errorf("%w: requested_role must be viewer, commenter or editor", ErrInvalid)
	}
	input.Message = strings.TrimSpace(input.Message)
	if utf8.RuneCountInString(input.Message) > MaxAccessRequestMessageRunes {
		return CreateAccessRequestInput{}, fmt.Errorf("%w: message exceeds %d characters", ErrInvalid, MaxAccessRequestMessageRunes)
	}
	if strings.TrimSpace(input.RequesterID) == "" {
		return CreateAccessRequestInput{}, fmt.Errorf("%w: an authenticated requester is required", ErrInvalid)
	}
	return input, nil
}

// resolveAccess combines every grant that applies to a principal and keeps the
// strongest one. Sharing with a parent department also covers its descendants,
// which the repository expresses by listing the principal's department closure.
func resolveAccess(workbookID string, principal AccessPrincipal, sharing WorkbookSharing, departmentIDs map[string]string) WorkbookAccess {
	identities := principal.identities()
	for _, identity := range identities {
		if identity == strings.ToLower(strings.TrimSpace(sharing.OwnerID)) && identity != "" {
			return newWorkbookAccess(workbookID, principal, sharing, RoleOwner, AccessSourceOwner, "")
		}
	}
	if principal.Admin {
		return newWorkbookAccess(workbookID, principal, sharing, RoleOwner, AccessSourceAdmin, "")
	}
	role, source, label := RoleNone, "", ""
	promote := func(candidate ShareRole, candidateSource, candidateLabel string) {
		if roleRank(candidate) > roleRank(role) {
			role, source, label = candidate, candidateSource, candidateLabel
		}
	}
	roles := principal.roleSet()
	for _, share := range sharing.Shares {
		principalID := strings.ToLower(strings.TrimSpace(share.PrincipalID))
		switch share.PrincipalType {
		case PrincipalUser:
			for _, identity := range identities {
				if identity == principalID {
					promote(share.Role, AccessSourceUser, share.PrincipalLabel)
				}
			}
		case PrincipalDepartment:
			if name, ok := departmentIDs[principalID]; ok {
				if name == "" {
					name = share.PrincipalLabel
				}
				promote(share.Role, AccessSourceDepartment, name)
			}
		case PrincipalRole:
			for _, candidate := range roles {
				if candidate == principalID {
					promote(share.Role, AccessSourceRole, share.PrincipalID)
				}
			}
		}
	}
	// Link access covers everyone who reaches the workbook in this deployment.
	// kanpic runs inside a closed network behind authentication, so both
	// organization and anyone resolve to the signed-in actor.
	if sharing.LinkAccess == LinkAccessOrganization || sharing.LinkAccess == LinkAccessAnyone {
		if principal.Authenticated || strings.TrimSpace(principal.UserID) != "" {
			promote(sharing.LinkRole, AccessSourceLink, sharing.LinkAccess)
		}
	}
	return newWorkbookAccess(workbookID, principal, sharing, role, source, label)
}

// sharingFromWorkbook builds the sharing state for an already loaded workbook
// row so listings resolve access without another round trip.
func sharingFromWorkbook(wb Workbook, shares []WorkbookShare) WorkbookSharing {
	sharing := WorkbookSharing{
		WorkbookID: wb.ID, OwnerID: wb.OwnerID, LinkAccess: wb.LinkAccess, LinkRole: wb.LinkRole,
		SharingLocked: wb.SharingLocked, ViewerCanCopy: wb.ViewerCanCopy, Shares: shares,
	}
	if sharing.LinkAccess == "" {
		sharing.LinkAccess = LinkAccessRestricted
	}
	if sharing.LinkRole == RoleNone {
		sharing.LinkRole = RoleViewer
	}
	return sharing
}
