// Automatic audiences and playback eligibility share the same ratings policy.
package eligibility

import "testing"

func TestAudienceAutomaticAndManualValidation(t *testing.T) {
	for _, tt := range []struct {
		kind, cert, override string
		tags                 []string
		want                 Tier
		review               bool
	}{
		{"episode", "TV-Y7", "", []string{"animation"}, TierYoungChild, false},
		{"episode", "TV-PG", "", []string{"comedy"}, TierTeen, false},
		{"episode", "TV-14", "", []string{"scifi"}, TierTeen, false},
		{"episode", "TV-14", "teen", []string{"scifi"}, TierTeen, false},
		{"movie", "PG", "young_child", []string{"anime"}, TierYoungChild, false},
		{"movie", "PG", "", []string{"anime"}, TierYoungChild, false},
		{"episode", "", "teen", []string{"fantasy"}, TierTeen, false},
		{"episode", "", "", nil, TierAdult, false},
		{"movie", "NR", "", nil, TierAdult, false},
		{"movie", "invalid", "", nil, TierAdult, false},
		{"movie", "G", "", nil, TierYoungChild, false},
		{"movie", "PG-13", "", []string{"horror", "war"}, TierTeen, false},
		{"episode", "TV-G", "", nil, TierYoungChild, false},
		{"episode", "TV-Y7-FV", "", []string{"anime"}, TierYoungChild, false},
		{"episode", "TV-MA", "teen", []string{"anime"}, TierAdult, false},
		{"movie", "R", "young_child", nil, TierAdult, false},
		{"episode", "TV-Y7", "", []string{"horror", "animation"}, TierYoungChild, false},
	} {
		got, review := Audience(tt.kind, tt.cert, tt.tags, tt.override)
		if got != tt.want || review != tt.review {
			t.Errorf("%+v: got %s review=%v", tt, got, review)
		}
	}
}
