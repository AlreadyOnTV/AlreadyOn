package channel

import (
	"context"
	"database/sql"
	"fmt"
)

// The approved 38-channel cable-style lineup runs from kids to unrestricted.
var defaultChannels = []Draft{
	{Number: 101, Name: "Kids Toon Classic"},
	{Number: 102, Name: "Kids Toon Modern"},
	{Number: 103, Name: "Kids Live Classic"},
	{Number: 104, Name: "Kids Live Modern"},
	{Number: 105, Name: "Kids Movie Classic"},
	{Number: 106, Name: "Kids Movie Modern"},
	{Number: 107, Name: "Teen Toon Classic"},
	{Number: 108, Name: "Teen Toon Modern"},
	{Number: 109, Name: "Teen Anime One"},
	{Number: 110, Name: "Teen Anime Two"},
	{Number: 111, Name: "Teen Real Classic"},
	{Number: 112, Name: "Teen Real Modern"},
	{Number: 113, Name: "Teen Drama Classic"},
	{Number: 114, Name: "Teen Drama Modern"},
	{Number: 115, Name: "Teen Worlds Classic"},
	{Number: 116, Name: "Teen Worlds Modern"},
	{Number: 117, Name: "Teen Action Classic"},
	{Number: 118, Name: "Teen Action Modern"},
	{Number: 119, Name: "Teen Movie Classic"},
	{Number: 120, Name: "Teen Movie Modern"},
	{Number: 121, Name: "Toon One"},
	{Number: 122, Name: "Toon Two"},
	{Number: 123, Name: "Anime One"},
	{Number: 124, Name: "Anime Two"},
	{Number: 125, Name: "Reality One"},
	{Number: 126, Name: "Reality Two"},
	{Number: 127, Name: "Drama One"},
	{Number: 128, Name: "Drama Two"},
	{Number: 129, Name: "Worlds One"},
	{Number: 130, Name: "Worlds Two"},
	{Number: 131, Name: "Action One"},
	{Number: 132, Name: "Action Two"},
	{Number: 133, Name: "Cinema One"},
	{Number: 134, Name: "Cinema Two"},
	{Number: 135, Name: "Action Cinema"},
	{Number: 136, Name: "Beyond Cinema"},
	{Number: 137, Name: "Teen Comedy Classic"},
	{Number: 138, Name: "Teen Comedy Modern"},
}

// SeedDefaults inserts AlreadyOn's built-in default channels
// (defaultChannels, all within the reserved 100-199 range) if — and
// only if — the channels table is currently empty. This is deliberately
// an explicit, app-level startup step rather than part of a schema
// migration: a migration runs against every database including this
// package's own test databases, which would otherwise always start
// with 53 rows already present. Returns how many channels were
// inserted (0 if the table already had at least one row, meaning
// seeding was skipped).
func SeedDefaults(ctx context.Context, db *sql.DB) (int, error) {
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM channels`).Scan(&count); err != nil {
		return 0, fmt.Errorf("channel: seed defaults: %w", err)
	}
	if count > 0 {
		return 0, nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("channel: seed defaults: %w", err)
	}
	defer tx.Rollback()

	for _, d := range defaultChannels {
		if _, err := tx.ExecContext(ctx, `INSERT INTO channels (number, name) VALUES (?, ?)`, d.Number, d.Name); err != nil {
			return 0, fmt.Errorf("channel: seed defaults: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("channel: seed defaults: %w", err)
	}
	return len(defaultChannels), nil
}
