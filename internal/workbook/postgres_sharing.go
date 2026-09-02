package workbook

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// resourceWorkbookQueries maps a REST path parameter to the query that finds
// the workbook it belongs to, so one authorization layer can protect every
// nested resource without each handler repeating the lookup.
var resourceWorkbookQueries = map[string]string{
	"workbookId":          `SELECT id::text FROM workbooks WHERE id=$1 AND deleted_at IS NULL`,
	"sheetId":             `SELECT workbook_id::text FROM sheets WHERE id=$1`,
	"chartId":             `SELECT workbook_id::text FROM charts WHERE id=$1`,
	"imageId":             `SELECT workbook_id::text FROM sheet_images WHERE id=$1`,
	"pivotId":             `SELECT workbook_id::text FROM pivots WHERE id=$1`,
	"watchRuleId":         `SELECT workbook_id::text FROM watch_rules WHERE id=$1`,
	"namedFunctionId":     `SELECT workbook_id::text FROM named_functions WHERE id=$1`,
	"sheetTableId":        `SELECT workbook_id::text FROM sheet_tables WHERE id=$1`,
	"scenarioId":          `SELECT workbook_id::text FROM scenarios WHERE id=$1`,
	"namedRangeId":        `SELECT workbook_id::text FROM named_ranges WHERE id=$1`,
	"commentId":           `SELECT workbook_id::text FROM comment_threads WHERE id=$1`,
	"messageId":           `SELECT t.workbook_id::text FROM comment_messages m JOIN comment_threads t ON t.id=m.thread_id WHERE m.id=$1`,
	"conflictId":          `SELECT workbook_id::text FROM cell_conflicts WHERE id=$1`,
	"versionId":           `SELECT workbook_id::text FROM workbook_versions WHERE id=$1`,
	"filterViewId":        `SELECT s.workbook_id::text FROM filter_views f JOIN sheets s ON s.id=f.sheet_id WHERE f.id=$1`,
	"dataValidationId":    `SELECT s.workbook_id::text FROM data_validations v JOIN sheets s ON s.id=v.sheet_id WHERE v.id=$1`,
	"conditionalFormatId": `SELECT s.workbook_id::text FROM conditional_formats c JOIN sheets s ON s.id=c.sheet_id WHERE c.id=$1`,
	"operationId":         `SELECT workbook_id::text FROM cell_operations WHERE operation_id=$1`,
	"automationId":        `SELECT workbook_id::text FROM automations WHERE id=$1`,
	"automationRunId":     `SELECT a.workbook_id::text FROM automation_runs r JOIN automations a ON a.id=r.automation_id WHERE r.id=$1`,
	"aiActionId":          `SELECT workbook_id::text FROM ai_actions WHERE id=$1`,
	"accessRequestId":     `SELECT workbook_id::text FROM workbook_access_requests WHERE id=$1`,
}

// ResourceKinds lists the path parameters that resolve to a workbook.
func ResourceKinds() []string {
	kinds := make([]string, 0, len(resourceWorkbookQueries))
	for kind := range resourceWorkbookQueries {
		kinds = append(kinds, kind)
	}
	return kinds
}

func (r *PostgresRepository) WorkbookIDForResource(ctx context.Context, kind, id string) (string, error) {
	query, ok := resourceWorkbookQueries[kind]
	if !ok {
		return "", fmt.Errorf("%w: unknown resource kind %q", ErrInvalid, kind)
	}
	if strings.TrimSpace(id) == "" {
		return "", ErrNotFound
	}
	var workbookID string
	if err := r.pool.QueryRow(ctx, query, id).Scan(&workbookID); err != nil {
		var pgError *pgconn.PgError
		// A malformed identifier is a missing resource, not a server fault.
		if errors.Is(err, pgx.ErrNoRows) || (errors.As(err, &pgError) && pgError.Code == "22P02") {
			return "", ErrNotFound
		}
		return "", err
	}
	return workbookID, nil
}

