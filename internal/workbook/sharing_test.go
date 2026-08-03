package workbook

import (
	"context"
	"errors"
	"testing"
)

func TestShareRoleCapabilities(t *testing.T) {
	t.Parallel()
	cases := []struct {
		role       ShareRole
		capability Capability
		allowed    bool
	}{
		{RoleNone, CapabilityRead, false},
		{RoleViewer, CapabilityRead, true},
		{RoleViewer, CapabilityComment, false},
		{RoleCommenter, CapabilityComment, true},
		{RoleCommenter, CapabilityWrite, false},
		{RoleEditor, CapabilityWrite, true},
		{RoleEditor, CapabilityManage, false},
		{RoleOwner, CapabilityManage, true},
	}
	for _, testCase := range cases {
		if got := testCase.role.Allows(testCase.capability); got != testCase.allowed {
			t.Fatalf("%s allows %s = %v", testCase.role, testCase.capability, got)
		}
	}
	if AssignableShareRole(RoleOwner) || !AssignableShareRole(RoleEditor) {
		t.Fatal("owner must not be assignable through a share")
	}
}

func TestResolveAccessCombinesEveryGrant(t *testing.T) {
	t.Parallel()
	sharing := WorkbookSharing{
		OwnerID: "owner", LinkAccess: LinkAccessRestricted, LinkRole: RoleViewer, ViewerCanCopy: true,
		Shares: []WorkbookShare{
			{PrincipalType: PrincipalUser, PrincipalID: "Viewer@corp.example", Role: RoleViewer},
			{PrincipalType: PrincipalDepartment, PrincipalID: "dept-finance", Role: RoleCommenter},
			{PrincipalType: PrincipalRole, PrincipalID: "kanpic-editor", Role: RoleEditor},
		},
	}
	closure := map[string]string{"dept-finance": "재무팀"}

	owner := resolveAccess("book", AccessPrincipal{UserID: "owner", Authenticated: true}, sharing, nil)
	if owner.Role != RoleOwner || owner.Source != AccessSourceOwner || !owner.CanManage {
		t.Fatalf("owner access = %#v", owner)
	}
	admin := resolveAccess("book", AccessPrincipal{UserID: "root", Admin: true, Authenticated: true}, sharing, nil)
	if admin.Role != RoleOwner || admin.Source != AccessSourceAdmin {
		t.Fatalf("admin access = %#v", admin)
	}
	// A user share matches the email as well as the identifier.
	byEmail := resolveAccess("book", AccessPrincipal{UserID: "u-1", Email: "viewer@corp.example", Authenticated: true}, sharing, nil)
	if byEmail.Role != RoleViewer || byEmail.Source != AccessSourceUser || byEmail.CanComment {
		t.Fatalf("email share access = %#v", byEmail)
	}
	department := resolveAccess("book", AccessPrincipal{UserID: "u-2", Authenticated: true}, sharing, closure)
	if department.Role != RoleCommenter || department.Source != AccessSourceDepartment || department.SourceLabel != "재무팀" {
		t.Fatalf("department access = %#v", department)
	}
	byRole := resolveAccess("book", AccessPrincipal{UserID: "u-3", Roles: []string{"kanpic-editor"}, Authenticated: true}, sharing, nil)
	if byRole.Role != RoleEditor || byRole.Source != AccessSourceRole || !byRole.CanWrite {
		t.Fatalf("role access = %#v", byRole)
	}
	// The strongest grant wins when several apply.
	strongest := resolveAccess("book", AccessPrincipal{UserID: "u-4", Email: "viewer@corp.example", Roles: []string{"kanpic-editor"}, Authenticated: true}, sharing, closure)
	if strongest.Role != RoleEditor {
		t.Fatalf("combined access = %#v", strongest)
	}
	stranger := resolveAccess("book", AccessPrincipal{UserID: "outsider", Authenticated: true}, sharing, nil)
	if stranger.Role != RoleNone || stranger.CanRead {
		t.Fatalf("stranger access = %#v", stranger)
	}
	sharing.LinkAccess, sharing.LinkRole = LinkAccessOrganization, RoleCommenter
	link := resolveAccess("book", AccessPrincipal{UserID: "outsider", Authenticated: true}, sharing, nil)
	if link.Role != RoleCommenter || link.Source != AccessSourceLink {
		t.Fatalf("link access = %#v", link)
	}
}

