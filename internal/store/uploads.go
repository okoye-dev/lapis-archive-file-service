package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/okoye-dev/lapis-archive-file-service/internal/domain"
)

type UploadStore struct {
	pool *pgxpool.Pool
}

func NewUploadStore(pool *pgxpool.Pool) *UploadStore {
	return &UploadStore{pool: pool}
}

func (s *UploadStore) Create(ctx context.Context, up *domain.Upload) error {
	_, err := s.pool.Exec(ctx, `
		insert into uploads (storage_key, owner_id, file_name, size_bytes, created_at)
		values ($1,$2,$3,$4,$5)
		on conflict (storage_key) do nothing`,
		up.StorageKey, nullable(up.OwnerID), up.FileName, up.SizeBytes, up.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert upload: %w", err)
	}
	return nil
}

func (s *UploadStore) ListExpired(ctx context.Context, anonBefore, ownedBefore time.Time, limit int) ([]*domain.Upload, error) {
	rows, err := s.pool.Query(ctx, `
		select storage_key, owner_id, file_name, size_bytes, created_at, delete_attempts
		from uploads
		where (owner_id is null and created_at < $1)
		   or (owner_id is not null and created_at < $2)
		order by created_at asc
		limit $3`, anonBefore, ownedBefore, limit)
	if err != nil {
		return nil, fmt.Errorf("list expired uploads: %w", err)
	}
	defer rows.Close()

	var out []*domain.Upload
	for rows.Next() {
		var up domain.Upload
		var ownerID *string
		if err := rows.Scan(&up.StorageKey, &ownerID, &up.FileName, &up.SizeBytes,
			&up.CreatedAt, &up.DeleteAttempts); err != nil {
			return nil, fmt.Errorf("scan upload: %w", err)
		}
		if ownerID != nil {
			up.OwnerID = *ownerID
		}
		out = append(out, &up)
	}
	return out, rows.Err()
}

func (s *UploadStore) GetByStorageKey(ctx context.Context, storageKey string) (*domain.Upload, error) {
	row := s.pool.QueryRow(ctx, `
		select storage_key, owner_id, file_name, size_bytes, created_at, delete_attempts
		from uploads where storage_key = $1`, storageKey)

	var up domain.Upload
	var ownerID *string
	err := row.Scan(&up.StorageKey, &ownerID, &up.FileName, &up.SizeBytes, &up.CreatedAt, &up.DeleteAttempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get upload: %w", err)
	}
	if ownerID != nil {
		up.OwnerID = *ownerID
	}
	return &up, nil
}

func (s *UploadStore) Delete(ctx context.Context, storageKey string) error {
	if _, err := s.pool.Exec(ctx, `delete from uploads where storage_key = $1`, storageKey); err != nil {
		return fmt.Errorf("delete upload: %w", err)
	}
	return nil
}

// MarkDeleteFailed bumps the retry counter and keeps the row for next run.
func (s *UploadStore) MarkDeleteFailed(ctx context.Context, storageKey, errMsg string) error {
	if _, err := s.pool.Exec(ctx, `
		update uploads
		set delete_attempts = delete_attempts + 1, last_delete_error = $2
		where storage_key = $1`, storageKey, errMsg); err != nil {
		return fmt.Errorf("mark delete failed: %w", err)
	}
	return nil
}
