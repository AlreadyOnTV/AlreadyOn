package eligibility

// Certification defaults reflect the household audience policy.

import "testing"

func TestParseTier(t *testing.T) {
	cases := []struct {
		raw  string
		want Tier
	}{
		{"young_child", TierYoungChild},
		{"teen", TierTeen},
		{"adult", TierAdult},
		{"", TierAdult},
		{"nonsense", TierAdult},
	}
	for _, c := range cases {
		if got := ParseTier(c.raw); got != c.want {
			t.Errorf("ParseTier(%q) = %q, want %q", c.raw, got, c.want)
		}
	}
}

func TestRequiredTier_Movie(t *testing.T) {
	cases := []struct {
		certification string
		want          Tier
	}{
		{"G", TierYoungChild},
		{"PG", TierYoungChild},
		{"PG-13", TierTeen},
		{"R", TierAdult},
		{"NC-17", TierAdult},
		{"", TierAdult},
		{"not-a-real-rating", TierAdult},
	}
	for _, c := range cases {
		if got := RequiredTier("movie", c.certification); got != c.want {
			t.Errorf("RequiredTier(movie, %q) = %q, want %q", c.certification, got, c.want)
		}
	}
}

func TestRequiredTier_Episode(t *testing.T) {
	cases := []struct {
		certification string
		want          Tier
	}{
		{"TV-Y", TierYoungChild},
		{"TV-Y7", TierYoungChild},
		{"TV-Y7-FV", TierYoungChild},
		{"TV-G", TierYoungChild},
		{"TV-PG", TierTeen},
		{"TV-14", TierTeen},
		{"TV-MA", TierAdult},
		{"", TierAdult},
	}
	for _, c := range cases {
		if got := RequiredTier("episode", c.certification); got != c.want {
			t.Errorf("RequiredTier(episode, %q) = %q, want %q", c.certification, got, c.want)
		}
	}
}

func TestEligible(t *testing.T) {
	cases := []struct {
		tier     Tier
		required Tier
		want     bool
	}{
		{TierYoungChild, TierYoungChild, true},
		{TierYoungChild, TierTeen, false},
		{TierYoungChild, TierAdult, false},
		{TierTeen, TierYoungChild, true},
		{TierTeen, TierTeen, true},
		{TierTeen, TierAdult, false},
		{TierAdult, TierYoungChild, true},
		{TierAdult, TierTeen, true},
		{TierAdult, TierAdult, true},
	}
	for _, c := range cases {
		if got := Eligible(c.tier, c.required); got != c.want {
			t.Errorf("Eligible(%q, %q) = %v, want %v", c.tier, c.required, got, c.want)
		}
	}
}

func TestMax(t *testing.T) {
	if got := Max(TierYoungChild, TierTeen); got != TierTeen {
		t.Errorf("Max(young_child, teen) = %q, want teen", got)
	}
	if got := Max(TierAdult, TierYoungChild); got != TierAdult {
		t.Errorf("Max(adult, young_child) = %q, want adult", got)
	}
	if got := Max(TierTeen, TierTeen); got != TierTeen {
		t.Errorf("Max(teen, teen) = %q, want teen", got)
	}
}
