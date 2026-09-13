package handoff

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct{ pool *pgxpool.Pool }

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore { return &PostgresStore{pool: pool} }

func (s *PostgresStore) Put(ctx context.Context, hash string, ticket Ticket) error {
	// 지난 표는 아무도 꺼내지 않으므로 새 표를 둘 때 함께 치운다. 문서를
	// 통째로 들고 있는 줄이라 쌓이게 두면 안 된다.
	_, _ = s.pool.Exec(ctx, `DELETE FROM handoff_claims WHERE expires_at < now()`)
	data := ticket.Data
	if data == nil {
		data = []byte{}
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO handoff_claims(claim_hash,resource,format,file_name,content_type,data,issued_by,expires_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
		hash, ticket.Resource, ticket.Format, ticket.Filename, ticket.ContentType, data, ticket.IssuedBy, ticket.ExpiresAt)
	return err
}

func (s *PostgresStore) Take(ctx context.Context, hash string, now time.Time) (Ticket, error) {
	// 꺼내는 것과 지우는 것이 한 문장이라 같은 표를 둘이 동시에 들고 와도
	// 한쪽만 받는다.
	var ticket Ticket
	err := s.pool.QueryRow(ctx, `
		DELETE FROM handoff_claims WHERE claim_hash=$1
		RETURNING resource,format,file_name,content_type,data,issued_by,expires_at`, hash).
		Scan(&ticket.Resource, &ticket.Format, &ticket.Filename, &ticket.ContentType, &ticket.Data, &ticket.IssuedBy, &ticket.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Ticket{}, ErrNotFound
	}
	if err != nil {
		return Ticket{}, err
	}
	if !now.Before(ticket.ExpiresAt) {
		return Ticket{}, ErrNotFound
	}
	return ticket, nil
}

func (s *PostgresStore) SaveReceipt(ctx context.Context, receipt Receipt) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO handoff_receipts(workbook_id,source,file_name,received_by,received_at)
		VALUES($1,$2,$3,$4,$5)
		ON CONFLICT (workbook_id) DO UPDATE SET source=EXCLUDED.source, file_name=EXCLUDED.file_name, received_by=EXCLUDED.received_by, received_at=EXCLUDED.received_at`,
		receipt.WorkbookID, receipt.Source, receipt.Filename, receipt.ReceivedBy, receipt.ReceivedAt)
	return err
}

func (s *PostgresStore) ReceiptFor(ctx context.Context, workbookID string) (Receipt, error) {
	var receipt Receipt
	err := s.pool.QueryRow(ctx, `SELECT workbook_id::text,source,file_name,received_by,received_at FROM handoff_receipts WHERE workbook_id=$1`, workbookID).
		Scan(&receipt.WorkbookID, &receipt.Source, &receipt.Filename, &receipt.ReceivedBy, &receipt.ReceivedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Receipt{}, ErrNotFound
	}
	return receipt, err
}
