package audit

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Auditor records an append-only trail of significant actions (what existed,
// what was done to it, when) so deletions and the like are accountable.
type Auditor interface {
	Record(ctx context.Context, action, subject string, detail any) error
}

type PostgresAuditor struct {
	pool *pgxpool.Pool
}

func NewPostgresAuditor(pool *pgxpool.Pool) *PostgresAuditor {
	return &PostgresAuditor{pool: pool}
}

func (a *PostgresAuditor) Record(ctx context.Context, action, subject string, detail any) error {
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

// Nop discards audit records; used when no database is configured.
type Nop struct{}

func (Nop) Record(context.Context, string, string, any) error { return nil }
