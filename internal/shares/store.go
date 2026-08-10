package shares

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("share not found")

type Store interface {
	Create(ctx context.Context, s *Share) error
	GetBySlug(ctx context.Context, slug string) (*Share, error)
	ListByOwner(ctx context.Context, ownerID string) ([]*Share, error)
	Delete(ctx context.Context, slug, ownerID string) error
}

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (p *PostgresStore) Create(ctx context.Context, s *Share) error {
	_, err := p.pool.Exec(ctx, `
		insert into shares
			(slug, owner_id, owner_email, recipient_email, storage_key,
			 file_name, file_size, code_hash, code_salt, created_at, expires_at)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		s.Slug, nullable(s.OwnerID), nullable(s.OwnerEmail), nullable(s.RecipientEmail),
		s.StorageKey, s.FileName, s.FileSize, s.CodeHash, s.CodeSalt, s.CreatedAt, s.ExpiresAt)
	if err != nil {
		return fmt.Errorf("insert share: %w", err)
	}
	return nil
}

func scanShare(row pgx.Row) (*Share, error) {
	var s Share
	var ownerID, ownerEmail, recipientEmail *string
	err := row.Scan(&s.Slug, &ownerID, &ownerEmail, &recipientEmail, &s.StorageKey,
		&s.FileName, &s.FileSize, &s.CodeHash, &s.CodeSalt, &s.CreatedAt, &s.ExpiresAt)
	if err != nil {
		return nil, err
	}
	if ownerID != nil {
		s.OwnerID = *ownerID
	}
	if ownerEmail != nil {
		s.OwnerEmail = *ownerEmail
	}
	if recipientEmail != nil {
		s.RecipientEmail = *recipientEmail
	}
	return &s, nil
}

const selectColumns = `slug, owner_id, owner_email, recipient_email, storage_key,
	file_name, file_size, code_hash, code_salt, created_at, expires_at`

func (p *PostgresStore) GetBySlug(ctx context.Context, slug string) (*Share, error) {
	row := p.pool.QueryRow(ctx, `select `+selectColumns+` from shares where slug = $1`, slug)
	s, err := scanShare(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get share: %w", err)
	}
	return s, nil
}

func (p *PostgresStore) ListByOwner(ctx context.Context, ownerID string) ([]*Share, error) {
	rows, err := p.pool.Query(ctx, `select `+selectColumns+`
		from shares where owner_id = $1 order by created_at desc`, ownerID)
	if err != nil {
		return nil, fmt.Errorf("list shares: %w", err)
	}
	defer rows.Close()

	var out []*Share
	for rows.Next() {
		s, err := scanShare(rows)
		if err != nil {
			return nil, fmt.Errorf("scan share: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (p *PostgresStore) Delete(ctx context.Context, slug, ownerID string) error {
	tag, err := p.pool.Exec(ctx, `delete from shares where slug = $1 and owner_id = $2`, slug, ownerID)
	if err != nil {
		return fmt.Errorf("delete share: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
