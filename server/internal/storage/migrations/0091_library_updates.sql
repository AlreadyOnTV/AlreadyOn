-- +goose Up
-- Desired preferences and dirty work survive process restarts. Scan revisions
-- are independent so changing channel selections never restarts useful discovery.
CREATE TABLE library_update_state (
 id INTEGER PRIMARY KEY CHECK(id=1),
 revision INTEGER NOT NULL DEFAULT 0,
 applied_revision INTEGER NOT NULL DEFAULT 0,
 scan_revision INTEGER NOT NULL DEFAULT 0,
 needs_scan INTEGER NOT NULL DEFAULT 0,
 state TEXT NOT NULL DEFAULT 'idle',
 processed INTEGER NOT NULL DEFAULT 0,
 total INTEGER NOT NULL DEFAULT 0,
 error TEXT NOT NULL DEFAULT ''
);
INSERT INTO library_update_state(id) VALUES(1);
CREATE TABLE library_update_channels (
 channel_id INTEGER PRIMARY KEY REFERENCES channels(id) ON DELETE CASCADE,
 rebuild INTEGER NOT NULL DEFAULT 0,
 revision INTEGER NOT NULL
);

-- +goose Down
DROP TABLE library_update_channels;
DROP TABLE library_update_state;
