package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/okoye-dev/lapis-archive-file-service/internal/domain"
	"github.com/okoye-dev/lapis-archive-file-service/internal/storage"
)

type fakeUploadStore struct {
	uploads    []*domain.Upload
	deleted    []string
	failed     map[string]int
	failDelete map[string]bool
}

func (f *fakeUploadStore) ListExpired(_ context.Context, anonBefore, ownedBefore time.Time, limit int) ([]*domain.Upload, error) {
	var out []*domain.Upload
	for _, u := range f.uploads {
		anon := u.OwnerID == "" && u.CreatedAt.Before(anonBefore)
		owned := u.OwnerID != "" && u.CreatedAt.Before(ownedBefore)
		if anon || owned {
			out = append(out, u)
		}
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func (f *fakeUploadStore) Delete(_ context.Context, key string) error {
	if f.failDelete[key] {
		return errors.New("db unavailable")
	}
	f.deleted = append(f.deleted, key)
	return nil
}

func (f *fakeUploadStore) MarkDeleteFailed(_ context.Context, key, _ string) error {
	if f.failed == nil {
		f.failed = make(map[string]int)
	}
	f.failed[key]++
	return nil
}

type fakeShareRemover struct {
	removed []string
	err     error
}

func (f *fakeShareRemover) DeleteByStorageKey(_ context.Context, key string) ([]string, error) {
	f.removed = append(f.removed, key)
	if f.err != nil {
		return nil, f.err
	}
	return []string{"slug-for-" + key}, nil
}

type flakyObjects struct {
	deleted  []string
	failOn   map[string]error
	notFound map[string]bool
}

func (f *flakyObjects) GetFileSize(_ context.Context, key string) (int64, error) {
	if f.notFound[key] {
		return 0, storage.ErrObjectNotFound
	}
	return 100, nil
}

func (f *flakyObjects) DeleteFile(_ context.Context, key string) error {
	if err, ok := f.failOn[key]; ok {
		return err
	}
	f.deleted = append(f.deleted, key)
	return nil
}

func newRetentionJob(store *fakeUploadStore, shares *fakeShareRemover, objects *flakyObjects, auditor *fakeAuditor) PurgeExpiredUploads {
	return PurgeExpiredUploads{
		Store:   store,
		Shares:  shares,
		Objects: objects,
		Auditor: auditor,
		// Production defaults: 3 days anonymous, 7 days owned.
		AnonTTL:  72 * time.Hour,
		OwnedTTL: 168 * time.Hour,
	}
}

func TestRetentionWindows(t *testing.T) {
	now := time.Now().UTC()
	store := &fakeUploadStore{uploads: []*domain.Upload{
		{StorageKey: "anon-old", CreatedAt: now.Add(-4 * 24 * time.Hour)},
		{StorageKey: "anon-fresh", CreatedAt: now.Add(-2 * 24 * time.Hour)},
		{StorageKey: "owned-old", OwnerID: "u1", CreatedAt: now.Add(-8 * 24 * time.Hour)},
		{StorageKey: "owned-fresh", OwnerID: "u1", CreatedAt: now.Add(-4 * 24 * time.Hour)},
	}}
	shares := &fakeShareRemover{}
	objects := &flakyObjects{}
	auditor := &fakeAuditor{}

	if err := newRetentionJob(store, shares, objects, auditor).Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}

	want := map[string]bool{"anon-old": true, "owned-old": true}
	if len(objects.deleted) != 2 || !want[objects.deleted[0]] || !want[objects.deleted[1]] {
		t.Errorf("deleted objects = %v, want anon-old + owned-old", objects.deleted)
	}
	if len(store.deleted) != 2 {
		t.Errorf("deleted rows = %v, want 2", store.deleted)
	}
	// Retention wins over live shares: both files' shares are gone too.
	if len(shares.removed) != 2 {
		t.Errorf("shares removed = %v, want 2", shares.removed)
	}
	if len(auditor.actions) != 2 {
		t.Errorf("audit records = %v, want 2 purge_upload", auditor.actions)
	}
}

func TestRetentionRetriesFailures(t *testing.T) {
	now := time.Now().UTC()
	store := &fakeUploadStore{uploads: []*domain.Upload{
		{StorageKey: "stuck", CreatedAt: now.Add(-4 * 24 * time.Hour), DeleteAttempts: 1},
		{StorageKey: "fine", CreatedAt: now.Add(-4 * 24 * time.Hour)},
	}}
	shares := &fakeShareRemover{}
	objects := &flakyObjects{failOn: map[string]error{"stuck": errors.New("bucket unavailable")}}
	auditor := &fakeAuditor{}

	if err := newRetentionJob(store, shares, objects, auditor).Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}

	if store.failed["stuck"] != 1 {
		t.Errorf("stuck attempts = %d, want 1 new mark", store.failed["stuck"])
	}
	if len(store.deleted) != 1 || store.deleted[0] != "fine" {
		t.Errorf("deleted rows = %v, want just fine", store.deleted)
	}
	// One purge_upload for the success, one purge_upload_failed for the retry.
	var ok, failed int
	for _, a := range auditor.actions {
		switch a {
		case "purge_upload":
			ok++
		case "purge_upload_failed":
			failed++
		}
	}
	if ok != 1 || failed != 1 {
		t.Errorf("audit = %v, want 1 purge_upload + 1 purge_upload_failed", auditor.actions)
	}
}

