package workbook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"kanpic/pkg/identity"
)

type commentScanner interface{ Scan(...any) error }

const commentThreadSelect = `
	SELECT t.id::text,t.workbook_id::text,t.sheet_id::text,s.name,t.cell_range,t.idempotency_key,
	       t.resolved,t.revision,t.created_by,t.resolved_by,t.resolved_at,t.created_at,t.updated_at,
	       COALESCE(jsonb_agg(jsonb_build_object(
	         'id',m.id::text,'thread_id',m.thread_id::text,'author_id',m.author_id,
	         'content',m.content,'mentions',m.mentions,'revision',m.revision,
	         'deleted_at',m.deleted_at,'created_at',m.created_at,'updated_at',m.updated_at
	       ) ORDER BY m.created_at,m.id) FILTER (WHERE m.id IS NOT NULL),'[]'::jsonb)
	FROM comment_threads t
	JOIN workbooks w ON w.id=t.workbook_id AND w.deleted_at IS NULL
	JOIN sheets s ON s.id=t.sheet_id
	LEFT JOIN comment_messages m ON m.thread_id=t.id`

const commentThreadGroup = ` GROUP BY t.id,s.name`

func scanCommentThread(row commentScanner) (CommentThread, error) {
	var thread CommentThread
	var messages []byte
	if err := row.Scan(&thread.ID, &thread.WorkbookID, &thread.SheetID, &thread.SheetName, &thread.Range, &thread.IdempotencyKey, &thread.Resolved, &thread.Revision, &thread.CreatedBy, &thread.ResolvedBy, &thread.ResolvedAt, &thread.CreatedAt, &thread.UpdatedAt, &messages); err != nil {
		return CommentThread{}, err
	}
	if err := json.Unmarshal(messages, &thread.Messages); err != nil {
		return CommentThread{}, err
	}
	if thread.Messages == nil {
		thread.Messages = []CommentMessage{}
	}
	return thread, nil
}

