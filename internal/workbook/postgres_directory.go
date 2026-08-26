package workbook

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

func (r *PostgresRepository) userRoles(ctx context.Context, userID string) ([]string, error) {
	rows, err := r.pool.Query(ctx, `SELECT role FROM user_roles WHERE lower(user_id)=lower($1) ORDER BY lower(role)`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	roles := make([]string, 0)
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			return nil, err
		}
		roles = append(roles, role)
	}
	return roles, rows.Err()
}

// UserAccessProfile is read on every authenticated request, so it is a single
// primary key lookup plus a role fetch, and it refreshes the activity stamp at
// most once every ten minutes.
func (r *PostgresRepository) UserAccessProfile(ctx context.Context, userID string) (UserAccessProfile, error) {
	profile := UserAccessProfile{UserID: strings.TrimSpace(userID), Status: UserStatusActive, Roles: []string{}}
	if profile.UserID == "" {
		return profile, nil
	}
	err := r.pool.QueryRow(ctx, `
		UPDATE directory_users SET last_seen_at=now()
		WHERE lower(user_id)=lower($1) AND (last_seen_at IS NULL OR last_seen_at < now() - interval '10 minutes')
		RETURNING status`, profile.UserID).Scan(&profile.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := r.pool.QueryRow(ctx, `SELECT status FROM directory_users WHERE lower(user_id)=lower($1)`, profile.UserID).Scan(&profile.Status); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return profile, nil
			}
			return UserAccessProfile{}, err
		}
	} else if err != nil {
		return UserAccessProfile{}, err
	}
	profile.Known = true
	profile.Suspended = profile.Status == UserStatusSuspended
	roles, err := r.userRoles(ctx, profile.UserID)
	if err != nil {
		return UserAccessProfile{}, err
	}
	profile.Roles = roles
	return profile, nil
}

// EnsureUser records somebody who signed in so administrators can manage them
// without registering accounts by hand.
func (r *PostgresRepository) EnsureUser(ctx context.Context, userID, displayName, email string) error {
	id, err := normalizeUserID(userID)
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx, `
		INSERT INTO directory_users(user_id,display_name,email,last_seen_at) VALUES($1,$2,$3,now())
		ON CONFLICT (user_id) DO UPDATE SET
			display_name=CASE WHEN excluded.display_name<>'' THEN excluded.display_name ELSE directory_users.display_name END,
			email=CASE WHEN excluded.email<>'' THEN excluded.email ELSE directory_users.email END,
			last_seen_at=now()`, id, strings.TrimSpace(displayName), strings.TrimSpace(email))
	return err
}

func (r *PostgresRepository) ListUsers(ctx context.Context) ([]DirectoryUser, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT u.user_id,u.display_name,u.email,u.status,u.note,u.created_by,u.created_at,u.updated_at,u.last_seen_at,
		       coalesce(array_agg(DISTINCT r.role) FILTER (WHERE r.role IS NOT NULL), '{}') AS roles,
		       coalesce(array_agg(DISTINCT d.name) FILTER (WHERE d.name IS NOT NULL), '{}') AS departments,
		       (SELECT count(*) FROM workbooks w WHERE lower(w.owner_id)=lower(u.user_id) AND w.deleted_at IS NULL) AS owned
		FROM directory_users u
		LEFT JOIN user_roles r ON lower(r.user_id)=lower(u.user_id)
		LEFT JOIN department_members m ON lower(m.user_id)=lower(u.user_id)
		LEFT JOIN departments d ON d.id=m.department_id
		GROUP BY u.user_id
		ORDER BY lower(u.user_id)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]DirectoryUser, 0)
	for rows.Next() {
		var user DirectoryUser
		if err := rows.Scan(&user.UserID, &user.DisplayName, &user.Email, &user.Status, &user.Note, &user.CreatedBy, &user.CreatedAt, &user.UpdatedAt, &user.LastSeenAt, &user.Roles, &user.Departments, &user.OwnedBooks); err != nil {
			return nil, err
		}
		items = append(items, user)
	}
	return items, rows.Err()
}

