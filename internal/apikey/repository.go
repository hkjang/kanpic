package apikey

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"kanpic/pkg/identity"
)

type Key struct {
	ID         string     `json:"id"`
	UserID     string     `json:"user_id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Scopes     []string   `json:"scopes"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

type CreatedKey struct {
	Key
	Secret string `json:"secret"`
}

type CreateInput struct {
	Name      string     `json:"name"`
	Scopes    []string   `json:"scopes"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type UpdateInput struct {
	Name      *string     `json:"name,omitempty"`
	Scopes    *[]string   `json:"scopes,omitempty"`
	ExpiresAt **time.Time `json:"expires_at,omitempty"`
}

// UnmarshalJSON preserves the distinction between an omitted expires_at and
// an explicit null, which clears an existing expiration during PATCH.
func (input *UpdateInput) UnmarshalJSON(data []byte) error {
	var raw struct {
		Name      *string         `json:"name"`
		Scopes    *[]string       `json:"scopes"`
		ExpiresAt json.RawMessage `json:"expires_at"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	input.Name, input.Scopes = raw.Name, raw.Scopes
	if raw.ExpiresAt == nil {
		input.ExpiresAt = nil
		return nil
	}
	if string(raw.ExpiresAt) == "null" {
		var expiresAt *time.Time
		input.ExpiresAt = &expiresAt
		return nil
	}
	var expiresAt time.Time
	if err := json.Unmarshal(raw.ExpiresAt, &expiresAt); err != nil {
		return fmt.Errorf("invalid expires_at: %w", err)
	}
	pointer := &expiresAt
	input.ExpiresAt = &pointer
	return nil
}

type Principal struct {
	UserID string
	KeyID  string
	Scopes map[string]struct{}
}

type Repository struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

func (r *Repository) Create(ctx context.Context, userID string, input CreateInput) (CreatedKey, error) {
	secret, hash, prefix, err := makeSecret()
	if err != nil {
		return CreatedKey{}, err
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		return CreatedKey{}, errors.New("key name is required")
	}
	input.Scopes = normalizedScopes(input.Scopes)
	if len(input.Scopes) == 0 {
		return CreatedKey{}, errors.New("at least one scope is required")
	}
	now := time.Now().UTC()
	item := Key{ID: identity.New(), UserID: userID, Name: input.Name, Prefix: prefix, Scopes: input.Scopes, ExpiresAt: input.ExpiresAt, CreatedAt: now, UpdatedAt: now}
	_, err = r.pool.Exec(ctx, `INSERT INTO api_keys(id,user_id,name,key_prefix,key_hash,scopes,expires_at,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$8)`, item.ID, userID, item.Name, prefix, hash[:], item.Scopes, item.ExpiresAt, now)
	if err != nil {
		return CreatedKey{}, err
	}
	return CreatedKey{Key: item, Secret: secret}, nil
}

func (r *Repository) List(ctx context.Context, userID string, all bool) ([]Key, error) {
	query := `SELECT id::text,user_id,name,key_prefix,scopes,expires_at,last_used_at,revoked_at,created_at,updated_at FROM api_keys`
	args := []any{}
	if !all {
		query += ` WHERE user_id=$1`
		args = append(args, userID)
	}
	query += ` ORDER BY created_at DESC`
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Key, 0)
	for rows.Next() {
		item, err := scanKey(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *Repository) Update(ctx context.Context, id, userID string, input UpdateInput, admin bool) (Key, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Key{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	query := `SELECT id::text,user_id,name,key_prefix,scopes,expires_at,last_used_at,revoked_at,created_at,updated_at FROM api_keys WHERE id=$1`
	args := []any{id}
	if !admin {
		query += ` AND user_id=$2`
		args = append(args, userID)
	}
	query += ` FOR UPDATE`
	item, err := scanKey(tx.QueryRow(ctx, query, args...))
	if err != nil {
		return Key{}, err
	}
	if input.Name != nil {
		item.Name = strings.TrimSpace(*input.Name)
		if item.Name == "" {
			return Key{}, errors.New("key name cannot be empty")
		}
	}
	if input.Scopes != nil {
		item.Scopes = normalizedScopes(*input.Scopes)
		if len(item.Scopes) == 0 {
			return Key{}, errors.New("at least one scope is required")
		}
	}
	if input.ExpiresAt != nil {
		item.ExpiresAt = *input.ExpiresAt
	}
	item.UpdatedAt = time.Now().UTC()
	if _, err := tx.Exec(ctx, `UPDATE api_keys SET name=$2,scopes=$3,expires_at=$4,updated_at=$5 WHERE id=$1`, id, item.Name, item.Scopes, item.ExpiresAt, item.UpdatedAt); err != nil {
		return Key{}, err
	}
	return item, tx.Commit(ctx)
}

func (r *Repository) Revoke(ctx context.Context, id, userID string, admin bool) error {
	query := `UPDATE api_keys SET revoked_at=now(),updated_at=now() WHERE id=$1 AND revoked_at IS NULL`
	args := []any{id}
	if !admin {
		query += ` AND user_id=$2`
		args = append(args, userID)
	}
	command, err := r.pool.Exec(ctx, query, args...)
	if err == nil && command.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return err
}

func (r *Repository) Rotate(ctx context.Context, id, userID string, admin bool) (CreatedKey, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return CreatedKey{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	query := `SELECT id::text,user_id,name,key_prefix,scopes,expires_at,last_used_at,revoked_at,created_at,updated_at FROM api_keys WHERE id=$1 AND revoked_at IS NULL`
	args := []any{id}
	if !admin {
		query += ` AND user_id=$2`
		args = append(args, userID)
	}
	query += ` FOR UPDATE`
	old, err := scanKey(tx.QueryRow(ctx, query, args...))
	if err != nil {
		return CreatedKey{}, err
	}
	secret, hash, prefix, err := makeSecret()
	if err != nil {
		return CreatedKey{}, err
	}
	now := time.Now().UTC()
	item := Key{ID: identity.New(), UserID: old.UserID, Name: old.Name, Prefix: prefix, Scopes: old.Scopes, ExpiresAt: old.ExpiresAt, CreatedAt: now, UpdatedAt: now}
	if _, err := tx.Exec(ctx, `UPDATE api_keys SET revoked_at=$2,updated_at=$2 WHERE id=$1`, id, now); err != nil {
		return CreatedKey{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO api_keys(id,user_id,name,key_prefix,key_hash,scopes,expires_at,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$8)`, item.ID, item.UserID, item.Name, prefix, hash[:], item.Scopes, item.ExpiresAt, now); err != nil {
		return CreatedKey{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CreatedKey{}, err
	}
	return CreatedKey{Key: item, Secret: secret}, nil
}

func (r *Repository) Authenticate(ctx context.Context, secret string) (Principal, error) {
	hash := sha256.Sum256([]byte(secret))
	var principal Principal
	var scopes []string
	err := r.pool.QueryRow(ctx, `UPDATE api_keys SET last_used_at=now() WHERE key_hash=$1 AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at>now()) RETURNING user_id,id::text,scopes`, hash[:]).Scan(&principal.UserID, &principal.KeyID, &scopes)
	if err != nil {
		return Principal{}, err
	}
	principal.Scopes = make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		principal.Scopes[scope] = struct{}{}
	}
	return principal, nil
}

// NewPrincipal builds a scoped principal that is not backed by a stored key,
// such as an OAuth access token that carries kanpic scopes.
func NewPrincipal(userID string, scopes []string) Principal {
	principal := Principal{UserID: userID, Scopes: make(map[string]struct{}, len(scopes))}
	for _, scope := range normalizedScopes(scopes) {
		principal.Scopes[scope] = struct{}{}
	}
	return principal
}

// scopeAliases are scope names people grant that kanpic splits more finely.
// "Write to the spreadsheet" is how the request reads in a Keycloak client
// scope or a ticket; kanpic checks range.write for values and formula.write for
// formulas, and the alias stands for both.
var scopeAliases = map[string][]string{
	"spreadsheet.read":  {"workbook.read", "range.read", "formula.read"},
	"spreadsheet.write": {"range.write", "formula.write"},
}

func (p Principal) Allows(required string) bool {
	if required == "" {
		return true
	}
	if p.holds(required) {
		return true
	}
	for alias, expands := range scopeAliases {
		if !p.holds(alias) {
			continue
		}
		for _, scope := range expands {
			if scope == required {
				return true
			}
		}
	}
	return false
}

// holds is the scope check without aliases: the scope itself, a wildcard over
// its family, or everything.
func (p Principal) holds(required string) bool {
	if _, ok := p.Scopes["*"]; ok {
		return true
	}
	if _, ok := p.Scopes[required]; ok {
		return true
	}
	parts := strings.Split(required, ".")
	for i := len(parts) - 1; i > 0; i-- {
		if _, ok := p.Scopes[strings.Join(parts[:i], ".")+".*"]; ok {
			return true
		}
	}
	return false
}

// WithScopes returns a copy of the principal that also holds the given scopes.
// The original is left alone, so a widening made for one call ends with it.
func (p Principal) WithScopes(extra ...string) Principal {
	widened := Principal{UserID: p.UserID, KeyID: p.KeyID, Scopes: make(map[string]struct{}, len(p.Scopes)+len(extra))}
	for scope := range p.Scopes {
		widened.Scopes[scope] = struct{}{}
	}
	for _, scope := range normalizedScopes(extra) {
		widened.Scopes[scope] = struct{}{}
	}
	return widened
}

type keyScanner interface{ Scan(...any) error }

func scanKey(row keyScanner) (Key, error) {
	var item Key
	err := row.Scan(&item.ID, &item.UserID, &item.Name, &item.Prefix, &item.Scopes, &item.ExpiresAt, &item.LastUsedAt, &item.RevokedAt, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func makeSecret() (string, [32]byte, string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", [32]byte{}, "", fmt.Errorf("generate API key: %w", err)
	}
	secret := "kp_live_" + base64.RawURLEncoding.EncodeToString(bytes)
	hash := sha256.Sum256([]byte(secret))
	return secret, hash, secret[:15], nil
}

func normalizedScopes(scopes []string) []string {
	seen := make(map[string]struct{})
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope != "" {
			seen[scope] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for scope := range seen {
		result = append(result, scope)
	}
	sort.Strings(result)
	return result
}

// DefaultOwnerScopes is what an MCP caller may do on a workbook its own user
// owns even when its API key or OAuth token was narrowed to less. A narrowed
// credential is there to keep an agent away from what other people shared with
// its user; on the user's own documents it would only stop the agent editing
// cells, formats and automations the user can edit anyway. Administrators
// change the list with the mcp.owner_scopes setting.
var DefaultOwnerScopes = []string{
	"workbook.read", "range.read", "range.write", "formula.read", "formula.write",
	"format.read", "format.write", "automation.read", "automation.write", "automation.run",
	"chart.read", "chart.write", "pivot.read", "pivot.write", "comment.read", "comment.write",
	"version.read", "version.write",
}

// ownerGrantableFamilies are the scope families that act on one workbook's
// content. Anything outside them — administration, API keys, profiles,
// sharing and deleting (workbook.write), AI budget, imports — is never added by
// ownership, whatever the setting says.
var ownerGrantableFamilies = []string{"range", "formula", "format", "automation", "chart", "pivot", "comment", "version", "presentation", "spreadsheet"}

// OwnerGrantable reports whether a scope may be added on the caller's own
// workbooks.
func OwnerGrantable(scope string) bool {
	scope = strings.TrimSpace(scope)
	if scope == "workbook.read" || scope == "export.read" {
		return true
	}
	if scope == "automation.webhook.invoke" {
		return false
	}
	for _, family := range ownerGrantableFamilies {
		if strings.HasPrefix(scope, family+".") && len(scope) > len(family)+1 {
			return true
		}
	}
	return false
}