// departmentClosure returns every department the principal belongs to plus all
// ancestors, so sharing with a parent department also reaches its descendants.
func (r *PostgresRepository) departmentClosure(ctx context.Context, principal AccessPrincipal) (map[string]string, error) {
	identities := principal.identities()
	closure := make(map[string]string)
	if len(identities) == 0 {
		return closure, nil
	}
	rows, err := r.pool.Query(ctx, `
		WITH RECURSIVE membership AS (
			SELECT d.id, d.parent_id, d.name
			FROM departments d
			JOIN department_members m ON m.department_id=d.id
			WHERE lower(m.user_id) = ANY($1)
			UNION
			SELECT p.id, p.parent_id, p.name
			FROM departments p
			JOIN membership c ON c.parent_id = p.id
		)
		SELECT id::text, name FROM membership`, identities)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		closure[strings.ToLower(id)] = name
	}
	return closure, rows.Err()
}

func (r *PostgresRepository) sharesFor(ctx context.Context, workbookIDs []string) (map[string][]WorkbookShare, error) {
	result := make(map[string][]WorkbookShare, len(workbookIDs))
	if len(workbookIDs) == 0 {
		return result, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id::text,workbook_id::text,principal_type,principal_id,principal_label,role,revision,created_by,created_at,updated_at
		FROM workbook_shares WHERE workbook_id = ANY($1::uuid[])
		ORDER BY principal_type, lower(principal_id)`, workbookIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var share WorkbookShare
		if err := rows.Scan(&share.ID, &share.WorkbookID, &share.PrincipalType, &share.PrincipalID, &share.PrincipalLabel, &share.Role, &share.Revision, &share.CreatedBy, &share.CreatedAt, &share.UpdatedAt); err != nil {
			return nil, err
		}
		result[share.WorkbookID] = append(result[share.WorkbookID], share)
	}
	return result, rows.Err()
}

func (r *PostgresRepository) GetWorkbookSharing(ctx context.Context, workbookID string) (WorkbookSharing, error) {
	sharing := WorkbookSharing{WorkbookID: workbookID}
	err := r.pool.QueryRow(ctx, `SELECT owner_id,link_access,link_role,sharing_locked,viewer_can_copy FROM workbooks WHERE id=$1 AND deleted_at IS NULL`, workbookID).
		Scan(&sharing.OwnerID, &sharing.LinkAccess, &sharing.LinkRole, &sharing.SharingLocked, &sharing.ViewerCanCopy)
	if err != nil {
		var pgError *pgconn.PgError
		if errors.Is(err, pgx.ErrNoRows) || (errors.As(err, &pgError) && pgError.Code == "22P02") {
			return WorkbookSharing{}, ErrNotFound
		}
		return WorkbookSharing{}, err
	}
	shares, err := r.sharesFor(ctx, []string{workbookID})
	if err != nil {
		return WorkbookSharing{}, err
	}
	sharing.Shares = shares[workbookID]
	if sharing.Shares == nil {
		sharing.Shares = []WorkbookShare{}
	}
	return sharing, nil
}

func (r *PostgresRepository) ResolveWorkbookAccess(ctx context.Context, workbookID string, principal AccessPrincipal) (WorkbookAccess, error) {
	sharing, err := r.GetWorkbookSharing(ctx, workbookID)
	if err != nil {
		return WorkbookAccess{}, err
	}
	closure, err := r.departmentClosure(ctx, principal)
	if err != nil {
		return WorkbookAccess{}, err
	}
	return resolveAccess(workbookID, principal, sharing, closure), nil
}

func (r *PostgresRepository) UpdateWorkbookSharing(ctx context.Context, workbookID string, input UpdateSharingInput) (WorkbookSharing, error) {
	normalized, err := validateSharingInput(input)
	if err != nil {
		return WorkbookSharing{}, err
	}
	assignments := make([]string, 0, 4)
	args := []any{workbookID}
	appendAssignment := func(column string, value any) {
		args = append(args, value)
		assignments = append(assignments, fmt.Sprintf("%s=$%d", column, len(args)))
	}
	if normalized.LinkAccess != nil {
		appendAssignment("link_access", *normalized.LinkAccess)
	}
	if normalized.LinkRole != nil {
		appendAssignment("link_role", string(*normalized.LinkRole))
	}
	if normalized.SharingLocked != nil {
		appendAssignment("sharing_locked", *normalized.SharingLocked)
	}
	if normalized.ViewerCanCopy != nil {
		appendAssignment("viewer_can_copy", *normalized.ViewerCanCopy)
	}
	statement := fmt.Sprintf(`UPDATE workbooks SET %s, updated_at=now() WHERE id=$1 AND deleted_at IS NULL`, strings.Join(assignments, ","))
	tag, err := r.pool.Exec(ctx, statement, args...)
	if err != nil {
		return WorkbookSharing{}, err
	}
	if tag.RowsAffected() == 0 {
		return WorkbookSharing{}, ErrNotFound
	}
	return r.GetWorkbookSharing(ctx, workbookID)
}

func (r *PostgresRepository) PutWorkbookShare(ctx context.Context, workbookID string, input ShareInput) (WorkbookShare, error) {
	normalized, err := validateShareInput(input)
	if err != nil {
		return WorkbookShare{}, err
	}
	sharing, err := r.GetWorkbookSharing(ctx, workbookID)
	if err != nil {
		return WorkbookShare{}, err
	}
	if normalized.PrincipalType == PrincipalUser && strings.EqualFold(normalized.PrincipalID, sharing.OwnerID) {
		return WorkbookShare{}, fmt.Errorf("%w: the owner already has full access", ErrInvalid)
	}
	if normalized.PrincipalType == PrincipalDepartment {
		if _, err := r.GetDepartment(ctx, normalized.PrincipalID); err != nil {
			return WorkbookShare{}, err
		}
	}
	existing := false
	for _, share := range sharing.Shares {
		if share.PrincipalType == normalized.PrincipalType && strings.EqualFold(share.PrincipalID, normalized.PrincipalID) {
			existing = true
			break
		}
	}
	if !existing && len(sharing.Shares) >= MaxWorkbookShares {
		return WorkbookShare{}, fmt.Errorf("%w: a workbook accepts at most %d shares", ErrInvalid, MaxWorkbookShares)
	}
	var share WorkbookShare
	err = r.pool.QueryRow(ctx, `
		INSERT INTO workbook_shares(workbook_id,principal_type,principal_id,principal_label,role,created_by)
		VALUES($1,$2,$3,$4,$5,$6)
		ON CONFLICT (workbook_id, principal_type, lower(principal_id))
		DO UPDATE SET role=EXCLUDED.role, principal_label=EXCLUDED.principal_label, revision=workbook_shares.revision+1, updated_at=now()
		RETURNING id::text,workbook_id::text,principal_type,principal_id,principal_label,role,revision,created_by,created_at,updated_at`,
		workbookID, normalized.PrincipalType, normalized.PrincipalID, normalized.PrincipalLabel, string(normalized.Role), normalized.ActorID).
		Scan(&share.ID, &share.WorkbookID, &share.PrincipalType, &share.PrincipalID, &share.PrincipalLabel, &share.Role, &share.Revision, &share.CreatedBy, &share.CreatedAt, &share.UpdatedAt)
	if err != nil {
		return WorkbookShare{}, err
	}
	return share, nil
}

func (r *PostgresRepository) DeleteWorkbookShare(ctx context.Context, workbookID, shareID string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM workbook_shares WHERE workbook_id=$1 AND id=$2`, workbookID, shareID)
	if err != nil {
		var pgError *pgconn.PgError
		if errors.As(err, &pgError) && pgError.Code == "22P02" {
			return ErrNotFound
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *PostgresRepository) TransferWorkbookOwnership(ctx context.Context, workbookID string, input TransferOwnershipInput) (WorkbookSharing, error) {
	newOwner := strings.TrimSpace(input.NewOwnerID)
	if newOwner == "" {
		return WorkbookSharing{}, fmt.Errorf("%w: new_owner_id is required", ErrInvalid)
	}
	sharing, err := r.GetWorkbookSharing(ctx, workbookID)
	if err != nil {
		return WorkbookSharing{}, err
	}
	if strings.EqualFold(sharing.OwnerID, newOwner) {
		return sharing, nil
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return WorkbookSharing{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE workbooks SET owner_id=$2, updated_at=now() WHERE id=$1 AND deleted_at IS NULL`, workbookID, newOwner); err != nil {
		return WorkbookSharing{}, err
	}
	// The new owner no longer needs a share row, and the previous owner keeps
	// editor access only when the caller asks for it.
	if _, err := tx.Exec(ctx, `DELETE FROM workbook_shares WHERE workbook_id=$1 AND principal_type='user' AND lower(principal_id)=lower($2)`, workbookID, newOwner); err != nil {
		return WorkbookSharing{}, err
	}
	if input.KeepAsEditor && strings.TrimSpace(sharing.OwnerID) != "" {
		if _, err := tx.Exec(ctx, `
			INSERT INTO workbook_shares(workbook_id,principal_type,principal_id,role,created_by)
			VALUES($1,'user',$2,'editor',$3)
			ON CONFLICT (workbook_id, principal_type, lower(principal_id))
			DO UPDATE SET role='editor', revision=workbook_shares.revision+1, updated_at=now()`, workbookID, sharing.OwnerID, input.ActorID); err != nil {
			return WorkbookSharing{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return WorkbookSharing{}, err
	}
	return r.GetWorkbookSharing(ctx, workbookID)
}

func (r *PostgresRepository) CreateDepartment(ctx context.Context, input CreateDepartmentInput) (Department, error) {
	name, err := validateDepartmentName(input.Name)
	if err != nil {
		return Department{}, err
	}
	description, err := validateDepartmentDescription(input.Description)
	if err != nil {
		return Department{}, err
	}
	var parent any
	if trimmed := strings.TrimSpace(input.ParentID); trimmed != "" {
		ancestor, err := r.GetDepartment(ctx, trimmed)
		if err != nil {
			return Department{}, err
		}
		if ancestor.Depth+1 >= MaxDepartmentDepth {
			return Department{}, fmt.Errorf("%w: departments nest at most %d levels", ErrInvalid, MaxDepartmentDepth)
		}
		parent = trimmed
	}
	var id string
	err = r.pool.QueryRow(ctx, `INSERT INTO departments(parent_id,name,description,created_by) VALUES($1,$2,$3,$4) RETURNING id::text`, parent, name, description, input.ActorID).Scan(&id)
	if err != nil {
		var pgError *pgconn.PgError
		if errors.As(err, &pgError) && pgError.Code == "23505" {
			return Department{}, fmt.Errorf("%w: a sibling department already uses that name", ErrDuplicateName)
		}
		return Department{}, err
	}
	return r.GetDepartment(ctx, id)
}

func (r *PostgresRepository) GetDepartment(ctx context.Context, id string) (Department, error) {
	var department Department
	var parent *string
	err := r.pool.QueryRow(ctx, `
		SELECT d.id::text,d.parent_id::text,d.name,d.description,d.revision,d.created_by,d.created_at,d.updated_at,
		       (SELECT count(*) FROM department_members m WHERE m.department_id=d.id)
		FROM departments d WHERE d.id=$1`, id).
		Scan(&department.ID, &parent, &department.Name, &department.Description, &department.Revision, &department.CreatedBy, &department.CreatedAt, &department.UpdatedAt, &department.MemberCount)
	if err != nil {
		var pgError *pgconn.PgError
		if errors.Is(err, pgx.ErrNoRows) || (errors.As(err, &pgError) && pgError.Code == "22P02") {
			return Department{}, ErrNotFound
		}
		return Department{}, err
	}
	if parent != nil {
		department.ParentID = *parent
	}
	path, depth, err := r.departmentPath(ctx, department.ID)
	if err != nil {
		return Department{}, err
	}
	department.Path, department.Depth = path, depth
	members, err := r.listDepartmentMembers(ctx, department.ID)
	if err != nil {
		return Department{}, err
	}
	department.Members = members
	managers, err := r.listDepartmentManagers(ctx, department.ID)
	if err != nil {
		return Department{}, err
	}
	department.Managers = managers
	return department, nil
}

func (r *PostgresRepository) departmentPath(ctx context.Context, id string) (string, int, error) {
	rows, err := r.pool.Query(ctx, `
		WITH RECURSIVE ancestry AS (
			SELECT id, parent_id, name, 0 AS depth FROM departments WHERE id=$1
			UNION ALL
			SELECT p.id, p.parent_id, p.name, a.depth+1 FROM departments p JOIN ancestry a ON a.parent_id=p.id
		)
		SELECT name, depth FROM ancestry ORDER BY depth DESC`, id)
	if err != nil {
		return "", 0, err
	}
	defer rows.Close()
	names := make([]string, 0, 4)
	depth := 0
	for rows.Next() {
		var name string
		var level int
		if err := rows.Scan(&name, &level); err != nil {
			return "", 0, err
		}
		names = append(names, name)
		if level > depth {
			depth = level
		}
	}
	if err := rows.Err(); err != nil {
		return "", 0, err
	}
	return strings.Join(names, " / "), depth, nil
}

func (r *PostgresRepository) listDepartmentManagers(ctx context.Context, id string) ([]string, error) {
	rows, err := r.pool.Query(ctx, `SELECT user_id FROM department_managers WHERE department_id=$1 ORDER BY lower(user_id)`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	managers := make([]string, 0)
	for rows.Next() {
		var manager string
		if err := rows.Scan(&manager); err != nil {
			return nil, err
		}
		managers = append(managers, manager)
	}
	return managers, rows.Err()
}

func (r *PostgresRepository) listDepartmentMembers(ctx context.Context, id string) ([]string, error) {
	rows, err := r.pool.Query(ctx, `SELECT user_id FROM department_members WHERE department_id=$1 ORDER BY lower(user_id)`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	members := make([]string, 0)
	for rows.Next() {
		var member string
		if err := rows.Scan(&member); err != nil {
			return nil, err
		}
		members = append(members, member)
	}
	return members, rows.Err()
}

func (r *PostgresRepository) ListDepartments(ctx context.Context) ([]Department, error) {
	rows, err := r.pool.Query(ctx, `
		WITH RECURSIVE tree AS (
			SELECT d.id, d.parent_id, d.name, d.description, d.revision, d.created_by, d.created_at, d.updated_at,
			       0 AS depth, d.name::text AS path
			FROM departments d WHERE d.parent_id IS NULL
			UNION ALL
			SELECT c.id, c.parent_id, c.name, c.description, c.revision, c.created_by, c.created_at, c.updated_at,
			       t.depth+1, t.path || ' / ' || c.name
			FROM departments c JOIN tree t ON c.parent_id = t.id
		)
		SELECT t.id::text, t.parent_id::text, t.name, t.description, t.revision, t.created_by, t.created_at, t.updated_at, t.depth, t.path,
		       (SELECT count(*) FROM department_members m WHERE m.department_id=t.id)
		FROM tree t ORDER BY t.path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Department, 0)
	for rows.Next() {
		var department Department
		var parent *string
		if err := rows.Scan(&department.ID, &parent, &department.Name, &department.Description, &department.Revision, &department.CreatedBy, &department.CreatedAt, &department.UpdatedAt, &department.Depth, &department.Path, &department.MemberCount); err != nil {
			return nil, err
		}
		if parent != nil {
			department.ParentID = *parent
		}
		items = append(items, department)
	}
	return items, rows.Err()
}

func (r *PostgresRepository) ListDepartmentsForUser(ctx context.Context, userID string) ([]Department, error) {
	closure, err := r.departmentClosure(ctx, AccessPrincipal{UserID: userID})
	if err != nil {
		return nil, err
	}
	all, err := r.ListDepartments(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]Department, 0, len(closure))
	for _, department := range all {
		if _, ok := closure[strings.ToLower(department.ID)]; ok {
			items = append(items, department)
		}
	}
	return items, nil
}

func (r *PostgresRepository) UpdateDepartment(ctx context.Context, id string, input UpdateDepartmentInput) (Department, error) {
	current, err := r.GetDepartment(ctx, id)
	if err != nil {
		return Department{}, err
	}
	if input.ExpectedRevision > 0 && input.ExpectedRevision != current.Revision {
		return Department{}, ErrVersionConflict
	}
	assignments := make([]string, 0, 3)
	args := []any{id}
	appendAssignment := func(column string, value any) {
		args = append(args, value)
		assignments = append(assignments, fmt.Sprintf("%s=$%d", column, len(args)))
	}
	if input.Name != nil {
		name, err := validateDepartmentName(*input.Name)
		if err != nil {
			return Department{}, err
		}
		appendAssignment("name", name)
	}
	if input.Description != nil {
		description, err := validateDepartmentDescription(*input.Description)
		if err != nil {
			return Department{}, err
		}
		appendAssignment("description", description)
	}
	if input.ParentID != nil {
		trimmed := strings.TrimSpace(*input.ParentID)
		if trimmed == "" {
			appendAssignment("parent_id", nil)
		} else {
			if strings.EqualFold(trimmed, id) {
				return Department{}, fmt.Errorf("%w: a department cannot be its own parent", ErrInvalid)
			}
			descendants, err := r.departmentDescendants(ctx, id)
			if err != nil {
				return Department{}, err
			}
			if _, cycle := descendants[strings.ToLower(trimmed)]; cycle {
				return Department{}, fmt.Errorf("%w: the new parent is a descendant of this department", ErrInvalid)
			}
			if _, err := r.GetDepartment(ctx, trimmed); err != nil {
				return Department{}, err
			}
			appendAssignment("parent_id", trimmed)
		}
	}
	if len(assignments) == 0 {
		return current, nil
	}
	statement := fmt.Sprintf(`UPDATE departments SET %s, revision=revision+1, updated_at=now() WHERE id=$1`, strings.Join(assignments, ","))
	if _, err := r.pool.Exec(ctx, statement, args...); err != nil {
		var pgError *pgconn.PgError
		if errors.As(err, &pgError) && pgError.Code == "23505" {
			return Department{}, fmt.Errorf("%w: a sibling department already uses that name", ErrDuplicateName)
		}
		return Department{}, err
	}
	return r.GetDepartment(ctx, id)
}

func (r *PostgresRepository) departmentDescendants(ctx context.Context, id string) (map[string]struct{}, error) {
	rows, err := r.pool.Query(ctx, `
		WITH RECURSIVE subtree AS (
			SELECT id FROM departments WHERE id=$1
			UNION ALL
			SELECT c.id FROM departments c JOIN subtree s ON c.parent_id = s.id
		)
		SELECT id::text FROM subtree`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]struct{})
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		result[strings.ToLower(value)] = struct{}{}
	}
	return result, rows.Err()
}