func (r *PostgresRepository) GetUser(ctx context.Context, userID string) (DirectoryUser, error) {
	users, err := r.ListUsers(ctx)
	if err != nil {
		return DirectoryUser{}, err
	}
	for _, user := range users {
		if strings.EqualFold(user.UserID, strings.TrimSpace(userID)) {
			return user, nil
		}
	}
	return DirectoryUser{}, ErrNotFound
}

func (r *PostgresRepository) UpsertUser(ctx context.Context, input UpsertUserInput) (DirectoryUser, error) {
	normalized, err := validateUpsertUser(input)
	if err != nil {
		return DirectoryUser{}, err
	}
	if _, err := r.pool.Exec(ctx, `
		INSERT INTO directory_users(user_id,display_name,email,note,created_by) VALUES($1,$2,$3,$4,$5)
		ON CONFLICT (user_id) DO UPDATE SET
			display_name=excluded.display_name, email=excluded.email, note=excluded.note, updated_at=now()`,
		normalized.UserID, normalized.DisplayName, normalized.Email, normalized.Note, normalized.ActorID); err != nil {
		return DirectoryUser{}, err
	}
	return r.GetUser(ctx, normalized.UserID)
}

func (r *PostgresRepository) UpdateUser(ctx context.Context, userID string, input UpdateUserInput) (DirectoryUser, error) {
	normalized, err := validateUpdateUser(input)
	if err != nil {
		return DirectoryUser{}, err
	}
	id, err := normalizeUserID(userID)
	if err != nil {
		return DirectoryUser{}, err
	}
	assignments := make([]string, 0, 4)
	args := []any{id}
	appendAssignment := func(column string, value any) {
		args = append(args, value)
		assignments = append(assignments, fmt.Sprintf("%s=$%d", column, len(args)))
	}
	if normalized.DisplayName != nil {
		appendAssignment("display_name", *normalized.DisplayName)
	}
	if normalized.Email != nil {
		appendAssignment("email", *normalized.Email)
	}
	if normalized.Note != nil {
		appendAssignment("note", *normalized.Note)
	}
	if normalized.Status != nil {
		appendAssignment("status", *normalized.Status)
	}
	statement := fmt.Sprintf(`UPDATE directory_users SET %s, updated_at=now() WHERE lower(user_id)=lower($1)`, strings.Join(assignments, ","))
	command, err := r.pool.Exec(ctx, statement, args...)
	if err != nil {
		return DirectoryUser{}, err
	}
	if command.RowsAffected() == 0 {
		return DirectoryUser{}, ErrNotFound
	}
	return r.GetUser(ctx, id)
}

func (r *PostgresRepository) GrantUserRole(ctx context.Context, userID, role, actorID string) (DirectoryUser, error) {
	id, err := normalizeUserID(userID)
	if err != nil {
		return DirectoryUser{}, err
	}
	name, err := normalizeRoleName(role)
	if err != nil {
		return DirectoryUser{}, err
	}
	if _, err := r.GetUser(ctx, id); err != nil {
		return DirectoryUser{}, err
	}
	var count int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM user_roles WHERE lower(user_id)=lower($1)`, id).Scan(&count); err != nil {
		return DirectoryUser{}, err
	}
	if count >= MaxRolesPerUser {
		return DirectoryUser{}, fmt.Errorf("%w: a user holds at most %d roles", ErrInvalid, MaxRolesPerUser)
	}
	if _, err := r.pool.Exec(ctx, `INSERT INTO user_roles(user_id,role,granted_by) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, id, name, actorID); err != nil {
		return DirectoryUser{}, err
	}
	return r.GetUser(ctx, id)
}

func (r *PostgresRepository) RevokeUserRole(ctx context.Context, userID, role string) (DirectoryUser, error) {
	id, err := normalizeUserID(userID)
	if err != nil {
		return DirectoryUser{}, err
	}
	command, err := r.pool.Exec(ctx, `DELETE FROM user_roles WHERE lower(user_id)=lower($1) AND lower(role)=lower($2)`, id, strings.TrimSpace(role))
	if err != nil {
		return DirectoryUser{}, err
	}
	if command.RowsAffected() == 0 {
		return DirectoryUser{}, ErrNotFound
	}
	return r.GetUser(ctx, id)
}

