package workbook

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

func (r *MemoryRepository) directoryUserLocked(userID string) (DirectoryUser, bool) {
	stored, ok := r.directory[strings.ToLower(strings.TrimSpace(userID))]
	if !ok {
		return DirectoryUser{}, false
	}
	user := stored
	user.Roles = append([]string(nil), r.userRoles[strings.ToLower(user.UserID)]...)
	sort.Slice(user.Roles, func(i, j int) bool { return strings.ToLower(user.Roles[i]) < strings.ToLower(user.Roles[j]) })
	user.Departments = make([]string, 0)
	for id, department := range r.departments {
		for _, member := range r.departmentMembers[id] {
			if strings.EqualFold(member, user.UserID) {
				user.Departments = append(user.Departments, department.Name)
			}
		}
	}
	sort.Strings(user.Departments)
	user.OwnedBooks = 0
	for _, state := range r.workbooks {
		if strings.EqualFold(state.workbook.OwnerID, user.UserID) {
			user.OwnedBooks++
		}
	}
	return user, true
}

func (r *MemoryRepository) UserAccessProfile(_ context.Context, userID string) (UserAccessProfile, error) {
	profile := UserAccessProfile{UserID: strings.TrimSpace(userID), Status: UserStatusActive, Roles: []string{}}
	if profile.UserID == "" {
		return profile, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := strings.ToLower(profile.UserID)
	stored, ok := r.directory[key]
	if !ok {
		return profile, nil
	}
	seen := r.now()
	stored.LastSeenAt = &seen
	r.directory[key] = stored
	profile.Known = true
	profile.Status = stored.Status
	profile.Suspended = stored.Status == UserStatusSuspended
	profile.Roles = append([]string(nil), r.userRoles[key]...)
	return profile, nil
}

func (r *MemoryRepository) EnsureUser(_ context.Context, userID, displayName, email string) error {
	id, err := normalizeUserID(userID)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := strings.ToLower(id)
	now := r.now()
	stored, ok := r.directory[key]
	if !ok {
		stored = DirectoryUser{UserID: id, Status: UserStatusActive, CreatedAt: now}
	}
	if trimmed := strings.TrimSpace(displayName); trimmed != "" {
		stored.DisplayName = trimmed
	}
	if trimmed := strings.TrimSpace(email); trimmed != "" {
		stored.Email = trimmed
	}
	stored.UpdatedAt, stored.LastSeenAt = now, &now
	r.directory[key] = stored
	return nil
}

func (r *MemoryRepository) ListUsers(_ context.Context) ([]DirectoryUser, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := make([]DirectoryUser, 0, len(r.directory))
	for key := range r.directory {
		if user, ok := r.directoryUserLocked(key); ok {
			items = append(items, user)
		}
	}
	sort.Slice(items, func(i, j int) bool { return strings.ToLower(items[i].UserID) < strings.ToLower(items[j].UserID) })
	return items, nil
}

func (r *MemoryRepository) GetUser(_ context.Context, userID string) (DirectoryUser, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	user, ok := r.directoryUserLocked(userID)
	if !ok {
		return DirectoryUser{}, ErrNotFound
	}
	return user, nil
}

func (r *MemoryRepository) UpsertUser(ctx context.Context, input UpsertUserInput) (DirectoryUser, error) {
	normalized, err := validateUpsertUser(input)
	if err != nil {
		return DirectoryUser{}, err
	}
	r.mu.Lock()
	key := strings.ToLower(normalized.UserID)
	now := r.now()
	stored, ok := r.directory[key]
	if !ok {
		stored = DirectoryUser{UserID: normalized.UserID, Status: UserStatusActive, CreatedBy: normalized.ActorID, CreatedAt: now}
	}
	stored.DisplayName, stored.Email, stored.Note, stored.UpdatedAt = normalized.DisplayName, normalized.Email, normalized.Note, now
	r.directory[key] = stored
	r.mu.Unlock()
	return r.GetUser(ctx, normalized.UserID)
}

func (r *MemoryRepository) UpdateUser(ctx context.Context, userID string, input UpdateUserInput) (DirectoryUser, error) {
	normalized, err := validateUpdateUser(input)
	if err != nil {
		return DirectoryUser{}, err
	}
	id, err := normalizeUserID(userID)
	if err != nil {
		return DirectoryUser{}, err
	}
	r.mu.Lock()
	key := strings.ToLower(id)
	stored, ok := r.directory[key]
	if !ok {
		r.mu.Unlock()
		return DirectoryUser{}, ErrNotFound
	}
	if normalized.DisplayName != nil {
		stored.DisplayName = *normalized.DisplayName
	}
	if normalized.Email != nil {
		stored.Email = *normalized.Email
	}
	if normalized.Note != nil {
		stored.Note = *normalized.Note
	}
	if normalized.Status != nil {
		stored.Status = *normalized.Status
	}
	stored.UpdatedAt = r.now()
	r.directory[key] = stored
	r.mu.Unlock()
	return r.GetUser(ctx, id)
}

func (r *MemoryRepository) GrantUserRole(ctx context.Context, userID, role, actorID string) (DirectoryUser, error) {
	id, err := normalizeUserID(userID)
	if err != nil {
		return DirectoryUser{}, err
	}
	name, err := normalizeRoleName(role)
	if err != nil {
		return DirectoryUser{}, err
	}
	r.mu.Lock()
	key := strings.ToLower(id)
	if _, ok := r.directory[key]; !ok {
		r.mu.Unlock()
		return DirectoryUser{}, ErrNotFound
	}
	if len(r.userRoles[key]) >= MaxRolesPerUser {
		r.mu.Unlock()
		return DirectoryUser{}, fmt.Errorf("%w: a user holds at most %d roles", ErrInvalid, MaxRolesPerUser)
	}
	for _, existing := range r.userRoles[key] {
		if strings.EqualFold(existing, name) {
			r.mu.Unlock()
			return r.GetUser(ctx, id)
		}
	}
	r.userRoles[key] = append(r.userRoles[key], name)
	r.mu.Unlock()
	return r.GetUser(ctx, id)
}

func (r *MemoryRepository) RevokeUserRole(ctx context.Context, userID, role string) (DirectoryUser, error) {
	id, err := normalizeUserID(userID)
	if err != nil {
		return DirectoryUser{}, err
	}
	r.mu.Lock()
	key := strings.ToLower(id)
	remaining := make([]string, 0, len(r.userRoles[key]))
	removed := false
	for _, existing := range r.userRoles[key] {
		if strings.EqualFold(existing, strings.TrimSpace(role)) {
			removed = true
			continue
		}
		remaining = append(remaining, existing)
	}
	if !removed {
		r.mu.Unlock()
		return DirectoryUser{}, ErrNotFound
	}
	r.userRoles[key] = remaining
	r.mu.Unlock()
	return r.GetUser(ctx, id)
}

func (r *MemoryRepository) LookupUsers(_ context.Context, ids []string) ([]UserSummary, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	wanted := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if trimmed := strings.TrimSpace(id); trimmed != "" && len(wanted) < MaxUserLookupIDs {
			wanted[strings.ToLower(trimmed)] = struct{}{}
		}
	}
	items := make([]UserSummary, 0, len(wanted))
	for _, user := range r.directory {
		_, byID := wanted[strings.ToLower(user.UserID)]
		_, byEmail := wanted[strings.ToLower(user.Email)]
		if byID || (user.Email != "" && byEmail) {
			items = append(items, UserSummary{UserID: user.UserID, DisplayName: user.DisplayName, Email: user.Email})
		}
	}
	sort.Slice(items, func(i, j int) bool { return strings.ToLower(items[i].UserID) < strings.ToLower(items[j].UserID) })
	return items, nil
}

