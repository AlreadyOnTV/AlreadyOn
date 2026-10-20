"""Synthetic catalog only: browser coverage never writes to the user's preview."""
import os
from pathlib import Path
import sqlite3

root = Path(os.environ['LIBRARY_TEST_ROOT'])
assert root.name.startswith('alreadyon-library-e2e.') and (root / '.synthetic-test').is_file()
db = sqlite3.connect(root / 'data' / 'alreadyon.db')
with db:
    db.execute("INSERT INTO tv_shows(id,tmdb_id,title,first_air_date,content_rating) VALUES(9001,9001,'Example Academy','2008-01-01','TV-14')")
    for number in range(1, 59):
        movie = number <= 55
        title = f'Film {number:02}' if movie else ['Pilot', 'Second Story', 'Finale'][number - 56]
        db.execute("INSERT INTO media_files(id,root_kind,path,size_bytes,modified_unix,first_seen_unix,last_seen_unix,duration_seconds,status) VALUES(?,'media',?,1,1,1,1,1200,'ok')", (9000 + number, f'/synthetic/{title}.mkv'))
        db.execute("""INSERT INTO identified_items(id,media_file_id,media_type,title,series_title,season_number,episode_number,release_year,genre_tags,genre_source,content_rating,show_id,metadata_fetched_at_unix,keyless_fetched_at_unix)
        VALUES(?,?,?,?,?,?,?,?,?,'tmdb',?,?,1,1)""", (9000 + number, 9000 + number, 'movie' if movie else 'episode', title, None if movie else 'Example Academy', None if movie else 1, None if movie else number - 55, 2008, '["anime","family"]' if movie else '["comedy"]', 'PG' if movie else None, None if movie else 9001))
    for number in range(25):
        show_id = 9100 + number
        item_id = 9200 + number
        title = f'Scroll Show {number:02}'
        db.execute("INSERT INTO tv_shows(id,tmdb_id,title,first_air_date,content_rating) VALUES(?,?,?,?,?)", (show_id, show_id, title, '2018-01-01', 'TV-PG'))
        db.execute("INSERT INTO media_files(id,root_kind,path,size_bytes,modified_unix,first_seen_unix,last_seen_unix,duration_seconds,status) VALUES(?,'media',?,1,1,1,1,1200,'ok')", (item_id, f'/synthetic/{title}.mkv'))
        db.execute("""INSERT INTO identified_items(id,media_file_id,media_type,title,series_title,season_number,episode_number,release_year,genre_tags,genre_source,content_rating,show_id,metadata_fetched_at_unix,keyless_fetched_at_unix)
        VALUES(?,?,'episode',?,?,1,1,2018,'["drama"]','tmdb','TV-PG',?,1,1)""", (item_id, item_id, title, title, show_id))
    db.execute("INSERT INTO channel_items(channel_id,media_file_id,position) SELECT id,9001,0 FROM channels WHERE number=133")
db.close()