// LookupUsers resolves identifiers or e-mail addresses to display names so the
// interface can show a person instead of a raw identifier.
func (r *PostgresRepository) LookupUsers(ctx context.Context, ids []string) ([]UserSummary, error) {
	wanted := make([]string, 0, len(ids))
	for _, id := range ids {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			wanted = append(wanted, strings.ToLower(trimmed))
		}
		if len(wanted) >= MaxUserLookupIDs {
			break
		}
	}
	if len(wanted) == 0 {
		return []UserSummary{}, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT user_id, display_name, email FROM directory_users
		WHERE lower(user_id) = ANY($1) OR (email <> '' AND lower(email) = ANY($1))`, wanted)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]UserSummary, 0, len(wanted))
	for rows.Next() {
		var summary UserSummary
		if err := rows.Scan(&summary.UserID, &summary.DisplayName, &summary.Email); err != nil {
			return nil, err
		}
		items = append(items, summary)
	}
	return items, rows.Err()
}

// SearchUsers powers the share and mention pickers.
func (r *PostgresRepository) SearchUsers(ctx context.Context, query string, limit int) ([]UserSummary, error) {
	needle := strings.TrimSpace(query)
	if needle == "" {
		return []UserSummary{}, nil
	}
	if limit <= 0 || limit > 25 {
		limit = 10
	}
	rows, err := r.pool.Query(ctx, `
		SELECT user_id, display_name, email FROM directory_users
		WHERE status='active' AND (user_id ILIKE '%'||$1||'%' OR display_name ILIKE '%'||$1||'%' OR email ILIKE '%'||$1||'%')
		ORDER BY lower(coalesce(nullif(display_name,''), user_id))
		LIMIT $2`, needle, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]UserSummary, 0, limit)
	for rows.Next() {
		var summary UserSummary
		if err := rows.Scan(&summary.UserID, &summary.DisplayName, &summary.Email); err != nil {
			return nil, err
		}
		items = append(items, summary)
	}
	return items, rows.Err()
}

func (r *PostgresRepository) AdminOverview(ctx context.Context) (AdminOverview, error) {
	var overview AdminOverview
	err := r.pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM directory_users),
			(SELECT count(*) FROM directory_users WHERE status='active'),
			(SELECT count(*) FROM directory_users WHERE status='suspended'),
			(SELECT count(*) FROM departments),
			(SELECT count(*) FROM workbooks WHERE deleted_at IS NULL),
			(SELECT count(*) FROM workbooks WHERE deleted_at IS NOT NULL),
			(SELECT count(DISTINCT workbook_id) FROM workbook_shares),
			(SELECT count(*) FROM workbooks WHERE deleted_at IS NULL AND link_access='organization'),
			(SELECT count(*) FROM workbooks WHERE deleted_at IS NULL AND link_access='anyone'),
			(SELECT count(*) FROM workbooks w WHERE w.deleted_at IS NULL AND (btrim(w.owner_id)='' OR EXISTS(
				SELECT 1 FROM directory_users u WHERE lower(u.user_id)=lower(w.owner_id) AND u.status='suspended'))),
			(SELECT count(*) FROM workbook_access_requests WHERE status='pending'),
			(SELECT count(*) FROM workbook_shares)`).
		Scan(&overview.Users, &overview.ActiveUsers, &overview.SuspendedUsers, &overview.Departments, &overview.Workbooks,
			&overview.TrashedWorkbooks, &overview.SharedWorkbooks, &overview.OrganizationShared, &overview.AnyoneShared,
			&overview.OrphanWorkbooks, &overview.PendingRequests, &overview.Shares)
	if err != nil {
		return AdminOverview{}, err
	}
	return overview, nil
}

