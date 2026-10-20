-- +goose Up
-- Remove only stock decade definitions that no saved channel uses. Keep edits
-- and referenced IDs so this catalog cleanup cannot widen a channel's rules.
DELETE FROM channel_filter_options
WHERE kind='era' AND id IN ('1950s','1960s','1970s','1980s','1990s','2000s','2010s','2020s')
  AND json_extract(definition,'$.name')=substr(id,1,4)||'’s'
  AND json_extract(definition,'$.startYear')=CAST(substr(id,1,4) AS INTEGER)
  AND json_extract(definition,'$.endYear')=CAST(substr(id,1,4) AS INTEGER)+9
  AND json_remove(definition,'$.name','$.startYear','$.endYear')='{}'
  AND NOT EXISTS (
    SELECT 1 FROM channel_filters f, json_each(f.extra,'$.eras') selected
    WHERE selected.value=channel_filter_options.id
  );

-- +goose Down
INSERT OR IGNORE INTO channel_filter_options(kind,id,definition,position,default_position) VALUES
 ('era','2020s','{"name":"2020’s","startYear":2020,"endYear":2029}',3,3),
 ('era','2010s','{"name":"2010’s","startYear":2010,"endYear":2019}',4,4),
 ('era','2000s','{"name":"2000’s","startYear":2000,"endYear":2009}',5,5),
 ('era','1990s','{"name":"1990’s","startYear":1990,"endYear":1999}',6,6),
 ('era','1980s','{"name":"1980’s","startYear":1980,"endYear":1989}',7,7),
 ('era','1970s','{"name":"1970’s","startYear":1970,"endYear":1979}',8,8),
 ('era','1960s','{"name":"1960’s","startYear":1960,"endYear":1969}',9,9),
 ('era','1950s','{"name":"1950’s","startYear":1950,"endYear":1959}',10,10);