func (r *PostgresRepository) DeleteDepartment(ctx context.Context, id string) error {
	if _, err := r.GetDepartment(ctx, id); err != nil {
		return err
	}
	var children int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM departments WHERE parent_id=$1`, id).Scan(&children); err != nil {
		return err
	}
	if children > 0 {
		return fmt.Errorf("%w: move or delete the child departments first", ErrInvalid)
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM workbook_shares WHERE principal_type='department' AND lower(principal_id)=lower($1)`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM departments WHERE id=$1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// AddDepartmentManagers 는 이 부서를 맡을 사람을 더한다. 전역 관리자만
// 부른다 — 부서 관리자가 스스로를 늘릴 수 있으면 위임이 아니라 승격이다.
func (r *PostgresRepository) AddDepartmentManagers(ctx context.Context, id string, input DepartmentMembersInput) (Department, error) {
	managers, err := normalizeMemberIDs(input)
	if err != nil {
		return Department{}, err
	}
	if _, err := r.GetDepartment(ctx, id); err != nil {
		return Department{}, err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Department{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, manager := range managers {
		if _, err := tx.Exec(ctx, `INSERT INTO department_managers(department_id,user_id,added_by) VALUES($1,$2,$3) ON CONFLICT (department_id,user_id) DO NOTHING`, id, manager, input.ActorID); err != nil {
			return Department{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Department{}, err
	}
	return r.GetDepartment(ctx, id)
}

func (r *PostgresRepository) RemoveDepartmentManager(ctx context.Context, id, userID string) (Department, error) {
	if _, err := r.GetDepartment(ctx, id); err != nil {
		return Department{}, err
	}
	if _, err := r.pool.Exec(ctx, `DELETE FROM department_managers WHERE department_id=$1 AND lower(user_id)=lower($2)`, id, userID); err != nil {
		return Department{}, err
	}
	return r.GetDepartment(ctx, id)
}

// ManagedMembers 는 이 사람이 맡은 부서와 그 아래 부서의 구성원을 낸다.
//
// 아래 부서까지 보는 이유는, 부서가 나뉘어도 맡은 사람이 바뀌지 않기
// 때문이다. 팀이 둘로 갈라졌다고 관리자가 절반을 못 보게 되면 위임이
// 끊긴 것을 아무도 알아채지 못한다.
func (r *PostgresRepository) ManagedMembers(ctx context.Context, managerID string) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		WITH RECURSIVE managed AS (
			SELECT d.id FROM departments d
			JOIN department_managers m ON m.department_id=d.id AND lower(m.user_id)=lower(btrim($1))
			UNION
			SELECT child.id FROM departments child JOIN managed ON child.parent_id=managed.id
		)
		SELECT DISTINCT member.user_id
		FROM department_members member JOIN managed ON member.department_id=managed.id
		ORDER BY member.user_id`, managerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	members := make([]string, 0)
	for rows.Next() {
		var member string
		if err := rows.Scan(&member); err != nil {
			return nil, err
		}
		members = append(members, member)
	}
	return members, rows.Err()
}

func (r *PostgresRepository) AddDepartmentMembers(ctx context.Context, id string, input DepartmentMembersInput) (Department, error) {
	members, err := normalizeMemberIDs(input)
	if err != nil {
		return Department{}, err
	}
	if _, err := r.GetDepartment(ctx, id); err != nil {
		return Department{}, err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Department{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, member := range members {
		if _, err := tx.Exec(ctx, `INSERT INTO department_members(department_id,user_id,added_by) VALUES($1,$2,$3) ON CONFLICT (department_id,user_id) DO NOTHING`, id, member, input.ActorID); err != nil {
			return Department{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Department{}, err
	}
	return r.GetDepartment(ctx, id)
}

func (r *PostgresRepository) RemoveDepartmentMember(ctx context.Context, id, userID string) (Department, error) {
	if _, err := r.GetDepartment(ctx, id); err != nil {
		return Department{}, err
	}
	tag, err := r.pool.Exec(ctx, `DELETE FROM department_members WHERE department_id=$1 AND lower(user_id)=lower($2)`, id, strings.TrimSpace(userID))
	if err != nil {
		return Department{}, err
	}
	if tag.RowsAffected() == 0 {
		return Department{}, ErrNotFound
	}
	return r.GetDepartment(ctx, id)
}

func (r *PostgresRepository) CreateAccessRequest(ctx context.Context, workbookID string, input CreateAccessRequestInput) (AccessRequest, error) {
	normalized, err := validateAccessRequestInput(input)
	if err != nil {
		return AccessRequest{}, err
	}
	if _, err := r.GetWorkbookSharing(ctx, workbookID); err != nil {
		return AccessRequest{}, err
	}
	var request AccessRequest
	err = r.pool.QueryRow(ctx, `
		INSERT INTO workbook_access_requests(workbook_id,requester_id,requester_email,requester_name,requested_role,message)
		VALUES($1,$2,$3,$4,$5,$6)
		ON CONFLICT (workbook_id, lower(requester_id)) WHERE status='pending'
		DO UPDATE SET requested_role=EXCLUDED.requested_role, message=EXCLUDED.message, created_at=now()
		RETURNING id::text,workbook_id::text,requester_id,requester_email,requester_name,requested_role,message,status,decided_by,decided_at,created_at`,
		workbookID, normalized.RequesterID, normalized.RequesterMail, normalized.RequesterName, string(normalized.RequestedRole), normalized.Message).
		Scan(&request.ID, &request.WorkbookID, &request.RequesterID, &request.RequesterMail, &request.RequesterName, &request.RequestedRole, &request.Message, &request.Status, &request.DecidedBy, &request.DecidedAt, &request.CreatedAt)
	if err != nil {
		return AccessRequest{}, err
	}
	return request, nil
}

func (r *PostgresRepository) ListAccessRequests(ctx context.Context, workbookID string, pendingOnly bool) ([]AccessRequest, error) {
	query := `
		SELECT r.id::text,r.workbook_id::text,w.title,r.requester_id,r.requester_email,r.requester_name,r.requested_role,r.message,r.status,r.decided_by,r.decided_at,r.created_at
		FROM workbook_access_requests r JOIN workbooks w ON w.id=r.workbook_id
		WHERE r.workbook_id=$1`
	if pendingOnly {
		query += ` AND r.status='pending'`
	}
	query += ` ORDER BY r.created_at DESC`
	rows, err := r.pool.Query(ctx, query, workbookID)
	if err != nil {
		var pgError *pgconn.PgError
		if errors.As(err, &pgError) && pgError.Code == "22P02" {
			return nil, ErrNotFound
		}
		return nil, err
	}
	defer rows.Close()
	items := make([]AccessRequest, 0)
	for rows.Next() {
		var request AccessRequest
		if err := rows.Scan(&request.ID, &request.WorkbookID, &request.WorkbookTitle, &request.RequesterID, &request.RequesterMail, &request.RequesterName, &request.RequestedRole, &request.Message, &request.Status, &request.DecidedBy, &request.DecidedAt, &request.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, request)
	}
	return items, rows.Err()
}

func (r *PostgresRepository) DecideAccessRequest(ctx context.Context, requestID string, input DecideAccessRequestInput) (AccessRequest, error) {
	var request AccessRequest
	err := r.pool.QueryRow(ctx, `
		SELECT id::text,workbook_id::text,requester_id,requester_email,requester_name,requested_role,message,status,decided_by,decided_at,created_at
		FROM workbook_access_requests WHERE id=$1`, requestID).
		Scan(&request.ID, &request.WorkbookID, &request.RequesterID, &request.RequesterMail, &request.RequesterName, &request.RequestedRole, &request.Message, &request.Status, &request.DecidedBy, &request.DecidedAt, &request.CreatedAt)
	if err != nil {
		var pgError *pgconn.PgError
		if errors.Is(err, pgx.ErrNoRows) || (errors.As(err, &pgError) && pgError.Code == "22P02") {
			return AccessRequest{}, ErrNotFound
		}
		return AccessRequest{}, err
	}
	if request.Status != AccessRequestPending {
		return AccessRequest{}, fmt.Errorf("%w: the request was already decided", ErrRevision)
	}
	granted := request.RequestedRole
	if input.Role != RoleNone {
		if !AssignableShareRole(input.Role) {
			return AccessRequest{}, fmt.Errorf("%w: role must be viewer, commenter or editor", ErrInvalid)
		}
		granted = input.Role
	}
	status := AccessRequestDenied
	if input.Approve {
		status = AccessRequestApproved
		if _, err := r.PutWorkbookShare(ctx, request.WorkbookID, ShareInput{
			PrincipalType: PrincipalUser, PrincipalID: request.RequesterID, PrincipalLabel: request.RequesterName, Role: granted, ActorID: input.ActorID,
		}); err != nil {
			return AccessRequest{}, err
		}
	}
	err = r.pool.QueryRow(ctx, `
		UPDATE workbook_access_requests SET status=$2, decided_by=$3, decided_at=now(), requested_role=$4 WHERE id=$1
		RETURNING id::text,workbook_id::text,requester_id,requester_email,requester_name,requested_role,message,status,decided_by,decided_at,created_at`,
		requestID, status, input.ActorID, string(granted)).
		Scan(&request.ID, &request.WorkbookID, &request.RequesterID, &request.RequesterMail, &request.RequesterName, &request.RequestedRole, &request.Message, &request.Status, &request.DecidedBy, &request.DecidedAt, &request.CreatedAt)
	if err != nil {
		return AccessRequest{}, err
	}
	return request, nil
}