// GovernedWorkbooks lists workbooks for administrators, including the ones they
// do not own, so over-sharing and orphaned data can be found and fixed.
// WorkbooksOwnedBy 는 한 사람이 가진 워크북을 모두 낸다. 퇴사자를 정리할 때
// "이 사람이 무엇을 가지고 있는가" 를 먼저 알아야 하기 때문이다.
//
// 목록을 자르지 않는다. 200개까지만 보여 주면 201번째 워크북은 넘겨지지
// 않은 채로 남고, 아무도 그것을 모른다. 휴지통에 있는 것도 함께 낸다 —
// 지운 것도 그 사람의 것이고 소유자만 되살릴 수 있다.
func (r *PostgresRepository) WorkbooksOwnedBy(ctx context.Context, ownerID string) ([]GovernedWorkbook, error) {
	owner := strings.TrimSpace(ownerID)
	if owner == "" {
		return nil, fmt.Errorf("%w: owner is required", ErrInvalid)
	}
	rows, err := r.pool.Query(ctx, `
		SELECT w.id::text,w.title,w.owner_id,coalesce(u.display_name,''),coalesce(u.status,''),w.link_access,w.link_role,w.version,w.updated_at,w.deleted_at,
		       (SELECT count(*) FROM workbook_shares s WHERE s.workbook_id=w.id),
		       (SELECT count(*) FROM sheets sh WHERE sh.workbook_id=w.id),
		       (SELECT count(*) FROM workbook_access_requests q WHERE q.workbook_id=w.id AND q.status='pending')
		FROM workbooks w
		LEFT JOIN directory_users u ON lower(u.user_id)=lower(w.owner_id)
		WHERE lower(btrim(w.owner_id))=lower(btrim($1))
		ORDER BY w.updated_at DESC`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]GovernedWorkbook, 0)
	for rows.Next() {
		var item GovernedWorkbook
		if err := rows.Scan(&item.ID, &item.Title, &item.OwnerID, &item.OwnerName, &item.OwnerStatus, &item.LinkAccess, &item.LinkRole,
			&item.Version, &item.UpdatedAt, &item.DeletedAt, &item.ShareCount, &item.SheetCount, &item.PendingAccess); err != nil {
			return nil, err
		}
		if strings.TrimSpace(string(item.LinkAccess)) == "" {
			item.LinkAccess = LinkAccessRestricted
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *PostgresRepository) GovernedWorkbooks(ctx context.Context, filter string, limit int) ([]GovernedWorkbook, error) {
	if !ValidGovernanceFilter(filter) {
		return nil, fmt.Errorf("%w: unknown workbook filter", ErrInvalid)
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	condition := "w.deleted_at IS NULL"
	switch filter {
	case GovernanceFilterOrganization:
		condition += " AND w.link_access='organization'"
	case GovernanceFilterAnyone:
		condition += " AND w.link_access='anyone'"
	case GovernanceFilterOrphan:
		condition += " AND (btrim(w.owner_id)='' OR EXISTS(SELECT 1 FROM directory_users u WHERE lower(u.user_id)=lower(w.owner_id) AND u.status='suspended'))"
	case GovernanceFilterTrashed:
		condition = "w.deleted_at IS NOT NULL"
	}
	rows, err := r.pool.Query(ctx, fmt.Sprintf(`
		SELECT w.id::text,w.title,w.owner_id,coalesce(u.display_name,''),coalesce(u.status,''),w.link_access,w.link_role,w.version,w.updated_at,w.deleted_at,
		       (SELECT count(*) FROM workbook_shares s WHERE s.workbook_id=w.id),
		       (SELECT count(*) FROM sheets sh WHERE sh.workbook_id=w.id),
		       (SELECT count(*) FROM workbook_access_requests q WHERE q.workbook_id=w.id AND q.status='pending')
		FROM workbooks w
		LEFT JOIN directory_users u ON lower(u.user_id)=lower(w.owner_id)
		WHERE %s
		ORDER BY w.updated_at DESC
		LIMIT $1`, condition), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]GovernedWorkbook, 0)
	for rows.Next() {
		var item GovernedWorkbook
		if err := rows.Scan(&item.ID, &item.Title, &item.OwnerID, &item.OwnerName, &item.OwnerStatus, &item.LinkAccess, &item.LinkRole,
			&item.Version, &item.UpdatedAt, &item.DeletedAt, &item.ShareCount, &item.SheetCount, &item.PendingAccess); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