func (r *MemoryRepository) SearchUsers(_ context.Context, query string, limit int) ([]UserSummary, error) {
	needle := strings.ToLower(strings.TrimSpace(query))
	if needle == "" {
		return []UserSummary{}, nil
	}
	if limit <= 0 || limit > 25 {
		limit = 10
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := make([]UserSummary, 0, limit)
	for _, user := range r.directory {
		if user.Status == UserStatusSuspended {
			continue
		}
		haystack := strings.ToLower(user.UserID + " " + user.DisplayName + " " + user.Email)
		if strings.Contains(haystack, needle) {
			items = append(items, UserSummary{UserID: user.UserID, DisplayName: user.DisplayName, Email: user.Email})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		left := strings.ToLower(items[i].DisplayName + items[i].UserID)
		right := strings.ToLower(items[j].DisplayName + items[j].UserID)
		return left < right
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (r *MemoryRepository) AdminOverview(_ context.Context) (AdminOverview, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	overview := AdminOverview{Users: len(r.directory), Departments: len(r.departments), TrashedWorkbooks: len(r.trash)}
	suspended := make(map[string]struct{}, len(r.directory))
	// 잠든 것을 재는 기준 시각. 한 번만 읽어 사용자와 워크북이 같은 시각을
	// 쓴다 — 세는 도중에 날이 바뀌면 수가 서로 어긋난다.
	dormantUsers := r.now().AddDate(0, 0, -DormantUserDays)
	dormantBooks := r.now().AddDate(0, 0, -DormantWorkbookDays)
	for key, user := range r.directory {
		if user.Status == UserStatusSuspended {
			overview.SuspendedUsers++
			suspended[key] = struct{}{}
			continue
		}
		overview.ActiveUsers++
		// 한 번도 들어온 적 없는 계정도 잠든 것으로 센다. 미리 등록해 두고
		// 아무도 쓰지 않은 계정이 그대로 남는 일이 흔하다.
		if user.LastSeenAt == nil || user.LastSeenAt.Before(dormantUsers) {
			overview.DormantUsers++
		}
	}
	for id, state := range r.workbooks {
		overview.Workbooks++
		switch state.workbook.LinkAccess {
		case LinkAccessOrganization:
			overview.OrganizationShared++
		case LinkAccessAnyone:
			overview.AnyoneShared++
		}
		owner := strings.ToLower(strings.TrimSpace(state.workbook.OwnerID))
		if _, blocked := suspended[owner]; owner == "" || blocked {
			overview.OrphanWorkbooks++
		}
		if len(r.shares[id]) > 0 {
			overview.SharedWorkbooks++
			overview.Shares += len(r.shares[id])
		}
		if state.workbook.UpdatedAt.Before(dormantBooks) {
			overview.DormantWorkbooks++
		}
	}
	for _, request := range r.accessRequests {
		if request.Status == AccessRequestPending {
			overview.PendingRequests++
		}
	}
	return overview, nil
}

// WorkbooksOwnedBy 는 한 사람이 가진 워크북을 모두 낸다. 퇴사자를 정리할 때
// "이 사람이 무엇을 가지고 있는가" 를 먼저 알아야 하기 때문이다.
//
// 목록을 자르지 않는다. 소유권 관리 화면이 200개까지만 보여 주면 201번째
// 워크북은 넘겨지지 않은 채로 남고, 아무도 그것을 모른다.
//
// 휴지통에 있는 것도 함께 낸다. 지운 것도 그 사람의 것이고, 소유자만
// 되살릴 수 있다.
func (r *MemoryRepository) WorkbooksOwnedBy(_ context.Context, ownerID string) ([]GovernedWorkbook, error) {
	owner := strings.ToLower(strings.TrimSpace(ownerID))
	if owner == "" {
		return nil, fmt.Errorf("%w: owner is required", ErrInvalid)
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := make([]GovernedWorkbook, 0)
	collect := func(states map[string]*workbookState) {
		for id, state := range states {
			if strings.ToLower(strings.TrimSpace(state.workbook.OwnerID)) != owner {
				continue
			}
			person := r.directory[owner]
			item := GovernedWorkbook{
				ID: id, Title: state.workbook.Title, OwnerID: state.workbook.OwnerID,
				OwnerName: person.DisplayName, OwnerStatus: person.Status,
				LinkAccess: state.workbook.LinkAccess, LinkRole: state.workbook.LinkRole,
				ShareCount: len(r.shares[id]), SheetCount: len(state.sheets),
				Version: state.workbook.Version, UpdatedAt: state.workbook.UpdatedAt, DeletedAt: state.deletedAt,
			}
			if item.LinkAccess == "" {
				item.LinkAccess = LinkAccessRestricted
			}
			items = append(items, item)
		}
	}
	collect(r.workbooks)
	collect(r.trash)
	sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
	return items, nil
}

func (r *MemoryRepository) GovernedWorkbooks(_ context.Context, filter string, limit int) ([]GovernedWorkbook, error) {
	if !ValidGovernanceFilter(filter) {
		return nil, fmt.Errorf("%w: unknown workbook filter", ErrInvalid)
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	states := make(map[string]*workbookState, len(r.workbooks)+len(r.trash))
	if filter == GovernanceFilterTrashed {
		for id, state := range r.trash {
			states[id] = state
		}
	} else {
		for id, state := range r.workbooks {
			states[id] = state
		}
	}
	items := make([]GovernedWorkbook, 0, len(states))
	for id, state := range states {
		owner := r.directory[strings.ToLower(strings.TrimSpace(state.workbook.OwnerID))]
		item := GovernedWorkbook{
			ID: id, Title: state.workbook.Title, OwnerID: state.workbook.OwnerID, OwnerName: owner.DisplayName, OwnerStatus: owner.Status,
			LinkAccess: state.workbook.LinkAccess, LinkRole: state.workbook.LinkRole, ShareCount: len(r.shares[id]),
			SheetCount: len(state.sheets), Version: state.workbook.Version, UpdatedAt: state.workbook.UpdatedAt, DeletedAt: state.deletedAt,
		}
		if item.LinkAccess == "" {
			item.LinkAccess = LinkAccessRestricted
		}
		for _, request := range r.accessRequests {
			if request.WorkbookID == id && request.Status == AccessRequestPending {
				item.PendingAccess++
			}
		}
		switch filter {
		case GovernanceFilterOrganization:
			if item.LinkAccess != LinkAccessOrganization {
				continue
			}
		case GovernanceFilterAnyone:
			if item.LinkAccess != LinkAccessAnyone {
				continue
			}
		case GovernanceFilterOrphan:
			if strings.TrimSpace(item.OwnerID) != "" && item.OwnerStatus != UserStatusSuspended {
				continue
			}
		case GovernanceFilterDormant:
			if item.UpdatedAt.After(r.now().AddDate(0, 0, -DormantWorkbookDays)) {
				continue
			}
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}
