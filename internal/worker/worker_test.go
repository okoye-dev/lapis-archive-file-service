package worker

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/okoye-dev/lapis-archive-file-service/internal/domain"
)

type countingJob struct {
	runs atomic.Int32
}

func (j *countingJob) Name() string            { return "counting" }
func (j *countingJob) Interval() time.Duration { return 20 * time.Millisecond }
func (j *countingJob) Run(context.Context) error {
	j.runs.Add(1)
	return nil
}

func TestRunnerTicksAndStops(t *testing.T) {
	job := &countingJob{}
	r := New(nil, job)

	ctx, cancel := context.WithCancel(context.Background())
	r.Start(ctx)
	time.Sleep(75 * time.Millisecond) // immediate run + ~3 ticks
	cancel()
	r.Wait()

	if n := job.runs.Load(); n < 2 {
		t.Errorf("expected multiple runs, got %d", n)
	}
}

type fakeStore struct {
	expired []*domain.Share
	deleted []string
}

func (f *fakeStore) ListExpired(context.Context, int) ([]*domain.Share, error) {
	return f.expired, nil
}
func (f *fakeStore) DeleteBySlug(_ context.Context, slug string) error {
	f.deleted = append(f.deleted, slug)
	return nil
}

type fakeObjects struct{ deleted []string }

func (f *fakeObjects) DeleteFile(_ context.Context, key string) error {
	f.deleted = append(f.deleted, key)
	return nil
}

type fakeAuditor struct{ actions []string }

func (f *fakeAuditor) Record(_ context.Context, action, _ string, _ any) error {
	f.actions = append(f.actions, action)
	return nil
}

func TestPurgeDeletesAndAudits(t *testing.T) {
	store := &fakeStore{expired: []*domain.Share{
		{Slug: "aaaaaaaaaa", StorageKey: "u1_a.txt"},
		{Slug: "bbbbbbbbbb", StorageKey: "u2_b.txt"},
	}}
	objects := &fakeObjects{}
	auditor := &fakeAuditor{}

	job := PurgeExpiredShares{Store: store, Objects: objects, Auditor: auditor}
	if err := job.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}

	if len(objects.deleted) != 2 {
		t.Errorf("deleted objects = %v, want 2", objects.deleted)
	}
	if len(store.deleted) != 2 {
		t.Errorf("deleted rows = %v, want 2", store.deleted)
	}
	if len(auditor.actions) != 2 {
		t.Errorf("audit records = %v, want 2", auditor.actions)
	}
}
