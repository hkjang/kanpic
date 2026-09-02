package workbook

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"kanpic/pkg/identity"
)

// The in-memory repository mirrors the PostgreSQL sharing behaviour so tests and
// the development server enforce the same rules.

func (r *MemoryRepository) sharingLocked(workbookID string) (WorkbookSharing, error) {
	state, ok := r.workbooks[workbookID]
	if !ok {
		return WorkbookSharing{}, ErrNotFound
	}
	sharing := WorkbookSharing{
		WorkbookID: workbookID, OwnerID: state.workbook.OwnerID,
		LinkAccess: state.workbook.LinkAccess, LinkRole: state.workbook.LinkRole,
		SharingLocked: state.workbook.SharingLocked, ViewerCanCopy: state.workbook.ViewerCanCopy,
		Shares: make([]WorkbookShare, 0, len(r.shares[workbookID])),
	}
	if sharing.LinkAccess == "" {
		sharing.LinkAccess = LinkAccessRestricted
	}
	if sharing.LinkRole == RoleNone {
		sharing.LinkRole = RoleViewer
	}
	for _, share := range r.shares[workbookID] {
		sharing.Shares = append(sharing.Shares, share)
	}
	sort.Slice(sharing.Shares, func(i, j int) bool {
		if sharing.Shares[i].PrincipalType == sharing.Shares[j].PrincipalType {
			return strings.ToLower(sharing.Shares[i].PrincipalID) < strings.ToLower(sharing.Shares[j].PrincipalID)
		}
		return sharing.Shares[i].PrincipalType < sharing.Shares[j].PrincipalType
	})
	return sharing, nil
}

func (r *MemoryRepository) GetWorkbookSharing(_ context.Context, workbookID string) (WorkbookSharing, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.sharingLocked(workbookID)
}

// departmentClosureLocked collects the principal's departments and every
// ancestor so a share on a parent department reaches the descendants.
func (r *MemoryRepository) departmentClosureLocked(principal AccessPrincipal) map[string]string {
	closure := make(map[string]string)
	identities := principal.identities()
	if len(identities) == 0 {
		return closure
	}
	for id, department := range r.departments {
		for _, member := range r.departmentMembers[id] {
			lowered := strings.ToLower(member)
			for _, identity := range identities {
				if lowered != identity {
					continue
				}
				for cursor := department; ; {
					closure[strings.ToLower(cursor.ID)] = cursor.Name
					parent, ok := r.departments[cursor.ParentID]
					if !ok {
						break
					}
					cursor = parent
				}
			}
		}
	}
	return closure
}

func (r *MemoryRepository) ResolveWorkbookAccess(_ context.Context, workbookID string, principal AccessPrincipal) (WorkbookAccess, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	sharing, err := r.sharingLocked(workbookID)
	if err != nil {
		return WorkbookAccess{}, err
	}
	return resolveAccess(workbookID, principal, sharing, r.departmentClosureLocked(principal)), nil
}

