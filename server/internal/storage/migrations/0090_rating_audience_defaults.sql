-- +goose Up
-- Align untouched built-in rating definitions with automatic audience selection.
-- User-edited definitions and all saved channel filters/overrides remain intact.
UPDATE channel_filter_options
SET definition=json_set(definition,'$.ratings',json('["TV-Y","TV-Y7","TV-Y7-FV","TV-G","G","PG"]'))
WHERE kind='age' AND id='young_child'
 AND json_extract(definition,'$.name')='Kids'
 AND json_extract(definition,'$.ratings')='["TV-Y","TV-Y7","TV-G","G"]'
 AND coalesce(json_extract(definition,'$.allowUnknown'),0)=0
 AND coalesce(json_extract(definition,'$.unrestricted'),0)=0;
UPDATE channel_filter_options
SET definition=json_set(definition,'$.ratings',json('["TV-Y","TV-Y7","TV-Y7-FV","TV-G","TV-PG","TV-14","G","PG","PG-13"]'))
WHERE kind='age' AND id='teen'
 AND json_extract(definition,'$.name') IN ('Teen','Teens')
 AND json_extract(definition,'$.ratings')='["TV-Y","TV-Y7","TV-G","TV-PG","TV-14","G","PG","PG-13"]'
 AND coalesce(json_extract(definition,'$.allowUnknown'),0)=0
 AND coalesce(json_extract(definition,'$.unrestricted'),0)=0;

-- +goose Down
-- Do not undo owner decisions made after the upgrade.
SELECT 1;
