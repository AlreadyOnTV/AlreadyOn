-- Session inactivity is independent of absolute sign-in lifetime, including native sessions.
-- +goose Up
ALTER TABLE server_settings ADD COLUMN remove_inactive_sessions INTEGER NOT NULL DEFAULT 1 CHECK (remove_inactive_sessions IN (0, 1));
ALTER TABLE server_settings ADD COLUMN inactive_session_days INTEGER NOT NULL DEFAULT 14 CHECK (inactive_session_days BETWEEN 1 AND 3650);

-- +goose Down
ALTER TABLE server_settings DROP COLUMN inactive_session_days;
ALTER TABLE server_settings DROP COLUMN remove_inactive_sessions;
