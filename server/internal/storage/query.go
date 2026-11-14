package storage

import (
	"context"
	"database/sql"
)

// Querier lets small settings operations share their caller's transaction.
// Bulk settings saves must commit all preferences and their pending work together.
type Querier interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}
