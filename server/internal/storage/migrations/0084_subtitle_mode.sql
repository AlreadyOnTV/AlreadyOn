-- Nullable for existing profiles: retain their saved on/off preference.
-- +goose Up
ALTER TABLE profiles ADD COLUMN subtitle_mode TEXT CHECK (subtitle_mode IN ('always', 'other_audio', 'off'));

-- +goose Down
ALTER TABLE profiles DROP COLUMN subtitle_mode;
