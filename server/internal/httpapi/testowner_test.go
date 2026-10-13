package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/alreadyonapp/alreadyon/server/internal/auth"
)

// testOwnerID returns the database's first owner, creating a synthetic one
// on an empty test database.
func testOwnerID(t testing.TB, db *sql.DB) int64 {
	t.Helper()
	ctx := context.Background()
	u, err := auth.FirstOwner(ctx, db)
	if errors.Is(err, auth.ErrNoAccount) {
		u, err = auth.CreateAccount(ctx, db, "owner", "synthetic-owner-pass")
	}
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}
