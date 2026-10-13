package channel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/alreadyonapp/alreadyon/server/internal/storage"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db.DB
}

func TestCreate_AndGet(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	c, err := Create(ctx, db, Draft{Number: 1000, Name: "AlreadyOn Movies"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if c.ID == 0 {
		t.Error("expected a nonzero id")
	}
	if c.Number != 1000 || c.Name != "AlreadyOn Movies" {
		t.Errorf("got %+v", c)
	}

	got, err := Get(ctx, db, c.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != c {
		t.Errorf("Get returned %+v, want %+v", got, c)
	}
}

func TestCreate_TrimsName(t *testing.T) {
	db := openTestDB(t)
	c, err := Create(context.Background(), db, Draft{Number: 1000, Name: "  Spacey Name  "})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if c.Name != "Spacey Name" {
		t.Errorf("Name = %q, want trimmed", c.Name)
	}
}

func TestCreate_RejectsBlankName(t *testing.T) {
	db := openTestDB(t)
	_, err := Create(context.Background(), db, Draft{Number: 1000, Name: "   "})
	if !errors.Is(err, ErrInvalidName) {
		t.Errorf("err = %v, want ErrInvalidName", err)
	}
}

func TestCreate_RejectsNonPositiveNumber(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	for _, n := range []int{0, -1} {
		_, err := Create(ctx, db, Draft{Number: n, Name: "Channel"})
		if !errors.Is(err, ErrInvalidNumber) {
			t.Errorf("Create with number=%d: err = %v, want ErrInvalidNumber", n, err)
		}
	}
}

func TestCreate_RejectsDuplicateNumber(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if _, err := Create(ctx, db, Draft{Number: 1000, Name: "First"}); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	_, err := Create(ctx, db, Draft{Number: 1000, Name: "Second"})
	if !errors.Is(err, ErrDuplicateNumber) {
		t.Errorf("err = %v, want ErrDuplicateNumber", err)
	}
}

func TestCreate_RejectsReservedNumber(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	for _, n := range []int{DefaultChannelRangeStart, 150, DefaultChannelRangeEnd} {
		_, err := Create(ctx, db, Draft{Number: n, Name: "Custom"})
		if !errors.Is(err, ErrReservedNumber) {
			t.Errorf("Create with number=%d: err = %v, want ErrReservedNumber", n, err)
		}
	}
}

func TestCreate_AllowsNumberOutsideReservedRange(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	for _, n := range []int{DefaultChannelRangeStart - 1, DefaultChannelRangeEnd + 1, 1000} {
		if _, err := Create(ctx, db, Draft{Number: n, Name: fmt.Sprintf("Custom %d", n)}); err != nil {
			t.Errorf("Create with number=%d: unexpected error: %v", n, err)
		}
	}
}

func TestGet_NotFound(t *testing.T) {
	db := openTestDB(t)
	_, err := Get(context.Background(), db, 999)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestList_OrderedByNumber(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	for _, draft := range []Draft{
		{Number: 1002, Name: "C"},
		{Number: 1000, Name: "A"},
		{Number: 1001, Name: "B"},
	} {
		if _, err := Create(ctx, db, draft); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	channels, err := List(ctx, db)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(channels) != 3 {
		t.Fatalf("len(channels) = %d, want 3", len(channels))
	}
	for i, want := range []string{"A", "B", "C"} {
		if channels[i].Name != want {
			t.Errorf("channels[%d].Name = %q, want %q", i, channels[i].Name, want)
		}
	}
}

func TestList_EmptyLibrary(t *testing.T) {
	db := openTestDB(t)
	channels, err := List(context.Background(), db)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(channels) != 0 {
		t.Errorf("len(channels) = %d, want 0", len(channels))
	}
}

func TestUpdate(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	c, err := Create(ctx, db, Draft{Number: 1000, Name: "Original"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated, err := Update(ctx, db, c.ID, Draft{Number: 1005, Name: "Renamed"})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Number != 1005 || updated.Name != "Renamed" {
		t.Errorf("got %+v", updated)
	}

	got, err := Get(ctx, db, c.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != updated {
		t.Errorf("Get after Update = %+v, want %+v", got, updated)
	}
}

func TestUpdate_NotFound(t *testing.T) {
	db := openTestDB(t)
	_, err := Update(context.Background(), db, 999, Draft{Number: 1000, Name: "X"})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestUpdate_RejectsDuplicateNumber(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if _, err := Create(ctx, db, Draft{Number: 1000, Name: "First"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	second, err := Create(ctx, db, Draft{Number: 1001, Name: "Second"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = Update(ctx, db, second.ID, Draft{Number: 1000, Name: "Second"})
	if !errors.Is(err, ErrDuplicateNumber) {
		t.Errorf("err = %v, want ErrDuplicateNumber", err)
	}
}

func TestUpdate_RejectsMovingIntoReservedRange(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	c, err := Create(ctx, db, Draft{Number: 1000, Name: "Custom"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = Update(ctx, db, c.ID, Draft{Number: 150, Name: "Custom"})
	if !errors.Is(err, ErrReservedNumber) {
		t.Errorf("err = %v, want ErrReservedNumber", err)
	}
}

func TestUpdate_DefaultChannelKeepingItsOwnReservedNumber_Succeeds(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	// A "default" channel here is simulated directly, since seeding the
	// real defaults happens via migration 0007, not through Create
	// (which always rejects the reserved range).
	if _, err := db.Exec(`INSERT INTO channels (number, name) VALUES (100, 'Seeded Default')`); err != nil {
		t.Fatalf("seed default channel: %v", err)
	}
	c, err := Get(ctx, db, 1)
	if err != nil {
		t.Fatalf("Get seeded channel: %v", err)
	}

	updated, err := Update(ctx, db, c.ID, Draft{Number: 100, Name: "Renamed Default"})
	if err != nil {
		t.Errorf("expected a default channel to keep its own reserved number, got: %v", err)
	}
	if updated.Name != "Renamed Default" {
		t.Errorf("Name = %q, want %q", updated.Name, "Renamed Default")
	}
}

func TestUpdate_SameChannelKeepingItsOwnNumber_Succeeds(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	c, err := Create(ctx, db, Draft{Number: 1000, Name: "Original"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = Update(ctx, db, c.ID, Draft{Number: 1000, Name: "Renamed"})
	if err != nil {
		t.Errorf("Update keeping its own number should not collide with itself, got: %v", err)
	}
}

func TestDelete(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	c, err := Create(ctx, db, Draft{Number: 1000, Name: "Doomed"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := Delete(ctx, db, c.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := Get(ctx, db, c.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete: err = %v, want ErrNotFound", err)
	}
}

func TestDelete_NotFound(t *testing.T) {
	db := openTestDB(t)
	err := Delete(context.Background(), db, 999)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestDelete_FreesUpItsNumber(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	c, err := Create(ctx, db, Draft{Number: 1000, Name: "First"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := Delete(ctx, db, c.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := Create(ctx, db, Draft{Number: 1000, Name: "Second"}); err != nil {
		t.Errorf("expected number 1000 to be reusable after deleting its channel, got: %v", err)
	}
}
