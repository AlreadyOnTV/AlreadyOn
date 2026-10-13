package schedule

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/commercial"
)

type rotationSeries struct {
	Key   string
	Items []lineupItem
}

// A checkpoint is stored AFTER each timeline segment, including commercials.
// Pending holds the rest of an already-started program's commercial expansion.
// Last records media IDs, not array positions, so adding episodes doesn't reset
// an existing show's progress. Each channel has its own checkpoints.
type rotationState struct {
	ChannelID        int64
	Turn             uint64
	Last             map[string]int64
	Show             string
	Remaining        int
	Pending          []lineupItem
	AdIndex          int
	SinceBreak       float64
	MovieCycle       uint64
	MovieRank        uint64
	MovieKey         string
	MovieBoundary    string
	MovieStarted     bool
	movieOrder       []movieRank
	movieOrderCycle  uint64
	movieOrderSource *rotationSeries
	movieOrderCount  int
}

func rotationRandom(channelID int64, key string, turn uint64) uint64 {
	sum := sha256.Sum256([]byte(fmt.Sprintf("alreadyon-tv-v1:%d:%s:%d", channelID, key, turn)))
	return binary.LittleEndian.Uint64(sum[:8])
}

// All filtered channels use a continuing rotation; manual order stays unchanged.
func loadRotation(ctx context.Context, db *sql.DB, channelID int64) ([]rotationSeries, error) {
	rows, err := db.QueryContext(ctx, `
 SELECT ci.media_file_id, mf.duration_seconds, COALESCE(ii.media_type, ''),
 CASE WHEN ii.media_type = 'episode' THEN
   CASE WHEN ii.show_id IS NOT NULL THEN 'show:' || ii.show_id
   ELSE 'title:' || COALESCE(NULLIF(ii.series_title, ''), ii.title) END
 ELSE 'movie:' || ci.media_file_id END
 FROM channel_items ci JOIN media_files mf ON mf.id = ci.media_file_id
 LEFT JOIN identified_items ii ON ii.media_file_id = ci.media_file_id
 WHERE ci.channel_id = ? AND mf.status = 'ok' AND mf.duration_seconds > 0
 AND EXISTS (SELECT 1 FROM channel_filters WHERE channel_id = ci.channel_id)
 ORDER BY COALESCE(ii.season_number, 0), COALESCE(ii.episode_number, 0), ci.media_file_id`, channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var series []rotationSeries
	indices := map[string]int{}
	for rows.Next() {
		var item lineupItem
		var key string
		if err := rows.Scan(&item.MediaFileID, &item.DurationSeconds, &item.MediaType, &key); err != nil {
			return nil, err
		}
		idx, ok := indices[key]
		if !ok {
			idx = len(series)
			indices[key] = idx
			series = append(series, rotationSeries{Key: key})
		}
		series[idx].Items = append(series[idx].Items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return series, nil
}

func (s *rotationState) next(series []rotationSeries, ads []lineupItem, settings commercial.Settings) lineupItem {
	if len(s.Pending) == 0 {
		if s.Last == nil {
			s.Last = map[string]int64{}
		}
		idx := -1
		for i, show := range series {
			if show.Key == s.Show {
				idx = i
				break
			}
		}
		if s.Remaining == 0 || idx < 0 {
			choices := make([]int, 0, len(series))
			for i, show := range series {
				if len(series) == 1 || show.Key != s.Show {
					choices = append(choices, i)
				}
			}
			moviesOnly := true
			for _, candidate := range series {
				if candidate.Items[0].MediaType == "episode" {
					moviesOnly = false
					break
				}
			}
			if moviesOnly {
				idx = s.nextMovie(series)
			} else {
				idx = choices[rotationRandom(s.ChannelID, "show", s.Turn)%uint64(len(choices))]
			}
			// One independent fair choice per block, not per episode. A different
			// show follows the block whenever the channel has an alternative.
			s.Remaining = 1 + int(rotationRandom(s.ChannelID, "block", s.Turn)%2)
			if series[idx].Items[0].MediaType != "episode" {
				s.Remaining = 1
			}
			s.Turn++
			s.Show = series[idx].Key
		}
		show := series[idx]
		next := int(rotationRandom(s.ChannelID, show.Key, 0) % uint64(len(show.Items)))
		if last, ok := s.Last[show.Key]; ok {
			for i, item := range show.Items {
				if item.MediaFileID == last {
					next = (i + 1) % len(show.Items)
					break
				}
			}
		}
		item := show.Items[next]
		if item.MediaType == "episode" {
			s.Last[show.Key] = item.MediaFileID
		}
		s.Remaining--
		e := &expander{pool: ads, poolIdx: s.AdIndex, sinceBreak: s.SinceBreak}
		if !settings.Enabled || len(ads) == 0 {
			e.emitWhole(item)
		} else if settings.Mode == commercial.ModeInterval {
			e.emitWithIntervalBreaks(item, settings)
		} else {
			e.emitWhole(item)
			e.insertBreak(settings.CommercialsPerBreak(item.MediaType))
		}
		s.Pending, s.AdIndex, s.SinceBreak = e.items, e.poolIdx, e.sinceBreak
	}
	item := s.Pending[0]
	s.Pending = s.Pending[1:]
	return item
}

func regenerateRotation(ctx context.Context, db *sql.DB, channelID int64, now time.Time, series []rotationSeries) error {
	settings, err := channelCommercialSettings(ctx, db, channelID)
	if err != nil {
		return err
	}
	ads, err := loadChannelCommercialPool(ctx, db, channelID)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Read before pruning: even after a long shutdown the last known airing
	// supplies the continuation rather than restarting every series.
	var end int64
	var checkpoint sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT ends_at_unix, continuation_json FROM schedule_entries
 WHERE channel_id = ? AND starts_at_unix <= ? ORDER BY starts_at_unix DESC LIMIT 1`, channelID, now.Unix()).Scan(&end, &checkpoint)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	state := rotationState{ChannelID: channelID}
	if checkpoint.Valid {
		if err := json.Unmarshal([]byte(checkpoint.String), &state); err != nil {
			return fmt.Errorf("schedule: invalid rotation checkpoint: %w", err)
		}
	}
	// A checkpoint can contain future commercial slots from the old policy.
	// Keep pending program slices (including their offsets), but drop ads that
	// are now disabled or outside the selected pool. The airing entry is untouched.
	allowedAds := map[int64]bool{}
	if settings.Enabled {
		for _, ad := range ads {
			allowedAds[ad.MediaFileID] = true
		}
	}
	pending := state.Pending[:0]
	for _, item := range state.Pending {
		if item.Kind != KindCommercial || allowedAds[item.MediaFileID] {
			pending = append(pending, item)
		}
	}
	state.Pending = pending
	// Removed shows must not linger indefinitely in serialized checkpoints.
	available := map[string]bool{}
	for _, show := range series {
		available[show.Key] = true
	}
	for key := range state.Last {
		if !available[key] || strings.HasPrefix(key, "movie:") {
			delete(state.Last, key)
		}
	}

	from := now.Add(-TrailingWindow)
	if end > now.Unix() {
		from = time.Unix(end, 0).UTC()
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM schedule_entries WHERE channel_id = ? AND
 (starts_at_unix >= ? OR ends_at_unix <= ?)`, channelID, from.Unix(), now.Add(-TrailingWindow).Unix()); err != nil {
		return err
	}
	for at := from; at.Before(now.Add(LeadingWindow)); {
		item := state.next(series, ads, settings)
		until := at.Add(durationFromSeconds(item.DurationSeconds))
		if !until.After(at) {
			return fmt.Errorf("schedule: nonpositive rotation duration")
		}
		raw, err := json.Marshal(state)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schedule_entries
   (channel_id,media_file_id,kind,source_offset_seconds,duration_seconds,starts_at_unix,ends_at_unix,continuation_json)
   VALUES (?,?,?,?,?,?,?,?)`, channelID, item.MediaFileID, string(item.Kind), item.SourceOffset, item.DurationSeconds, at.Unix(), until.Unix(), string(raw)); err != nil {
			return err
		}
		at = until
	}
	return tx.Commit()
}

// A seeded permutation is ordered by (hash, stable media key). Persisting its
// cursor takes constant space, unlike storing every previously played movie in
// each guide segment. New files behind the cursor join the next complete cycle.

type movieRank struct {
	index int
	rank  uint64
	key   string
}

func (s *rotationState) nextMovie(series []rotationSeries) int {
	for {
		if s.movieOrder == nil || s.movieOrderCycle != s.MovieCycle || s.movieOrderCount != len(series) || s.movieOrderSource != &series[0] {
			s.movieOrder = make([]movieRank, len(series))
			for i, c := range series {
				r := rotationRandom(s.ChannelID, "movie-order:"+c.Key, s.MovieCycle)
				if len(series) > 1 && c.Key == s.MovieBoundary {
					r = ^uint64(0)
				}
				s.movieOrder[i] = movieRank{i, r, c.Key}
			}
			sort.Slice(s.movieOrder, func(i, j int) bool {
				a, b := s.movieOrder[i], s.movieOrder[j]
				return a.rank < b.rank || (a.rank == b.rank && a.key < b.key)
			})
			s.movieOrderCycle = s.MovieCycle
			s.movieOrderCount = len(series)
			s.movieOrderSource = &series[0]
		}
		position := 0
		if s.MovieStarted {
			position = sort.Search(len(s.movieOrder), func(i int) bool {
				r := s.movieOrder[i]
				return r.rank > s.MovieRank || (r.rank == s.MovieRank && r.key > s.MovieKey)
			})
		}
		if position < len(s.movieOrder) {
			r := s.movieOrder[position]
			s.MovieRank = r.rank
			s.MovieKey = r.key
			s.MovieStarted = true
			return r.index
		}
		s.MovieCycle++
		s.MovieStarted = false
		s.MovieBoundary = s.Show
	}
}