func TestResolveAccessRespectsSharingAndCopyPolicies(t *testing.T) {
	t.Parallel()
	sharing := WorkbookSharing{OwnerID: "owner", LinkAccess: LinkAccessRestricted, LinkRole: RoleViewer, ViewerCanCopy: false,
		Shares: []WorkbookShare{
			{PrincipalType: PrincipalUser, PrincipalID: "editor", Role: RoleEditor},
			{PrincipalType: PrincipalUser, PrincipalID: "reader", Role: RoleViewer},
		}}
	editor := resolveAccess("book", AccessPrincipal{UserID: "editor", Authenticated: true}, sharing, nil)
	if !editor.CanManage || !editor.CanCopy {
		t.Fatalf("editors share by default: %#v", editor)
	}
	reader := resolveAccess("book", AccessPrincipal{UserID: "reader", Authenticated: true}, sharing, nil)
	if reader.CanCopy || reader.CanManage {
		t.Fatalf("viewer copy policy: %#v", reader)
	}
	sharing.SharingLocked = true
	locked := resolveAccess("book", AccessPrincipal{UserID: "editor", Authenticated: true}, sharing, nil)
	if locked.CanManage {
		t.Fatalf("locked sharing must exclude editors: %#v", locked)
	}
}

func seedSharedWorkbook(t *testing.T, repository *MemoryRepository) (string, string) {
	t.Helper()
	ctx := context.Background()
	book, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "공유", OwnerID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	return book.ID, book.Sheets[0].ID
}

func TestMemorySharingLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	workbookID, _ := seedSharedWorkbook(t, repository)

	if _, err := repository.PutWorkbookShare(ctx, workbookID, ShareInput{PrincipalType: PrincipalUser, PrincipalID: "owner", Role: RoleEditor}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("sharing with the owner error = %v", err)
	}
	share, err := repository.PutWorkbookShare(ctx, workbookID, ShareInput{PrincipalType: PrincipalUser, PrincipalID: "alice", Role: RoleViewer, ActorID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	upgraded, err := repository.PutWorkbookShare(ctx, workbookID, ShareInput{PrincipalType: PrincipalUser, PrincipalID: "ALICE", Role: RoleEditor, ActorID: "owner"})
	if err != nil || upgraded.ID != share.ID || upgraded.Revision != 2 || upgraded.Role != RoleEditor {
		t.Fatalf("share upsert = %#v, %v", upgraded, err)
	}
	access, err := repository.ResolveWorkbookAccess(ctx, workbookID, AccessPrincipal{UserID: "alice", Authenticated: true})
	if err != nil || !access.CanWrite || access.CanManage != true {
		t.Fatalf("editor access = %#v, %v", access, err)
	}
	if _, err := repository.UpdateWorkbookSharing(ctx, workbookID, UpdateSharingInput{SharingLocked: boolPointer(true)}); err != nil {
		t.Fatal(err)
	}
	access, _ = repository.ResolveWorkbookAccess(ctx, workbookID, AccessPrincipal{UserID: "alice", Authenticated: true})
	if access.CanManage {
		t.Fatalf("locked sharing = %#v", access)
	}
	if err := repository.DeleteWorkbookShare(ctx, workbookID, share.ID); err != nil {
		t.Fatal(err)
	}
	access, _ = repository.ResolveWorkbookAccess(ctx, workbookID, AccessPrincipal{UserID: "alice", Authenticated: true})
	if access.Role != RoleNone {
		t.Fatalf("revoked access = %#v", access)
	}
}

func TestMemoryDepartmentSharingInheritsFromParent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	workbookID, _ := seedSharedWorkbook(t, repository)

	head, err := repository.CreateDepartment(ctx, CreateDepartmentInput{Name: "경영지원본부", ActorID: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	finance, err := repository.CreateDepartment(ctx, CreateDepartmentInput{Name: "재무팀", ParentID: head.ID, ActorID: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if finance.Path != "경영지원본부 / 재무팀" || finance.Depth != 1 {
		t.Fatalf("department path = %#v", finance)
	}
	if _, err := repository.CreateDepartment(ctx, CreateDepartmentInput{Name: "재무팀", ParentID: head.ID}); !errors.Is(err, ErrDuplicateName) {
		t.Fatalf("sibling name error = %v", err)
	}
	if _, err := repository.AddDepartmentMembers(ctx, finance.ID, DepartmentMembersInput{UserIDs: []string{"kim", "lee", "kim"}, ActorID: "admin"}); err != nil {
		t.Fatal(err)
	}
	// Sharing with the parent department reaches the child department members.
	if _, err := repository.PutWorkbookShare(ctx, workbookID, ShareInput{PrincipalType: PrincipalDepartment, PrincipalID: head.ID, PrincipalLabel: head.Name, Role: RoleCommenter, ActorID: "owner"}); err != nil {
		t.Fatal(err)
	}
	access, err := repository.ResolveWorkbookAccess(ctx, workbookID, AccessPrincipal{UserID: "kim", Authenticated: true})
	if err != nil || access.Role != RoleCommenter || access.Source != AccessSourceDepartment {
		t.Fatalf("inherited access = %#v, %v", access, err)
	}
	outsider, _ := repository.ResolveWorkbookAccess(ctx, workbookID, AccessPrincipal{UserID: "park", Authenticated: true})
	if outsider.Role != RoleNone {
		t.Fatalf("outsider access = %#v", outsider)
	}
	if _, err := repository.RemoveDepartmentMember(ctx, finance.ID, "KIM"); err != nil {
		t.Fatal(err)
	}
	removed, _ := repository.ResolveWorkbookAccess(ctx, workbookID, AccessPrincipal{UserID: "kim", Authenticated: true})
	if removed.Role != RoleNone {
		t.Fatalf("removed member access = %#v", removed)
	}
	if err := repository.DeleteDepartment(ctx, head.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("deleting a parent department error = %v", err)
	}
}

func TestMemoryListWorkbooksFiltersByAccess(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	mine, _ := seedSharedWorkbook(t, repository)
	other, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "타인", OwnerID: "stranger"})
	if err != nil {
		t.Fatal(err)
	}
	shared, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "공유받음", OwnerID: "stranger"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.PutWorkbookShare(ctx, shared.ID, ShareInput{PrincipalType: PrincipalUser, PrincipalID: "owner", Role: RoleViewer}); err != nil {
		t.Fatal(err)
	}
	items, err := repository.ListWorkbooks(ctx, "", AccessPrincipal{UserID: "owner", Authenticated: true})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]ShareRole{}
	for _, item := range items {
		seen[item.ID] = item.AccessRole
	}
	if len(items) != 2 || seen[mine] != RoleOwner || seen[shared.ID] != RoleViewer {
		t.Fatalf("accessible workbooks = %#v", seen)
	}
	if _, listed := seen[other.ID]; listed {
		t.Fatal("a restricted workbook must not be listed")
	}
	all, err := repository.ListWorkbooks(ctx, "", AccessPrincipal{UserID: "root", Admin: true, Authenticated: true})
	if err != nil || len(all) != 3 {
		t.Fatalf("administrator listing = %d, %v", len(all), err)
	}
}

func TestMemoryOwnershipTransferAndAccessRequests(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repository := NewMemoryRepository()
	workbookID, _ := seedSharedWorkbook(t, repository)

	request, err := repository.CreateAccessRequest(ctx, workbookID, CreateAccessRequestInput{RequestedRole: RoleEditor, Message: "분석 필요", RequesterID: "alice", RequesterMail: "alice@corp.example"})
	if err != nil || request.Status != AccessRequestPending {
		t.Fatalf("access request = %#v, %v", request, err)
	}
	repeated, err := repository.CreateAccessRequest(ctx, workbookID, CreateAccessRequestInput{RequestedRole: RoleViewer, RequesterID: "ALICE"})
	if err != nil || repeated.ID != request.ID || repeated.RequestedRole != RoleViewer {
		t.Fatalf("repeated request = %#v, %v", repeated, err)
	}
	pending, err := repository.ListAccessRequests(ctx, workbookID, true)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending requests = %#v, %v", pending, err)
	}
	decided, err := repository.DecideAccessRequest(ctx, request.ID, DecideAccessRequestInput{Approve: true, Role: RoleCommenter, ActorID: "owner"})
	if err != nil || decided.Status != AccessRequestApproved || decided.RequestedRole != RoleCommenter {
		t.Fatalf("decision = %#v, %v", decided, err)
	}
	access, _ := repository.ResolveWorkbookAccess(ctx, workbookID, AccessPrincipal{UserID: "alice", Authenticated: true})
	if access.Role != RoleCommenter {
		t.Fatalf("approved access = %#v", access)
	}
	if _, err := repository.DecideAccessRequest(ctx, request.ID, DecideAccessRequestInput{Approve: false, ActorID: "owner"}); !errors.Is(err, ErrRevision) {
		t.Fatalf("deciding twice error = %v", err)
	}

	sharing, err := repository.TransferWorkbookOwnership(ctx, workbookID, TransferOwnershipInput{NewOwnerID: "alice", KeepAsEditor: true, ActorID: "owner"})
	if err != nil || sharing.OwnerID != "alice" {
		t.Fatalf("transfer = %#v, %v", sharing, err)
	}
	newOwner, _ := repository.ResolveWorkbookAccess(ctx, workbookID, AccessPrincipal{UserID: "alice", Authenticated: true})
	previous, _ := repository.ResolveWorkbookAccess(ctx, workbookID, AccessPrincipal{UserID: "owner", Authenticated: true})
	if newOwner.Role != RoleOwner || previous.Role != RoleEditor {
		t.Fatalf("after transfer owner=%s previous=%s", newOwner.Role, previous.Role)
	}
}

func boolPointer(value bool) *bool { return &value }
