-- Session addresses are server-observed peer IPs, never untrusted forwarded headers.
-- +goose Up
ALTER TABLE browser_sessions ADD COLUMN ip_address TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE browser_sessions DROP COLUMN ip_address;
