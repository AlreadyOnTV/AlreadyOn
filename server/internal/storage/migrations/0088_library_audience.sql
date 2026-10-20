-- +goose Up
ALTER TABLE identified_items ADD COLUMN audience_override TEXT CHECK(audience_override IN ('young_child','teen','adult'));
ALTER TABLE tv_shows ADD COLUMN local_series_title TEXT;
ALTER TABLE tv_shows ADD COLUMN local_series_year INTEGER;
ALTER TABLE tv_shows ADD COLUMN audience_override TEXT CHECK(audience_override IN ('young_child','teen','adult'));
ALTER TABLE tv_shows ADD COLUMN premiere_year_override INTEGER;
ALTER TABLE tv_shows ADD COLUMN genre_source TEXT;
ALTER TABLE tv_shows ADD COLUMN content_rating_manual INTEGER NOT NULL DEFAULT 0;
UPDATE metadata_settings SET keyless_enabled=1;
-- Preserve current airings. New default rules take effect when lineups are refreshed.
UPDATE channel_filters SET extra=json_set(COALESCE(extra,'{}'),'$.matchAudience',json('true'))
 WHERE channel_id IN (SELECT id FROM channels WHERE number BETWEEN 101 AND 136);
UPDATE channel_filters SET genres='["animation","anime"]',extra=json_remove(extra,'$.excludedGenres')
 WHERE channel_id IN (SELECT id FROM channels WHERE number IN (101,102));
UPDATE channel_filters SET extra=json_set(extra,'$.excludedGenres',json('["animation","anime"]'))
 WHERE channel_id IN (SELECT id FROM channels WHERE number IN (111,112,113,114,115,116,117,118,125,126,127,128,129,130,131,132));
INSERT OR IGNORE INTO channels(number,name) SELECT 137,'Teen Comedy Classic' WHERE EXISTS(SELECT 1 FROM channels WHERE number=101);
INSERT OR IGNORE INTO channels(number,name) SELECT 138,'Teen Comedy Modern' WHERE EXISTS(SELECT 1 FROM channels WHERE number=101);
INSERT OR IGNORE INTO channel_filters(channel_id,audience_tier,genres,media_type,era,title_contains,extra)
 SELECT id,'teen','["comedy"]','tv',CASE number WHEN 137 THEN 'nostalgia' ELSE 'modern' END,'','{"requireKnownYear":true,"matchAudience":true,"excludedGenres":["animation","anime"]}' FROM channels WHERE number IN (137,138);
-- +goose Down
DELETE FROM channels WHERE number IN (137,138) AND name IN ('Teen Comedy Classic','Teen Comedy Modern');
ALTER TABLE identified_items DROP COLUMN audience_override;
ALTER TABLE tv_shows DROP COLUMN audience_override;
ALTER TABLE tv_shows DROP COLUMN local_series_title;
ALTER TABLE tv_shows DROP COLUMN local_series_year;
ALTER TABLE tv_shows DROP COLUMN premiere_year_override;
ALTER TABLE tv_shows DROP COLUMN genre_source;
ALTER TABLE tv_shows DROP COLUMN content_rating_manual;
