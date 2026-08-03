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
