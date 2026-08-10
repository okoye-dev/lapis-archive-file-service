package worker

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// Job is a unit of periodic background work. New jobs just implement this
// interface and get registered with a Runner; nothing here is purge-specific.
type Job interface {
	Name() string
	Interval() time.Duration
	Run(ctx context.Context) error
}

// RecordFunc persists one row of run history per execution (job, timing,
// outcome) so runs are observable after the fact. It's a plain callback, not
// an interface: there's only ever one hook, and this keeps the worker package
// free of any database dependency. Optional; nil disables tracking.
type RecordFunc func(ctx context.Context, job string, start, end time.Time, runErr error)

type Runner struct {
	jobs   []Job
	record RecordFunc
	wg     sync.WaitGroup
}

func New(record RecordFunc, jobs ...Job) *Runner {
	return &Runner{jobs: jobs, record: record}
}

// Start runs each job immediately, then on its own interval, until ctx is
// cancelled. It returns right away; call Wait to block for a clean stop.
func (r *Runner) Start(ctx context.Context) {
	for _, job := range r.jobs {
		r.wg.Add(1)
		go r.loop(ctx, job)
	}
}

func (r *Runner) Wait() {
	r.wg.Wait()
}

func (r *Runner) loop(ctx context.Context, job Job) {
	defer r.wg.Done()

	ticker := time.NewTicker(job.Interval())
	defer ticker.Stop()

	r.runOnce(ctx, job)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.runOnce(ctx, job)
		}
	}
}

// runOnce executes a single pass with panic recovery and timing, logs the
// outcome, and records it, so one job misbehaving never takes down the runner
// or the process.
func (r *Runner) runOnce(ctx context.Context, job Job) {
	start := time.Now()
	err := safeRun(ctx, job)
	end := time.Now()

	if err != nil {
		log.Printf("worker %s: failed after %s: %v", job.Name(), end.Sub(start), err)
	} else {
		log.Printf("worker %s: ran in %s", job.Name(), end.Sub(start))
	}

	if r.record != nil {
		r.record(ctx, job.Name(), start, end, err)
	}
}

func safeRun(ctx context.Context, job Job) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v", p)
		}
	}()
	return job.Run(ctx)
}
