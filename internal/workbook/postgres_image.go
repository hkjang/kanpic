package workbook

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"kanpic/pkg/identity"
)

const imageColumns = `i.id::text,i.workbook_id::text,i.sheet_id::text,i.idempotency_key,i.content_type,i.byte_size,i.natural_width,i.natural_height,i.position_x,i.position_y,i.width,i.height,i.revision,i.created_by,i.updated_by,i.created_at,i.updated_at,w.version`

func scanImage(row pgx.Row) (Image, error) {
	var item Image
	if err := row.Scan(&item.ID, &item.WorkbookID, &item.SheetID, &item.CreateKey, &item.ContentType, &item.ByteSize, &item.NaturalWidth, &item.NaturalHeight,
		&item.Position.X, &item.Position.Y, &item.Position.Width, &item.Position.Height, &item.Revision, &item.CreatedBy, &item.UpdatedBy, &item.CreatedAt, &item.UpdatedAt, &item.WorkbookVersion); err != nil {
		return Image{}, err
	}
	return item, nil
}

func (r *PostgresRepository) CreateImage(ctx context.Context, workbookID, actor string, input CreateImageInput) (Image, error) {
	key := strings.TrimSpace(input.IdempotencyKey)
	if key == "" {
		return Image{}, fmt.Errorf("%w: idempotency_key is required", ErrInvalid)
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Image{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, workbookID+":image:"+actor+":"+key); err != nil {
		return Image{}, err
	}
	if existing, lookupErr := scanImage(tx.QueryRow(ctx, `SELECT `+imageColumns+` FROM sheet_images i JOIN workbooks w ON w.id=i.workbook_id WHERE i.workbook_id=$1 AND i.created_by=$2 AND i.idempotency_key=$3 AND w.deleted_at IS NULL`, workbookID, actor, key)); lookupErr == nil {
		return existing, tx.Commit(ctx)
	} else if !errors.Is(lookupErr, pgx.ErrNoRows) {
		return Image{}, lookupErr
	}
	var currentVersion int64
	if err := tx.QueryRow(ctx, `SELECT version FROM workbooks WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, workbookID).Scan(&currentVersion); errors.Is(err, pgx.ErrNoRows) {
		return Image{}, ErrNotFound
	} else if err != nil {
		return Image{}, err
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM sheet_images WHERE workbook_id=$1`, workbookID).Scan(&count); err != nil {
		return Image{}, err
	}
	if count >= MaxImagesPerWorkbook {
		return Image{}, fmt.Errorf("%w: a workbook may contain at most %d images", ErrInvalid, MaxImagesPerWorkbook)
	}
	item, err := imageFromInput(workbookID, key, actor, input)
	if err != nil {
		return Image{}, err
	}
	var sheetWorkbook string
	if err := tx.QueryRow(ctx, `SELECT workbook_id::text FROM sheets WHERE id=$1`, item.SheetID).Scan(&sheetWorkbook); err != nil || sheetWorkbook != workbookID {
		return Image{}, fmt.Errorf("%w: sheet does not belong to the workbook", ErrInvalid)
	}
	now := r.now()
	item.ID, item.Revision, item.CreatedAt, item.UpdatedAt = identity.New(), 1, now, now
	if err := insertImageTx(ctx, tx, item); err != nil {
		return Image{}, err
	}
	item.WorkbookVersion = currentVersion + 1
	if _, err := tx.Exec(ctx, `UPDATE workbooks SET version=$2,updated_at=$3 WHERE id=$1`, workbookID, item.WorkbookVersion, now); err != nil {
		return Image{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Image{}, err
	}
	return cloneImage(item), nil
}

func (r *PostgresRepository) ListImages(ctx context.Context, workbookID, sheetID string) ([]Image, error) {
	query := `SELECT ` + imageColumns + ` FROM sheet_images i JOIN workbooks w ON w.id=i.workbook_id WHERE i.workbook_id=$1 AND w.deleted_at IS NULL`
	args := []any{workbookID}
	if strings.TrimSpace(sheetID) != "" {
		query += ` AND i.sheet_id=$2`
		args = append(args, strings.TrimSpace(sheetID))
	}
	rows, err := r.pool.Query(ctx, query+` ORDER BY i.created_at,i.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Image, 0)
	for rows.Next() {
		item, scanErr := scanImage(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(items) == 0 {
		var exists bool
		if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM workbooks WHERE id=$1 AND deleted_at IS NULL)`, workbookID).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			return nil, ErrNotFound
		}
	}
	return items, nil
}

