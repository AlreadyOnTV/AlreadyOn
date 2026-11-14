package workrevision

import (
	"context"
	"errors"
	"testing"

	"github.com/alreadyonapp/alreadyon/server/internal/storage"
)

// Cancellation may arrive after a new preference has committed but before an
// old worker observes its signal. The SQL revision must reject that old commit.
func TestStaleRevisionRejectsCommitEvenWithoutCancellation(t *testing.T) {
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := With(t.Context(), 0)
	if _, err := db.Exec(`UPDATE library_update_state SET revision=1`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := Check(ctx, tx); !errors.Is(err, context.Canceled) {
		t.Fatalf("stale guard: %v", err)
	}
	if err := Check(With(t.Context(), 1), tx); err != nil {
		t.Fatal(err)
	}
	if err := CheckScan(WithScan(t.Context(), 0), tx); err != nil {
		t.Fatal("preference revision invalidated scan", err)
	}
}