func getCommentThreadTx(ctx context.Context, tx pgx.Tx, id string) (CommentThread, error) {
	thread, err := scanCommentThread(tx.QueryRow(ctx, commentThreadSelect+` WHERE t.id=$1`+commentThreadGroup, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return CommentThread{}, ErrNotFound
	}
	return thread, err
}

func (r *PostgresRepository) CreateCommentThread(ctx context.Context, workbookID, actorID string, input CreateCommentThreadInput) (CommentThread, error) {
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.IdempotencyKey == "" {
		return CommentThread{}, fmt.Errorf("%w: idempotency_key is required", ErrInvalid)
	}
	rangeValue, err := normalizeCommentRange(input.Range)
	if err != nil {
		return CommentThread{}, err
	}
	content, mentions, err := normalizeCommentContent(input.Content)
	if err != nil {
		return CommentThread{}, err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return CommentThread{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, workbookID+":comment-create"); err != nil {
		return CommentThread{}, err
	}
	var existingID string
	if err := tx.QueryRow(ctx, `SELECT id::text FROM comment_threads WHERE workbook_id=$1 AND created_by=$2 AND idempotency_key=$3`, workbookID, actorID, input.IdempotencyKey).Scan(&existingID); err == nil {
		return getCommentThreadTx(ctx, tx, existingID)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return CommentThread{}, err
	}
	var threadCount int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM comment_threads WHERE workbook_id=$1`, workbookID).Scan(&threadCount); err != nil {
		return CommentThread{}, err
	}
	if threadCount >= MaxCommentThreads {
		return CommentThread{}, fmt.Errorf("%w: a workbook may contain up to %d comment threads", ErrInvalid, MaxCommentThreads)
	}
	var sheetName string
	if err := tx.QueryRow(ctx, `SELECT s.name FROM sheets s JOIN workbooks w ON w.id=s.workbook_id AND w.deleted_at IS NULL WHERE s.id=$1 AND s.workbook_id=$2`, input.SheetID, workbookID).Scan(&sheetName); errors.Is(err, pgx.ErrNoRows) {
		return CommentThread{}, ErrNotFound
	} else if err != nil {
		return CommentThread{}, err
	}
	now := r.now()
	thread := CommentThread{ID: identity.New(), WorkbookID: workbookID, SheetID: input.SheetID, SheetName: sheetName, Range: rangeValue, IdempotencyKey: input.IdempotencyKey, Revision: 1, CreatedBy: actorID, CreatedAt: now, UpdatedAt: now}
	message := CommentMessage{ID: identity.New(), ThreadID: thread.ID, AuthorID: actorID, IdempotencyKey: input.IdempotencyKey, Content: content, Mentions: mentions, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if _, err := tx.Exec(ctx, `INSERT INTO comment_threads(id,workbook_id,sheet_id,cell_range,idempotency_key,revision,created_by,created_at,updated_at) VALUES($1,$2,$3,$4,$5,1,$6,$7,$7)`, thread.ID, workbookID, input.SheetID, rangeValue, input.IdempotencyKey, actorID, now); err != nil {
		return CommentThread{}, mapPostgresError(err)
	}
	if err := insertCommentMessageTx(ctx, tx, message); err != nil {
		return CommentThread{}, err
	}
	if err := replaceMentionNotificationsTx(ctx, tx, thread, message); err != nil {
		return CommentThread{}, err
	}
	thread.Messages = []CommentMessage{message}
	if err := tx.Commit(ctx); err != nil {
		return CommentThread{}, err
	}
	return thread, nil
}

func (r *PostgresRepository) ListCommentThreads(ctx context.Context, workbookID, sheetID string, includeResolved bool) ([]CommentThread, error) {
	var exists bool
	if sheetID == "" {
		if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM workbooks WHERE id=$1 AND deleted_at IS NULL)`, workbookID).Scan(&exists); err != nil {
			return nil, err
		}
	} else if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sheets s JOIN workbooks w ON w.id=s.workbook_id AND w.deleted_at IS NULL WHERE s.id=$1 AND s.workbook_id=$2)`, sheetID, workbookID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	query := commentThreadSelect + ` WHERE t.workbook_id=$1 AND ($2='' OR t.sheet_id::text=$2) AND ($3 OR NOT t.resolved)` + commentThreadGroup + ` ORDER BY t.updated_at DESC LIMIT $4`
	rows, err := r.pool.Query(ctx, query, workbookID, sheetID, includeResolved, MaxCommentThreads)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]CommentThread, 0)
	for rows.Next() {
		thread, err := scanCommentThread(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, thread)
	}
	return items, rows.Err()
}

func (r *PostgresRepository) GetCommentThread(ctx context.Context, id string) (CommentThread, error) {
	thread, err := scanCommentThread(r.pool.QueryRow(ctx, commentThreadSelect+` WHERE t.id=$1`+commentThreadGroup, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return CommentThread{}, ErrNotFound
	}
	return thread, err
}

func (r *PostgresRepository) AddCommentReply(ctx context.Context, threadID, actorID string, input CreateCommentReplyInput) (CommentThread, error) {
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.IdempotencyKey == "" {
		return CommentThread{}, fmt.Errorf("%w: idempotency_key is required", ErrInvalid)
	}
	content, mentions, err := normalizeCommentContent(input.Content)
	if err != nil {
		return CommentThread{}, err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return CommentThread{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, threadID+":comment-reply"); err != nil {
		return CommentThread{}, err
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM comment_threads t JOIN workbooks w ON w.id=t.workbook_id AND w.deleted_at IS NULL WHERE t.id=$1)`, threadID).Scan(&exists); err != nil {
		return CommentThread{}, err
	}
	if !exists {
		return CommentThread{}, ErrNotFound
	}
	var existingID string
	if err := tx.QueryRow(ctx, `SELECT id::text FROM comment_messages WHERE thread_id=$1 AND author_id=$2 AND idempotency_key=$3`, threadID, actorID, input.IdempotencyKey).Scan(&existingID); err == nil {
		return getCommentThreadTx(ctx, tx, threadID)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return CommentThread{}, err
	}
	var messageCount int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM comment_messages WHERE thread_id=$1`, threadID).Scan(&messageCount); err != nil {
		return CommentThread{}, err
	}
	if messageCount >= MaxCommentMessages {
		return CommentThread{}, fmt.Errorf("%w: a comment thread may contain up to %d messages", ErrInvalid, MaxCommentMessages)
	}
	now := r.now()
	message := CommentMessage{ID: identity.New(), ThreadID: threadID, AuthorID: actorID, IdempotencyKey: input.IdempotencyKey, Content: content, Mentions: mentions, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := insertCommentMessageTx(ctx, tx, message); err != nil {
		return CommentThread{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE comment_threads SET resolved=false,resolved_by='',resolved_at=NULL,revision=revision+1,updated_at=$2 WHERE id=$1`, threadID, now); err != nil {
		return CommentThread{}, err
	}
	thread, err := getCommentThreadTx(ctx, tx, threadID)
	if err != nil {
		return CommentThread{}, err
	}
	if err := replaceMentionNotificationsTx(ctx, tx, thread, message); err != nil {
		return CommentThread{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CommentThread{}, err
	}
	return thread, nil
}

