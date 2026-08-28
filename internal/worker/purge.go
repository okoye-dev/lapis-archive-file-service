package worker

import (
	"context"
	"log"
	"time"

	"github.com/okoye-dev/lapis-archive-file-service/internal/audit"
	"github.com/okoye-dev/lapis-archive-file-service/internal/domain"
)

// Shared cadence for the retention and purge sweeps.
const (
	sweepInterval  = time.Hour
	sweepBatchSize = 200
)

// ExpiredShareStore is the slice of the share store the purge job needs.
type ExpiredShareStore interface {
	ListExpired(ctx context.Context, limit int) ([]*domain.Share, error)
	DeleteBySlug(ctx context.Context, slug string) error
}

// PurgedShare is the snapshot written to the audit trail when a share is
// removed, so "what existed and was deleted" is answerable after the row is
// gone. Deliberately excludes the code hash/salt.
type PurgedShare struct {
	Slug       string    `json:"slug"`
	StorageKey string    `json:"storage_key"`
	FileName   string    `json:"file_name"`
	FileSize   int64     `json:"file_size"`
	OwnerEmail string    `json:"owner_email,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// PurgeExpiredShares removes shares past their expiry: it deletes the share
// row and records each removal in the audit trail. The object itself is left to
// the retention worker, which owns object deletion.
type PurgeExpiredShares struct {
	Store   ExpiredShareStore
	Auditor audit.Auditor
}

func (PurgeExpiredShares) Name() string            { return "purge-expired-shares" }
func (PurgeExpiredShares) Interval() time.Duration { return sweepInterval }

func (j PurgeExpiredShares) Run(ctx context.Context) error {
	expired, err := j.Store.ListExpired(ctx, sweepBatchSize)
	if err != nil {
		return err
	}

	for _, s := range expired {
		// Capture what existed before removing it, so the audit trail can
		// answer "what was deleted" even though the row is gone.
		record := PurgedShare{
			Slug:       s.Slug,
			StorageKey: s.StorageKey,
			FileName:   s.FileName,
			FileSize:   s.FileSize,
			OwnerEmail: s.OwnerEmail,
			CreatedAt:  s.CreatedAt,
			ExpiresAt:  s.ExpiresAt,
		}

		if err := j.Store.DeleteBySlug(ctx, s.Slug); err != nil {
			log.Printf("purge: delete row %s: %v", s.Slug, err)
			continue
		}
		if err := j.Auditor.Record(ctx, "purge_share", s.Slug, record); err != nil {
			log.Printf("purge: audit %s: %v", s.Slug, err)
		}
	}

	if len(expired) > 0 {
		log.Printf("purge: removed %d expired share(s)", len(expired))
	}
	return nil
}
