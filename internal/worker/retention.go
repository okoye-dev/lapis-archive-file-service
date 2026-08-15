package worker

import (
	"context"
	"log"
	"time"

	"github.com/okoye-dev/lapis-archive-file-service/internal/audit"
	"github.com/okoye-dev/lapis-archive-file-service/internal/domain"
)

const (
	retentionInterval  = time.Hour
	retentionBatchSize = 200
)

type ExpiredUploadStore interface {
	ListExpired(ctx context.Context, anonBefore, ownedBefore time.Time, limit int) ([]*domain.Upload, error)
	Delete(ctx context.Context, storageKey string) error
	MarkDeleteFailed(ctx context.Context, storageKey, errMsg string) error
}

// ShareRemover removes a file's shares when its object is deleted.
type ShareRemover interface {
	DeleteByStorageKey(ctx context.Context, storageKey string) ([]string, error)
}

// PurgedUpload is the audit snapshot kept after the row is gone.
type PurgedUpload struct {
	StorageKey        string    `json:"storage_key"`
	OwnerID           string    `json:"owner_id,omitempty"`
	FileName          string    `json:"file_name"`
	SizeBytes         int64     `json:"size_bytes"`
	CreatedAt         time.Time `json:"created_at"`
	DeletedShareSlugs []string  `json:"deleted_share_slugs,omitempty"`
}

type PurgeFailure struct {
	Attempt int    `json:"attempt"`
	Error   string `json:"error"`
}

// PurgeExpiredUploads deletes uploads past their window (AnonTTL / OwnedTTL)
// along with any shares of the file.
type PurgeExpiredUploads struct {
	Store   ExpiredUploadStore
	Shares  ShareRemover
	Objects ObjectDeleter
	Auditor audit.Auditor

	AnonTTL  time.Duration
	OwnedTTL time.Duration
}

func (PurgeExpiredUploads) Name() string            { return "purge-expired-uploads" }
func (PurgeExpiredUploads) Interval() time.Duration { return retentionInterval }

func (j PurgeExpiredUploads) Run(ctx context.Context) error {
	now := time.Now().UTC()
	expired, err := j.Store.ListExpired(ctx, now.Add(-j.AnonTTL), now.Add(-j.OwnedTTL), retentionBatchSize)
	if err != nil {
		return err
	}

	deleted, failed := 0, 0
	for _, up := range expired {
		if err := j.Objects.DeleteFile(ctx, up.StorageKey); err != nil {
			failed++
			log.Printf("retention: delete object %s (attempt %d): %v", up.StorageKey, up.DeleteAttempts+1, err)
			if merr := j.Store.MarkDeleteFailed(ctx, up.StorageKey, err.Error()); merr != nil {
				log.Printf("retention: mark failed %s: %v", up.StorageKey, merr)
			}
			if aerr := j.Auditor.Record(ctx, "purge_upload_failed", up.StorageKey,
				PurgeFailure{Attempt: up.DeleteAttempts + 1, Error: err.Error()}); aerr != nil {
				log.Printf("retention: audit failure %s: %v", up.StorageKey, aerr)
			}
			continue
		}

		slugs, err := j.Shares.DeleteByStorageKey(ctx, up.StorageKey)
		if err != nil {
			// Object's already gone; a stray share row just 404s until the
			// share purge clears it.
			log.Printf("retention: delete shares for %s: %v", up.StorageKey, err)
		}
		if err := j.Store.Delete(ctx, up.StorageKey); err != nil {
			failed++
			log.Printf("retention: delete row %s: %v", up.StorageKey, err)
			continue
		}

		record := PurgedUpload{
			StorageKey:        up.StorageKey,
			OwnerID:           up.OwnerID,
			FileName:          up.FileName,
			SizeBytes:         up.SizeBytes,
			CreatedAt:         up.CreatedAt,
			DeletedShareSlugs: slugs,
		}
		if err := j.Auditor.Record(ctx, "purge_upload", up.StorageKey, record); err != nil {
			log.Printf("retention: audit %s: %v", up.StorageKey, err)
		}
		deleted++
	}

	if len(expired) > 0 {
		log.Printf("retention: deleted %d upload(s), %d failed", deleted, failed)
	}
	return nil
}
