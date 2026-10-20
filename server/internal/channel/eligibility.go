package channel

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/alreadyonapp/alreadyon/server/internal/eligibility"
)

// Unidentified playable files also require the adult tier; a missing identity
// must never bypass rating restrictions when a catch-all channel schedules it.
// MinimumAudienceTier returns the least-restrictive eligibility.Tier
// that can see every item in channelID's own lineup (channel_items) —
// deliberately the whole lineup, not just what's currently scheduled,
// so a channel's visibility to a given tier doesn't flicker as the
// schedule window rotates. An empty lineup returns TierYoungChild
// (nothing in it to restrict).
//
// A movie's own content_rating is used directly; an episode's comes
// from its parent show (tv_shows.content_rating via identified_items.
// show_id) — see #79's migration 0020 and docs/PROJECT_BRIEF.md's "TV
// Show vs Episode Eligibility" section, since certification/genre/era
// are always decided at the show level, never per episode.
func MinimumAudienceTier(ctx context.Context, db *sql.DB, channelID int64) (eligibility.Tier, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT COALESCE(ii.media_type, ''),
				       COALESCE(CASE WHEN ii.content_rating_manual=1 THEN ii.content_rating END,ts.content_rating,ii.content_rating), COALESCE(CASE WHEN ii.genre_source='manual' THEN ii.genre_tags END,ts.genre_tags,ii.genre_tags,'[]'), COALESCE(ii.audience_override,ts.audience_override,'')
		FROM channel_items ci
		LEFT JOIN identified_items ii ON ii.media_file_id = ci.media_file_id
		LEFT JOIN tv_shows ts ON ts.id = ii.show_id
		WHERE ci.channel_id = ?`, channelID)
	if err != nil {
		return eligibility.TierYoungChild, fmt.Errorf("channel: minimum audience tier: %w", err)
	}
	defer rows.Close()

	tier := eligibility.TierYoungChild
	for rows.Next() {
		var mediaType string
		var certification sql.NullString
		var tagsJSON, override string
		if err := rows.Scan(&mediaType, &certification, &tagsJSON, &override); err != nil {
			return eligibility.TierYoungChild, fmt.Errorf("channel: minimum audience tier: %w", err)
		}
		var tags []string
		if err := json.Unmarshal([]byte(tagsJSON), &tags); err != nil {
			return eligibility.TierAdult, err
		}
		required, _ := eligibility.Audience(mediaType, certification.String, tags, override)
		tier = eligibility.Max(tier, required)
	}
	if err := rows.Err(); err != nil {
		return eligibility.TierYoungChild, fmt.Errorf("channel: minimum audience tier: %w", err)
	}
	return tier, nil
}
