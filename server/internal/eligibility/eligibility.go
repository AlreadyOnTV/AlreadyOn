// Package eligibility implements AlreadyOn's audience-tier content
// filtering (docs/PROJECT_BRIEF.md "Audience / Age Eligibility").
//
// This is a household-level content filter, not a security boundary:
// there is no real per-viewer authentication yet (see #33, and #97's
// own scope note) — a tier is whatever the calling client currently
// asserts (see the Profiles admin tab / ProfileMenu). Real enforcement
// against a logged-in viewer is tracked separately under #97/#98.
package eligibility

// Tier is one of AlreadyOn's three audience tiers. The zero value ""
// is never a valid Tier on its own — see ParseTier.
type Tier string

const (
	TierYoungChild Tier = "young_child"
	TierTeen       Tier = "teen"
	TierAdult      Tier = "adult"
)

// tierRank orders tiers from least to most permissive — used by
// Eligible/Max to compare "does tier A see at least as much as tier B".
var tierRank = map[Tier]int{
	TierYoungChild: 0,
	TierTeen:       1,
	TierAdult:      2,
}

// ParseTier validates raw against the three known tiers, defaulting to
// TierAdult (unrestricted) for an empty or unrecognized value — an
// unrecognized *tier* should never accidentally restrict a viewer who
// didn't ask for restriction, the opposite default of RequiredTier's
// unrecognized-*certification* case, and deliberately so: one protects
// against silently over-restricting, the other against silently
// under-restricting.
func ParseTier(raw string) Tier {
	switch Tier(raw) {
	case TierYoungChild, TierTeen:
		return Tier(raw)
	default:
		return TierAdult
	}
}

// youngChildTV/youngChildMovie/teenTV/teenMovie are the certification
// allow-lists from docs/PROJECT_BRIEF.md's example audience_tiers
// mapping. adult has no list: it's unconditionally unrestricted.
var (
	youngChildTV    = map[string]bool{"TV-Y": true, "TV-Y7": true, "TV-Y7-FV": true, "TV-G": true}
	youngChildMovie = map[string]bool{"G": true, "PG": true}
	teenTV          = map[string]bool{"TV-Y": true, "TV-Y7": true, "TV-Y7-FV": true, "TV-G": true, "TV-PG": true, "TV-14": true}
	teenMovie       = map[string]bool{"G": true, "PG": true, "PG-13": true}
)

// RequiredTier returns the least-restrictive Tier that may still see
// content of mediaType ("movie" or "episode") certified certification.
// A missing or unrecognized certification requires TierAdult — a
// failed or absent rating lookup must never accidentally let a
// restricted tier see something that was never actually vetted.
func RequiredTier(mediaType, certification string) Tier {
	if certification == "" {
		return TierAdult
	}
	if mediaType == "movie" {
		if youngChildMovie[certification] {
			return TierYoungChild
		}
		if teenMovie[certification] {
			return TierTeen
		}
		return TierAdult
	}
	// An episode's certification always comes from its parent show (see
	// internal/channel.MinimumAudienceTier), which uses TV ratings.
	if youngChildTV[certification] {
		return TierYoungChild
	}
	if teenTV[certification] {
		return TierTeen
	}
	return TierAdult
}

// Eligible reports whether a viewer on tier may see content that
// requires required.
func Eligible(tier, required Tier) bool {
	return tierRank[tier] >= tierRank[required]
}

// The certifications an admin can assign by hand, per media type. Ones
// outside the tier lists above (R, NC-17, NR, TV-MA) need TierAdult.
var (
	MovieCertifications = []string{"G", "PG", "PG-13", "R", "NC-17", "NR"}
	TVCertifications    = []string{"TV-Y", "TV-Y7", "TV-Y7-FV", "TV-G", "TV-PG", "TV-14", "TV-MA"}
)

// ValidCertification reports whether cert is a known rating for mediaType
// ("movie", or anything else for TV).
func ValidCertification(mediaType, cert string) bool {
	list := TVCertifications
	if mediaType == "movie" {
		list = MovieCertifications
	}
	for _, c := range list {
		if c == cert {
			return true
		}
	}
	return false
}

// Max returns whichever of a/b requires the more permissive tier —
// used to fold a channel's whole lineup down to the single tier that
// can see all of it (see internal/channel.MinimumAudienceTier).
func Max(a, b Tier) Tier {
	if tierRank[b] > tierRank[a] {
		return b
	}
	return a
}