func (r *PostgresRepository) UpdateCommentMessage(ctx context.Context, messageID, actorID string, input UpdateCommentMessageInput) (CommentThread, error) {
	content, mentions, err := normalizeCommentContent(input.Content)
	if err != nil {
		return CommentThread{}, err
	}
	if input.ExpectedRevision < 1 {
		return CommentThread{}, fmt.Errorf("%w: expected_revision is required", ErrInvalid)
	}
	return r.mutateCommentMessage(ctx, messageID, actorID, input.ExpectedRevision, func(message *CommentMessage, now time.Time) {
		message.Content, message.Mentions, message.DeletedAt = content, mentions, nil
	})
}

func (r *PostgresRepository) DeleteCommentMessage(ctx context.Context, messageID, actorID string, expectedRevision int64) (CommentThread, error) {
	if expectedRevision < 1 {
		return CommentThread{}, fmt.Errorf("%w: expected_revision is required", ErrInvalid)
	}
	return r.mutateCommentMessage(ctx, messageID, actorID, expectedRevision, func(message *CommentMessage, now time.Time) {
		message.Content, message.Mentions, message.DeletedAt = "", []string{}, &now
	})
}

func (r *PostgresRepository) mutateCommentMessage(ctx context.Context, messageID, actorID string, expectedRevision int64, apply func(*CommentMessage, time.Time)) (CommentThread, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return CommentThread{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var message CommentMessage
	err = tx.QueryRow(ctx, `SELECT m.id::text,m.thread_id::text,m.author_id,m.idempotency_key,m.content,m.mentions,m.revision,m.deleted_at,m.created_at,m.updated_at FROM comment_messages m JOIN comment_threads t ON t.id=m.thread_id JOIN workbooks w ON w.id=t.workbook_id AND w.deleted_at IS NULL WHERE m.id=$1 AND m.author_id=$2 FOR UPDATE OF m`, messageID, actorID).Scan(&message.ID, &message.ThreadID, &message.AuthorID, &message.IdempotencyKey, &message.Content, &message.Mentions, &message.Revision, &message.DeletedAt, &message.CreatedAt, &message.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && message.DeletedAt != nil) {
		return CommentThread{}, ErrNotFound
	}
	if err != nil {
		return CommentThread{}, err
	}
	if message.Revision != expectedRevision {
		return CommentThread{}, ErrRevision
	}
	now := r.now()
	apply(&message, now)
	message.Revision++
	message.UpdatedAt = now
	if _, err := tx.Exec(ctx, `UPDATE comment_messages SET content=$2,mentions=$3,revision=$4,deleted_at=$5,updated_at=$6 WHERE id=$1`, message.ID, message.Content, message.Mentions, message.Revision, message.DeletedAt, now); err != nil {
		return CommentThread{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE comment_threads SET revision=revision+1,updated_at=$2 WHERE id=$1`, message.ThreadID, now); err != nil {
		return CommentThread{}, err
	}
	thread, err := getCommentThreadTx(ctx, tx, message.ThreadID)
	if err != nil {
		return CommentThread{}, err
	}
	if err := replaceMentionNotificationsTx(ctx, tx, thread, message); err != nil {
		return CommentThread{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CommentThread{}, err
	}
	return thread, nil
}

func (r *PostgresRepository) UpdateCommentThread(ctx context.Context, threadID, actorID string, input UpdateCommentThreadInput) (CommentThread, error) {
	if input.ExpectedRevision < 1 || input.Resolved == nil {
		return CommentThread{}, fmt.Errorf("%w: resolved and expected_revision are required", ErrInvalid)
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return CommentThread{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var currentRevision int64
	if err := tx.QueryRow(ctx, `SELECT t.revision FROM comment_threads t JOIN workbooks w ON w.id=t.workbook_id AND w.deleted_at IS NULL WHERE t.id=$1 FOR UPDATE OF t`, threadID).Scan(&currentRevision); errors.Is(err, pgx.ErrNoRows) {
		return CommentThread{}, ErrNotFound
	} else if err != nil {
		return CommentThread{}, err
	}
	if currentRevision != input.ExpectedRevision {
		return CommentThread{}, ErrRevision
	}
	now := r.now()
	var resolvedBy string
	var resolvedAt *time.Time
	if *input.Resolved {
		resolvedBy, resolvedAt = actorID, &now
	}
	if _, err := tx.Exec(ctx, `UPDATE comment_threads SET resolved=$2,revision=revision+1,resolved_by=$3,resolved_at=$4,updated_at=$5 WHERE id=$1`, threadID, *input.Resolved, resolvedBy, resolvedAt, now); err != nil {
		return CommentThread{}, err
	}
	thread, err := getCommentThreadTx(ctx, tx, threadID)
	if err != nil {
		return CommentThread{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CommentThread{}, err
	}
	return thread, nil
}

func (r *PostgresRepository) DeleteCommentThread(ctx context.Context, threadID, actorID string) error {
	command, err := r.pool.Exec(ctx, `DELETE FROM comment_threads t USING workbooks w WHERE t.id=$1 AND t.created_by=$2 AND w.id=t.workbook_id AND w.deleted_at IS NULL`, threadID, actorID)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *PostgresRepository) ListMentionNotifications(ctx context.Context, aliases []string, unreadOnly bool, limit int) ([]MentionNotification, error) {
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 200 {
		return nil, fmt.Errorf("%w: notification limit must be between 1 and 200", ErrInvalid)
	}
	allowed := aliasesSet(aliases)
	if len(allowed) == 0 {
		return []MentionNotification{}, nil
	}
	normalized := make([]string, 0, len(allowed))
	for alias := range allowed {
		normalized = append(normalized, alias)
	}
	rows, err := r.pool.Query(ctx, `SELECT n.id::text,n.recipient,n.actor_id,n.workbook_id::text,n.sheet_id::text,s.name,n.thread_id::text,n.message_id::text,n.cell_range,n.read_at,n.created_at FROM mention_notifications n JOIN workbooks w ON w.id=n.workbook_id AND w.deleted_at IS NULL JOIN sheets s ON s.id=n.sheet_id WHERE lower(n.recipient)=ANY($1::text[]) AND ($2=false OR n.read_at IS NULL) ORDER BY n.created_at DESC LIMIT $3`, normalized, unreadOnly, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]MentionNotification, 0)
	for rows.Next() {
		var item MentionNotification
		if err := rows.Scan(&item.ID, &item.Recipient, &item.ActorID, &item.WorkbookID, &item.SheetID, &item.SheetName, &item.ThreadID, &item.MessageID, &item.Range, &item.ReadAt, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *PostgresRepository) MarkMentionNotificationRead(ctx context.Context, id string, aliases []string) (MentionNotification, error) {
	allowed := aliasesSet(aliases)
	normalized := make([]string, 0, len(allowed))
	for alias := range allowed {
		normalized = append(normalized, alias)
	}
	var item MentionNotification
	err := r.pool.QueryRow(ctx, `UPDATE mention_notifications n SET read_at=COALESCE(n.read_at,now()) WHERE n.id=$1 AND lower(n.recipient)=ANY($2::text[]) AND EXISTS(SELECT 1 FROM workbooks w WHERE w.id=n.workbook_id AND w.deleted_at IS NULL) RETURNING n.id::text,n.recipient,n.actor_id,n.workbook_id::text,n.sheet_id::text,n.thread_id::text,n.message_id::text,n.cell_range,n.read_at,n.created_at`, id, normalized).Scan(&item.ID, &item.Recipient, &item.ActorID, &item.WorkbookID, &item.SheetID, &item.ThreadID, &item.MessageID, &item.Range, &item.ReadAt, &item.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return MentionNotification{}, ErrNotFound
	}
	if err != nil {
		return MentionNotification{}, err
	}
	_ = r.pool.QueryRow(ctx, `SELECT name FROM sheets WHERE id=$1`, item.SheetID).Scan(&item.SheetName)
	return item, nil
}

func insertCommentMessageTx(ctx context.Context, tx pgx.Tx, message CommentMessage) error {
	_, err := tx.Exec(ctx, `INSERT INTO comment_messages(id,thread_id,author_id,idempotency_key,content,mentions,revision,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,1,$7,$7)`, message.ID, message.ThreadID, message.AuthorID, message.IdempotencyKey, message.Content, message.Mentions, message.CreatedAt)
	return mapPostgresError(err)
}

func replaceMentionNotificationsTx(ctx context.Context, tx pgx.Tx, thread CommentThread, message CommentMessage) error {
	if _, err := tx.Exec(ctx, `DELETE FROM mention_notifications WHERE message_id=$1`, message.ID); err != nil {
		return err
	}
	if message.DeletedAt != nil {
		return nil
	}
	for _, recipient := range message.Mentions {
		if strings.EqualFold(recipient, message.AuthorID) {
			continue
		}
		if _, err := tx.Exec(ctx, `INSERT INTO mention_notifications(id,recipient,actor_id,workbook_id,sheet_id,thread_id,message_id,cell_range,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, identity.New(), recipient, message.AuthorID, thread.WorkbookID, thread.SheetID, thread.ID, message.ID, thread.Range, message.UpdatedAt); err != nil {
			return mapPostgresError(err)
		}
	}
	return nil
}
