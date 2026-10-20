// Ambiguous automatic classifications stay unrestricted until manually reviewed.
package eligibility

import "testing"

func TestAudienceConservativeAndManualValidation(t *testing.T) {
	for _, tt := range []struct {
		kind, cert, override string
		tags                 []string
		want                 Tier
		review               bool
	}{
		{"episode", "TV-Y7", "", []string{"animation"}, TierYoungChild, false},
		{"episode", "TV-PG", "", []string{"comedy"}, TierTeen, false},
		{"episode", "TV-14", "", []string{"scifi"}, TierAdult, true},
		{"episode", "TV-14", "teen", []string{"scifi"}, TierTeen, false},
		{"movie", "PG", "young_child", []string{"anime"}, TierYoungChild, false},
		{"movie", "PG", "", []string{"anime"}, TierAdult, true},
		{"episode", "", "teen", []string{"fantasy"}, TierTeen, false},
		{"episode", "", "", nil, TierAdult, true},
		{"episode", "TV-MA", "teen", []string{"anime"}, TierAdult, false},
		{"movie", "R", "young_child", nil, TierAdult, false},
		{"episode", "TV-Y7", "", []string{"horror", "animation"}, TierAdult, true},
	} {
		got, review := Audience(tt.kind, tt.cert, tt.tags, tt.override)
		if got != tt.want || review != tt.review {
			t.Errorf("%+v: got %s review=%v", tt, got, review)
		}
	}
}