func TestRetentionShareRemovalFailureStillPurges(t *testing.T) {
	now := time.Now().UTC()
	store := &fakeUploadStore{uploads: []*domain.Upload{
		{StorageKey: "anon-old", CreatedAt: now.Add(-4 * 24 * time.Hour)},
	}}
	shares := &fakeShareRemover{err: errors.New("shares db down")}
	objects := &flakyObjects{}
	auditor := &fakeAuditor{}

	if err := newRetentionJob(store, shares, objects, auditor).Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}

	// A failed share cleanup must not block deleting the object and its row.
	if len(objects.deleted) != 1 || len(store.deleted) != 1 {
		t.Errorf("objects=%v rows=%v, want 1 each despite share failure", objects.deleted, store.deleted)
	}
	if len(auditor.actions) != 1 || auditor.actions[0] != "purge_upload" {
		t.Errorf("audit = %v, want 1 purge_upload", auditor.actions)
	}
}

func TestRetentionRowDeleteFailure(t *testing.T) {
	now := time.Now().UTC()
	store := &fakeUploadStore{
		uploads:    []*domain.Upload{{StorageKey: "anon-old", CreatedAt: now.Add(-4 * 24 * time.Hour)}},
		failDelete: map[string]bool{"anon-old": true},
	}
	shares := &fakeShareRemover{}
	objects := &flakyObjects{}
	auditor := &fakeAuditor{}

	if err := newRetentionJob(store, shares, objects, auditor).Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}

	// The object is gone but the row delete failed: mark it, don't audit success.
	if len(objects.deleted) != 1 {
		t.Errorf("object should still be deleted: %v", objects.deleted)
	}
	if store.failed["anon-old"] != 1 {
		t.Errorf("row-delete failure not marked: %v", store.failed)
	}
	for _, a := range auditor.actions {
		if a == "purge_upload" {
			t.Errorf("should not audit purge_upload when the row delete failed: %v", auditor.actions)
		}
	}
}

func TestRetentionSkipsOrphanRows(t *testing.T) {
	now := time.Now().UTC()
	store := &fakeUploadStore{uploads: []*domain.Upload{
		{StorageKey: "orphan", CreatedAt: now.Add(-4 * 24 * time.Hour)},
		{StorageKey: "real", CreatedAt: now.Add(-4 * 24 * time.Hour)},
	}}
	shares := &fakeShareRemover{}
	objects := &flakyObjects{notFound: map[string]bool{"orphan": true}}
	auditor := &fakeAuditor{}

	if err := newRetentionJob(store, shares, objects, auditor).Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}

	// The orphan (row but no object) is dropped without deleting an object or
	// auditing a purge; only the real file is purged and audited.
	if len(objects.deleted) != 1 || objects.deleted[0] != "real" {
		t.Errorf("deleted objects = %v, want [real]", objects.deleted)
	}
	if len(store.deleted) != 2 {
		t.Errorf("deleted rows = %v, want both rows gone", store.deleted)
	}
	if len(auditor.actions) != 1 || auditor.actions[0] != "purge_upload" {
		t.Errorf("audit = %v, want 1 purge_upload (real only)", auditor.actions)
	}
}
