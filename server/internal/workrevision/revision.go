// Package workrevision fences background commits against newer saved settings.
package workrevision

import (
	"context"
	"database/sql"
)

type key bool
type reader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func With(ctx context.Context, revision int64) context.Context {
	return context.WithValue(ctx, key(false), revision)
}
func WithScan(ctx context.Context, revision int64) context.Context {
	return context.WithValue(ctx, key(true), revision)
}

// Check is called inside the same transaction as a derived-data commit. SQLite
// serializes this check with preference saves, closing the cancel/commit race.
func Check(ctx context.Context, db reader) error     { return check(ctx, db, false) }
func CheckScan(ctx context.Context, db reader) error { return check(ctx, db, true) }
func check(ctx context.Context, db reader, scan bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	expected, ok := ctx.Value(key(scan)).(int64)
	if !ok {
		return nil
	}
	column := "revision"
	if scan {
		column = "scan_revision"
	}
	var current int64
	if err := db.QueryRowContext(ctx, "SELECT "+column+" FROM library_update_state WHERE id=1").Scan(&current); err != nil {
		return err
	}
	if current != expected {
		return context.Canceled
	}
	return nil
}

// Exec fences small destructive statements that do not already have a transaction.
func Exec(ctx context.Context, db *sql.DB, query string, args ...any) (sql.Result, error) {
	if _, ok := ctx.Value(key(false)).(int64); !ok {
		return db.ExecContext(ctx, query, args...)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := Check(ctx, tx); err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return result, tx.Commit()
}
