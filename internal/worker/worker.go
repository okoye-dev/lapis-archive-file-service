package worker

import (
	"context"
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

type Runner struct {
	jobs []Job
	wg   sync.WaitGroup
}

func New(jobs ...Job) *Runner {
	return &Runner{jobs: jobs}
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

// runOnce executes a single pass with panic recovery and timing, so one job
// misbehaving never takes down the runner or the process.
func (r *Runner) runOnce(ctx context.Context, job Job) {
	defer func() {
		if p := recover(); p != nil {
			log.Printf("worker %s: panic recovered: %v", job.Name(), p)
		}
	}()

	start := time.Now()
	if err := job.Run(ctx); err != nil {
		log.Printf("worker %s: failed after %s: %v", job.Name(), time.Since(start), err)
		return
	}
	log.Printf("worker %s: ran in %s", job.Name(), time.Since(start))
}
