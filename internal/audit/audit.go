package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Auditor records an append-only trail of significant actions (what existed,
// what was done to it, when) so deletions and the like are accountable.
type Auditor interface {
	Record(ctx context.Context, action, subject string, detail any) error
}

type DBAuditor struct {
	pool *pgxpool.Pool
}

func NewDBAuditor(pool *pgxpool.Pool) *DBAuditor {
	return &DBAuditor{pool: pool}
}

func (a *DBAuditor) Record(ctx context.Context, action, subject string, detail any) error {
	var raw []byte
	if detail != nil {
		b, err := json.Marshal(detail)
		if err != nil {
			return fmt.Errorf("marshal audit detail: %w", err)
		}
		raw = b
	}

	_, err := a.pool.Exec(ctx,
		`insert into audit_log (action, subject, detail) values ($1, $2, $3)`,
		action, subject, raw)
	if err != nil {
		return fmt.Errorf("write audit log: %w", err)
	}
	return nil
}

// DBRunRecorder stores worker run history in job_runs. It satisfies
// worker.Recorder (matched structurally, no import). Failures to record are
// logged, never propagated — run history must not affect the job itself.
type DBRunRecorder struct {
	pool *pgxpool.Pool
}

func NewDBRunRecorder(pool *pgxpool.Pool) *DBRunRecorder {
	return &DBRunRecorder{pool: pool}
}

func (r *DBRunRecorder) Record(ctx context.Context, job string, start, end time.Time, runErr error) {
	status, errText := "ok", ""
	if runErr != nil {
		status, errText = "error", runErr.Error()
	}

	var errArg any
	if errText != "" {
		errArg = errText
	}

	_, err := r.pool.Exec(ctx,
		`insert into job_runs (job, started_at, finished_at, status, error) values ($1,$2,$3,$4,$5)`,
		job, start, end, status, errArg)
	if err != nil {
		log.Printf("job_runs: record %s: %v", job, err)
	}
}