func (r *PostgresRepository) GetImage(ctx context.Context, imageID string) (Image, error) {
	item, err := scanImage(r.pool.QueryRow(ctx, `SELECT `+imageColumns+` FROM sheet_images i JOIN workbooks w ON w.id=i.workbook_id WHERE i.id=$1 AND w.deleted_at IS NULL`, imageID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Image{}, ErrNotFound
	}
	return item, err
}

func (r *PostgresRepository) GetImageContent(ctx context.Context, imageID string) (Image, error) {
	item, err := r.GetImage(ctx, imageID)
	if err != nil {
		return Image{}, err
	}
	if err := r.pool.QueryRow(ctx, `SELECT data FROM sheet_images WHERE id=$1`, imageID).Scan(&item.data); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Image{}, ErrNotFound
		}
		return Image{}, err
	}
	return item, nil
}

func (r *PostgresRepository) UpdateImage(ctx context.Context, imageID, actor string, input UpdateImageInput) (Image, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Image{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	item, err := scanImage(tx.QueryRow(ctx, `SELECT `+imageColumns+` FROM sheet_images i JOIN workbooks w ON w.id=i.workbook_id WHERE i.id=$1 AND w.deleted_at IS NULL FOR UPDATE OF i`, imageID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Image{}, ErrNotFound
	} else if err != nil {
		return Image{}, err
	}
	if input.ExpectedRevision != nil && *input.ExpectedRevision != item.Revision {
		return Image{}, ErrRevision
	}
	if input.Position != nil {
		position := *input.Position
		if err := validateImagePosition(&position); err != nil {
			return Image{}, err
		}
		item.Position = position
	}
	now := r.now()
	item.Revision++
	item.UpdatedBy, item.UpdatedAt = actor, now
	if _, err := tx.Exec(ctx, `UPDATE sheet_images SET position_x=$2,position_y=$3,width=$4,height=$5,revision=$6,updated_by=$7,updated_at=$8 WHERE id=$1`,
		item.ID, item.Position.X, item.Position.Y, item.Position.Width, item.Position.Height, item.Revision, item.UpdatedBy, item.UpdatedAt); err != nil {
		return Image{}, err
	}
	if err := tx.QueryRow(ctx, `UPDATE workbooks SET version=version+1,updated_at=$2 WHERE id=$1 RETURNING version`, item.WorkbookID, now).Scan(&item.WorkbookVersion); err != nil {
		return Image{}, err
	}
	return item, tx.Commit(ctx)
}

func (r *PostgresRepository) DeleteImage(ctx context.Context, imageID, actor string, expectedRevision *int64) error {
	_ = actor
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	item, err := scanImage(tx.QueryRow(ctx, `SELECT `+imageColumns+` FROM sheet_images i JOIN workbooks w ON w.id=i.workbook_id WHERE i.id=$1 AND w.deleted_at IS NULL FOR UPDATE OF i`, imageID))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if expectedRevision != nil && *expectedRevision != item.Revision {
		return ErrRevision
	}
	if _, err := tx.Exec(ctx, `DELETE FROM sheet_images WHERE id=$1`, imageID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE workbooks SET version=version+1,updated_at=$2 WHERE id=$1`, item.WorkbookID, r.now()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func insertImageTx(ctx context.Context, tx pgx.Tx, item Image) error {
	_, err := tx.Exec(ctx, `INSERT INTO sheet_images(id,workbook_id,sheet_id,idempotency_key,content_type,byte_size,natural_width,natural_height,position_x,position_y,width,height,data,revision,created_by,updated_by,created_at,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`,
		item.ID, item.WorkbookID, item.SheetID, item.CreateKey, item.ContentType, item.ByteSize, item.NaturalWidth, item.NaturalHeight,
		item.Position.X, item.Position.Y, item.Position.Width, item.Position.Height, item.data, item.Revision, item.CreatedBy, item.UpdatedBy, item.CreatedAt, item.UpdatedAt)
	return err
}
