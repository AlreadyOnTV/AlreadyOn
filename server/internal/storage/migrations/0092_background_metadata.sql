-- +goose Up
-- Provider settings share the durable update worker without forcing a filesystem scan.
ALTER TABLE library_update_state ADD COLUMN needs_metadata INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE library_update_state DROP COLUMN needs_metadata;
