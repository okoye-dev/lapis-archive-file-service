// Package store is the Postgres persistence for shares.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/okoye-dev/lapis-archive-file-service/internal/domain"
)

type ShareStore struct {
	pool *pgxpool.Pool
}

func NewShareStore(pool *pgxpool.Pool) *ShareStore {
	return &ShareStore{pool: pool}
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (s *ShareStore) Create(ctx context.Context, sh *domain.Share) error {
	_, err := s.pool.Exec(ctx, `
		insert into shares
			(slug, owner_id, owner_email, recipient_email, storage_key,
			 file_name, file_size, code_hash, code_salt, share_count, created_at, expires_at)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		sh.Slug, nullable(sh.OwnerID), nullable(sh.OwnerEmail), nullable(sh.RecipientEmail),
		sh.StorageKey, sh.FileName, sh.FileSize, sh.CodeHash, sh.CodeSalt, sh.ShareCount,
		sh.CreatedAt, sh.ExpiresAt)
	if err != nil {
		return fmt.Errorf("insert share: %w", err)
	}
	return nil
}

// RotateCode saves a rotation. owner_id only fills from empty, never overwrites.
func (s *ShareStore) RotateCode(ctx context.Context, sh *domain.Share) error {
	tag, err := s.pool.Exec(ctx, `
		update shares
		set code_hash = $1, code_salt = $2, expires_at = $3, share_count = $4,
		    recipient_email = $5, owner_id = coalesce(owner_id, $6)
		where slug = $7`,
		sh.CodeHash, sh.CodeSalt, sh.ExpiresAt, sh.ShareCount,
		nullable(sh.RecipientEmail), nullable(sh.OwnerID), sh.Slug)
	if err != nil {
		return fmt.Errorf("rotate share: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func scanShare(row pgx.Row) (*domain.Share, error) {
	var sh domain.Share
	var ownerID, ownerEmail, recipientEmail *string
	err := row.Scan(&sh.Slug, &ownerID, &ownerEmail, &recipientEmail, &sh.StorageKey,
		&sh.FileName, &sh.FileSize, &sh.CodeHash, &sh.CodeSalt, &sh.ShareCount,
		&sh.CreatedAt, &sh.ExpiresAt)
	if err != nil {
		return nil, err
	}
	if ownerID != nil {
		sh.OwnerID = *ownerID
	}
	if ownerEmail != nil {
		sh.OwnerEmail = *ownerEmail
	}
	if recipientEmail != nil {
		sh.RecipientEmail = *recipientEmail
	}
	return &sh, nil
}

const selectColumns = `slug, owner_id, owner_email, recipient_email, storage_key,
	file_name, file_size, code_hash, code_salt, share_count, created_at, expires_at`

func (s *ShareStore) GetBySlug(ctx context.Context, slug string) (*domain.Share, error) {
	row := s.pool.QueryRow(ctx, `select `+selectColumns+` from shares where slug = $1`, slug)
	sh, err := scanShare(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get share: %w", err)
	}
	return sh, nil
}

func (s *ShareStore) GetByStorageKey(ctx context.Context, storageKey string) (*domain.Share, error) {
	row := s.pool.QueryRow(ctx, `select `+selectColumns+` from shares where storage_key = $1`, storageKey)
	sh, err := scanShare(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get share by key: %w", err)
	}
	return sh, nil
}

// scanShares drains rows into shares and closes them.
func scanShares(rows pgx.Rows) ([]*domain.Share, error) {
	defer rows.Close()

	var out []*domain.Share
	for rows.Next() {
		sh, err := scanShare(rows)
		if err != nil {
			return nil, fmt.Errorf("scan share: %w", err)
		}
		out = append(out, sh)
	}
	return out, rows.Err()
}

func (s *ShareStore) ListByOwner(ctx context.Context, ownerID string) ([]*domain.Share, error) {
	rows, err := s.pool.Query(ctx, `select `+selectColumns+`
		from shares where owner_id = $1 order by created_at desc`, ownerID)
	if err != nil {
		return nil, fmt.Errorf("list shares: %w", err)
	}
	return scanShares(rows)
}

func (s *ShareStore) ListExpired(ctx context.Context, limit int) ([]*domain.Share, error) {
	rows, err := s.pool.Query(ctx, `select `+selectColumns+`
		from shares where expires_at < now() order by expires_at asc limit $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list expired: %w", err)
	}
	return scanShares(rows)
}

func (s *ShareStore) DeleteBySlug(ctx context.Context, slug string) error {
	if _, err := s.pool.Exec(ctx, `delete from shares where slug = $1`, slug); err != nil {
		return fmt.Errorf("delete share: %w", err)
	}
	return nil
}

// DeleteByStorageKey removes every share for a file and returns their slugs.
func (s *ShareStore) DeleteByStorageKey(ctx context.Context, storageKey string) ([]string, error) {
	rows, err := s.pool.Query(ctx,
		`delete from shares where storage_key = $1 returning slug`, storageKey)
	if err != nil {
		return nil, fmt.Errorf("delete shares by key: %w", err)
	}
	defer rows.Close()

	var slugs []string
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			return nil, fmt.Errorf("scan slug: %w", err)
		}
		slugs = append(slugs, slug)
	}
	return slugs, rows.Err()
}

func (s *ShareStore) Delete(ctx context.Context, slug, ownerID string) error {
	tag, err := s.pool.Exec(ctx, `delete from shares where slug = $1 and owner_id = $2`, slug, ownerID)
	if err != nil {
		return fmt.Errorf("delete share: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}