func (r *MemoryRepository) UpdateWorkbookSharing(_ context.Context, workbookID string, input UpdateSharingInput) (WorkbookSharing, error) {
	normalized, err := validateSharingInput(input)
	if err != nil {
		return WorkbookSharing{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	state, ok := r.workbooks[workbookID]
	if !ok {
		return WorkbookSharing{}, ErrNotFound
	}
	if normalized.LinkAccess != nil {
		state.workbook.LinkAccess = *normalized.LinkAccess
	}
	if normalized.LinkRole != nil {
		state.workbook.LinkRole = *normalized.LinkRole
	}
	if normalized.SharingLocked != nil {
		state.workbook.SharingLocked = *normalized.SharingLocked
	}
	if normalized.ViewerCanCopy != nil {
		state.workbook.ViewerCanCopy = *normalized.ViewerCanCopy
	}
	state.workbook.UpdatedAt = r.now()
	return r.sharingLocked(workbookID)
}

func (r *MemoryRepository) PutWorkbookShare(_ context.Context, workbookID string, input ShareInput) (WorkbookShare, error) {
	normalized, err := validateShareInput(input)
	if err != nil {
		return WorkbookShare{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	state, ok := r.workbooks[workbookID]
	if !ok {
		return WorkbookShare{}, ErrNotFound
	}
	if normalized.PrincipalType == PrincipalUser && strings.EqualFold(normalized.PrincipalID, state.workbook.OwnerID) {
		return WorkbookShare{}, fmt.Errorf("%w: the owner already has full access", ErrInvalid)
	}
	if normalized.PrincipalType == PrincipalDepartment {
		if _, exists := r.departments[normalized.PrincipalID]; !exists {
			return WorkbookShare{}, ErrNotFound
		}
	}
	if r.shares[workbookID] == nil {
		r.shares[workbookID] = make(map[string]WorkbookShare)
	}
	now := r.now()
	key := normalized.PrincipalType + ":" + strings.ToLower(normalized.PrincipalID)
	share, existing := r.shares[workbookID][key]
	if !existing {
		if len(r.shares[workbookID]) >= MaxWorkbookShares {
			return WorkbookShare{}, fmt.Errorf("%w: a workbook accepts at most %d shares", ErrInvalid, MaxWorkbookShares)
		}
		share = WorkbookShare{ID: identity.New(), WorkbookID: workbookID, PrincipalType: normalized.PrincipalType, PrincipalID: normalized.PrincipalID, Revision: 1, CreatedBy: normalized.ActorID, CreatedAt: now}
	} else {
		share.Revision++
	}
	share.Role = normalized.Role
	share.PrincipalLabel = normalized.PrincipalLabel
	share.UpdatedAt = now
	r.shares[workbookID][key] = share
	return share, nil
}

func (r *MemoryRepository) DeleteWorkbookShare(_ context.Context, workbookID, shareID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, share := range r.shares[workbookID] {
		if share.ID == shareID {
			delete(r.shares[workbookID], key)
			return nil
		}
	}
	return ErrNotFound
}

func (r *MemoryRepository) TransferWorkbookOwnership(_ context.Context, workbookID string, input TransferOwnershipInput) (WorkbookSharing, error) {
	newOwner := strings.TrimSpace(input.NewOwnerID)
	if newOwner == "" {
		return WorkbookSharing{}, fmt.Errorf("%w: new_owner_id is required", ErrInvalid)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	state, ok := r.workbooks[workbookID]
	if !ok {
		return WorkbookSharing{}, ErrNotFound
	}
	previous := state.workbook.OwnerID
	if strings.EqualFold(previous, newOwner) {
		return r.sharingLocked(workbookID)
	}
	state.workbook.OwnerID = newOwner
	state.workbook.UpdatedAt = r.now()
	if r.shares[workbookID] == nil {
		r.shares[workbookID] = make(map[string]WorkbookShare)
	}
	delete(r.shares[workbookID], PrincipalUser+":"+strings.ToLower(newOwner))
	if input.KeepAsEditor && strings.TrimSpace(previous) != "" {
		now := r.now()
		key := PrincipalUser + ":" + strings.ToLower(previous)
		share, existing := r.shares[workbookID][key]
		if !existing {
			share = WorkbookShare{ID: identity.New(), WorkbookID: workbookID, PrincipalType: PrincipalUser, PrincipalID: previous, Revision: 1, CreatedBy: input.ActorID, CreatedAt: now}
		} else {
			share.Revision++
		}
		share.Role = RoleEditor
		share.UpdatedAt = now
		r.shares[workbookID][key] = share
	}
	return r.sharingLocked(workbookID)
}

func (r *MemoryRepository) departmentDepthLocked(id string) int {
	depth := 0
	for cursor, ok := r.departments[id]; ok; cursor, ok = r.departments[cursor.ParentID] {
		if cursor.ParentID == "" {
			break
		}
		depth++
		if depth > MaxDepartmentDepth {
			break
		}
	}
	return depth
}

func (r *MemoryRepository) departmentPathLocked(id string) string {
	names := make([]string, 0, 4)
	for cursor, ok := r.departments[id]; ok; cursor, ok = r.departments[cursor.ParentID] {
		names = append([]string{cursor.Name}, names...)
		if cursor.ParentID == "" {
			break
		}
	}
	return strings.Join(names, " / ")
}

func (r *MemoryRepository) departmentLocked(id string) (Department, error) {
	stored, ok := r.departments[id]
	if !ok {
		return Department{}, ErrNotFound
	}
	department := stored
	members := append([]string(nil), r.departmentMembers[id]...)
	sort.Slice(members, func(i, j int) bool { return strings.ToLower(members[i]) < strings.ToLower(members[j]) })
	department.Members = members
	department.MemberCount = len(members)
	managers := append([]string(nil), r.departmentManagers[id]...)
	sort.Slice(managers, func(i, j int) bool { return strings.ToLower(managers[i]) < strings.ToLower(managers[j]) })
	department.Managers = managers
	department.Depth = r.departmentDepthLocked(id)
	department.Path = r.departmentPathLocked(id)
	return department, nil
}

func (r *MemoryRepository) CreateDepartment(_ context.Context, input CreateDepartmentInput) (Department, error) {
	name, err := validateDepartmentName(input.Name)
	if err != nil {
		return Department{}, err
	}
	description, err := validateDepartmentDescription(input.Description)
	if err != nil {
		return Department{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	parentID := strings.TrimSpace(input.ParentID)
	if parentID != "" {
		if _, ok := r.departments[parentID]; !ok {
			return Department{}, ErrNotFound
		}
		if r.departmentDepthLocked(parentID)+1 >= MaxDepartmentDepth {
			return Department{}, fmt.Errorf("%w: departments nest at most %d levels", ErrInvalid, MaxDepartmentDepth)
		}
	}
	for _, existing := range r.departments {
		if existing.ParentID == parentID && strings.EqualFold(existing.Name, name) {
			return Department{}, fmt.Errorf("%w: a sibling department already uses that name", ErrDuplicateName)
		}
	}
	now := r.now()
	department := Department{ID: identity.New(), ParentID: parentID, Name: name, Description: description, Revision: 1, CreatedBy: input.ActorID, CreatedAt: now, UpdatedAt: now}
	r.departments[department.ID] = department
	return r.departmentLocked(department.ID)
}

func (r *MemoryRepository) GetDepartment(_ context.Context, id string) (Department, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.departmentLocked(id)
}

func (r *MemoryRepository) ListDepartments(_ context.Context) ([]Department, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := make([]Department, 0, len(r.departments))
	for id := range r.departments {
		department, err := r.departmentLocked(id)
		if err != nil {
			return nil, err
		}
		items = append(items, department)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Path < items[j].Path })
	return items, nil
}

func (r *MemoryRepository) ListDepartmentsForUser(_ context.Context, userID string) ([]Department, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	closure := r.departmentClosureLocked(AccessPrincipal{UserID: userID})
	items := make([]Department, 0, len(closure))
	for id := range closure {
		for candidate := range r.departments {
			if strings.EqualFold(candidate, id) {
				department, err := r.departmentLocked(candidate)
				if err != nil {
					return nil, err
				}
				items = append(items, department)
			}
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Path < items[j].Path })
	return items, nil
}

func (r *MemoryRepository) UpdateDepartment(_ context.Context, id string, input UpdateDepartmentInput) (Department, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, ok := r.departments[id]
	if !ok {
		return Department{}, ErrNotFound
	}
	if input.ExpectedRevision > 0 && input.ExpectedRevision != current.Revision {
		return Department{}, ErrVersionConflict
	}
	if input.Name != nil {
		name, err := validateDepartmentName(*input.Name)
		if err != nil {
			return Department{}, err
		}
		for candidateID, existing := range r.departments {
			if candidateID != id && existing.ParentID == current.ParentID && strings.EqualFold(existing.Name, name) {
				return Department{}, fmt.Errorf("%w: a sibling department already uses that name", ErrDuplicateName)
			}
		}
		current.Name = name
	}
	if input.Description != nil {
		description, err := validateDepartmentDescription(*input.Description)
		if err != nil {
			return Department{}, err
		}
		current.Description = description
	}
	if input.ParentID != nil {
		parentID := strings.TrimSpace(*input.ParentID)
		if strings.EqualFold(parentID, id) {
			return Department{}, fmt.Errorf("%w: a department cannot be its own parent", ErrInvalid)
		}
		if parentID != "" {
			if _, exists := r.departments[parentID]; !exists {
				return Department{}, ErrNotFound
			}
			for cursor, ok := r.departments[parentID]; ok; cursor, ok = r.departments[cursor.ParentID] {
				if cursor.ID == id {
					return Department{}, fmt.Errorf("%w: the new parent is a descendant of this department", ErrInvalid)
				}
				if cursor.ParentID == "" {
					break
				}
			}
		}
		current.ParentID = parentID
	}
	current.Revision++
	current.UpdatedAt = r.now()
	r.departments[id] = current
	return r.departmentLocked(id)
}

func (r *MemoryRepository) DeleteDepartment(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.departments[id]; !ok {
		return ErrNotFound
	}
	for _, candidate := range r.departments {
		if candidate.ParentID == id {
			return fmt.Errorf("%w: move or delete the child departments first", ErrInvalid)
		}
	}
	delete(r.departments, id)
	delete(r.departmentMembers, id)
	for workbookID, shares := range r.shares {
		for key, share := range shares {
			if share.PrincipalType == PrincipalDepartment && strings.EqualFold(share.PrincipalID, id) {
				delete(r.shares[workbookID], key)
			}
		}
	}
	return nil
}

// AddDepartmentManagers 는 이 부서를 맡을 사람을 더한다. 전역 관리자만
// 부른다 — 부서 관리자가 스스로를 늘릴 수 있으면 위임이 아니라 승격이다.
func (r *MemoryRepository) AddDepartmentManagers(_ context.Context, id string, input DepartmentMembersInput) (Department, error) {
	managers, err := normalizeMemberIDs(input)
	if err != nil {
		return Department{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.departments[id]; !ok {
		return Department{}, ErrNotFound
	}
	existing := make(map[string]struct{}, len(r.departmentManagers[id]))
	for _, manager := range r.departmentManagers[id] {
		existing[strings.ToLower(manager)] = struct{}{}
	}
	for _, manager := range managers {
		if _, duplicate := existing[strings.ToLower(manager)]; duplicate {
			continue
		}
		r.departmentManagers[id] = append(r.departmentManagers[id], manager)
	}
	return r.departmentLocked(id)
}

func (r *MemoryRepository) RemoveDepartmentManager(_ context.Context, id, userID string) (Department, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.departments[id]; !ok {
		return Department{}, ErrNotFound
	}
	kept := make([]string, 0, len(r.departmentManagers[id]))
	for _, manager := range r.departmentManagers[id] {
		if strings.EqualFold(strings.TrimSpace(manager), strings.TrimSpace(userID)) {
			continue
		}
		kept = append(kept, manager)
	}
	r.departmentManagers[id] = kept
	return r.departmentLocked(id)
}

// ManagedMembers 는 이 사람이 맡은 부서와 그 아래 부서의 구성원을 낸다.
//
// 아래 부서까지 보는 이유는, 부서가 나뉘어도 맡은 사람이 바뀌지 않기
// 때문이다. 팀이 둘로 갈라졌다고 관리자가 절반을 못 보게 되면 위임이
// 끊긴 것을 아무도 알아채지 못한다.
func (r *MemoryRepository) ManagedMembers(_ context.Context, managerID string) ([]string, error) {
	manager := strings.ToLower(strings.TrimSpace(managerID))
	if manager == "" {
		return nil, nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	roots := make(map[string]struct{})
	for id, managers := range r.departmentManagers {
		for _, candidate := range managers {
			if strings.ToLower(strings.TrimSpace(candidate)) == manager {
				roots[id] = struct{}{}
			}
		}
	}
	if len(roots) == 0 {
		return nil, nil
	}
	inScope := func(id string) bool {
		// 위로 거슬러 올라가며 맡은 부서를 만나는지 본다.
		//
		// 걸음 수를 제한한다. 부모를 바꿀 때 자기 자손을 부모로 삼는 것은
		// 막고 있으므로 고리는 생기지 않아야 하지만, 그것 하나에 기대어
		// 끝없이 도는 반복문을 두면 자료가 한 번 어긋났을 때 서버가 선다.
		// 옆의 깊이·경로 계산도 같은 이유로 걸음을 센다.
		cursor, ok := r.departments[id]
		for steps := 0; ok && steps <= MaxDepartmentDepth; steps++ {
			if _, managed := roots[cursor.ID]; managed {
				return true
			}
			if cursor.ParentID == "" {
				return false
			}
			cursor, ok = r.departments[cursor.ParentID]
		}
		return false
	}
	seen := make(map[string]struct{})
	members := make([]string, 0)
	for id, list := range r.departmentMembers {
		if !inScope(id) {
			continue
		}
		for _, member := range list {
			key := strings.ToLower(strings.TrimSpace(member))
			if key == "" {
				continue
			}
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			members = append(members, member)
		}
	}
	sort.Strings(members)
	return members, nil
}

func (r *MemoryRepository) AddDepartmentMembers(_ context.Context, id string, input DepartmentMembersInput) (Department, error) {
	members, err := normalizeMemberIDs(input)
	if err != nil {
		return Department{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.departments[id]; !ok {
		return Department{}, ErrNotFound
	}
	existing := make(map[string]struct{}, len(r.departmentMembers[id]))
	for _, member := range r.departmentMembers[id] {
		existing[strings.ToLower(member)] = struct{}{}
	}
	for _, member := range members {
		if _, duplicate := existing[strings.ToLower(member)]; duplicate {
			continue
		}
		r.departmentMembers[id] = append(r.departmentMembers[id], member)
	}
	return r.departmentLocked(id)
}

func (r *MemoryRepository) RemoveDepartmentMember(_ context.Context, id, userID string) (Department, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.departments[id]; !ok {
		return Department{}, ErrNotFound
	}
	remaining := make([]string, 0, len(r.departmentMembers[id]))
	removed := false
	for _, member := range r.departmentMembers[id] {
		if strings.EqualFold(member, strings.TrimSpace(userID)) {
			removed = true
			continue
		}
		remaining = append(remaining, member)
	}
	if !removed {
		return Department{}, ErrNotFound
	}
	r.departmentMembers[id] = remaining
	return r.departmentLocked(id)
}

func (r *MemoryRepository) CreateAccessRequest(_ context.Context, workbookID string, input CreateAccessRequestInput) (AccessRequest, error) {
	normalized, err := validateAccessRequestInput(input)
	if err != nil {
		return AccessRequest{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	state, ok := r.workbooks[workbookID]
	if !ok {
		return AccessRequest{}, ErrNotFound
	}
	for id, existing := range r.accessRequests {
		if existing.WorkbookID == workbookID && existing.Status == AccessRequestPending && strings.EqualFold(existing.RequesterID, normalized.RequesterID) {
			existing.RequestedRole = normalized.RequestedRole
			existing.Message = normalized.Message
			existing.CreatedAt = r.now()
			r.accessRequests[id] = existing
			return existing, nil
		}
	}
	request := AccessRequest{
		ID: identity.New(), WorkbookID: workbookID, WorkbookTitle: state.workbook.Title,
		RequesterID: normalized.RequesterID, RequesterMail: normalized.RequesterMail, RequesterName: normalized.RequesterName,
		RequestedRole: normalized.RequestedRole, Message: normalized.Message, Status: AccessRequestPending, CreatedAt: r.now(),
	}
	r.accessRequests[request.ID] = request
	return request, nil
}

func (r *MemoryRepository) ListAccessRequests(_ context.Context, workbookID string, pendingOnly bool) ([]AccessRequest, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if _, ok := r.workbooks[workbookID]; !ok {
		return nil, ErrNotFound
	}
	items := make([]AccessRequest, 0)
	for _, request := range r.accessRequests {
		if request.WorkbookID != workbookID {
			continue
		}
		if pendingOnly && request.Status != AccessRequestPending {
			continue
		}
		items = append(items, request)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	return items, nil
}

func (r *MemoryRepository) DecideAccessRequest(ctx context.Context, requestID string, input DecideAccessRequestInput) (AccessRequest, error) {
	r.mu.Lock()
	request, ok := r.accessRequests[requestID]
	if !ok {
		r.mu.Unlock()
		return AccessRequest{}, ErrNotFound
	}
	if request.Status != AccessRequestPending {
		r.mu.Unlock()
		return AccessRequest{}, fmt.Errorf("%w: the request was already decided", ErrRevision)
	}
	granted := request.RequestedRole
	if input.Role != RoleNone {
		if !AssignableShareRole(input.Role) {
			r.mu.Unlock()
			return AccessRequest{}, fmt.Errorf("%w: role must be viewer, commenter or editor", ErrInvalid)
		}
		granted = input.Role
	}
	r.mu.Unlock()
	if input.Approve {
		if _, err := r.PutWorkbookShare(ctx, request.WorkbookID, ShareInput{
			PrincipalType: PrincipalUser, PrincipalID: request.RequesterID, PrincipalLabel: request.RequesterName, Role: granted, ActorID: input.ActorID,
		}); err != nil {
			return AccessRequest{}, err
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	decided := r.now()
	request.Status = AccessRequestDenied
	if input.Approve {
		request.Status = AccessRequestApproved
	}
	request.RequestedRole = granted
	request.DecidedBy = input.ActorID
	request.DecidedAt = &decided
	r.accessRequests[requestID] = request
	return request, nil
}

func (r *MemoryRepository) WorkbookIDForResource(_ context.Context, kind, id string) (string, error) {
	if strings.TrimSpace(id) == "" {
		return "", ErrNotFound
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	switch kind {
	case "workbookId":
		if _, ok := r.workbooks[id]; !ok {
			return "", ErrNotFound
		}
		return id, nil
	case "sheetId":
		if workbookID, ok := r.sheetToWB[id]; ok {
			return workbookID, nil
		}
	case "chartId":
		if chart, ok := r.charts[id]; ok {
			return chart.WorkbookID, nil
		}
	case "imageId":
		if image, ok := r.images[id]; ok {
			return image.WorkbookID, nil
		}
	case "pivotId":
		if pivot, ok := r.pivots[id]; ok {
			return pivot.WorkbookID, nil
		}
	case "namedRangeId":
		if named, ok := r.namedRanges[id]; ok {
			return named.WorkbookID, nil
		}
	case "namedFunctionId":
		if named, ok := r.namedFunctions[id]; ok {
			return named.WorkbookID, nil
		}
	case "watchRuleId":
		if rule, ok := r.watchRules[id]; ok {
			return rule.WorkbookID, nil
		}
	case "sheetTableId":
		if table, ok := r.sheetTables[id]; ok {
			return table.WorkbookID, nil
		}
	case "scenarioId":
		if item, ok := r.scenarios[id]; ok {
			return item.WorkbookID, nil
		}
	case "commentId":
		if thread, ok := r.comments[id]; ok {
			return thread.WorkbookID, nil
		}
	case "messageId":
		for _, thread := range r.comments {
			for _, message := range thread.Messages {
				if message.ID == id {
					return thread.WorkbookID, nil
				}
			}
		}
	case "accessRequestId":
		if request, ok := r.accessRequests[id]; ok {
			return request.WorkbookID, nil
		}
	case "conflictId":
		if conflict, ok := r.conflicts[id]; ok {
			return conflict.WorkbookID, nil
		}
	case "filterViewId":
		if view, ok := r.filters[id]; ok {
			return r.sheetToWB[view.SheetID], nil
		}
	case "dataValidationId":
		if rule, ok := r.validations[id]; ok {
			return r.sheetToWB[rule.SheetID], nil
		}
	case "conditionalFormatId":
		if rule, ok := r.conditionalFormats[id]; ok {
			return r.sheetToWB[rule.SheetID], nil
		}
	case "versionId":
		for workbookID, state := range r.workbooks {
			for _, saved := range state.versions {
				if saved.version.ID == id {
					return workbookID, nil
				}
			}
		}
	case "operationId":
		for workbookID, state := range r.workbooks {
			for _, candidate := range state.operations {
				if candidate.result.OperationID == id {
					return workbookID, nil
				}
			}
		}
	case "automationId", "automationRunId", "aiActionId":
		// Automations and AI actions are owned by their own services in memory
		// mode, so the API layer falls back to the workbook in the request body.
		return "", ErrNotFound
	default:
		return "", fmt.Errorf("%w: unknown resource kind %q", ErrInvalid, kind)
	}
	return "", ErrNotFound
}
