package eligibility

import "slices"

// Audience separates channel identity from cumulative viewing permission. A
// manual decision is positive owner validation; a known certification remains
// a safety floor. Ambiguous TV-14/PG-13 content needs review because these
// certifications do not establish the absence of nudity or graphic violence.
func Audience(mediaType, certification string, tags []string, override string) (Tier, bool) {
	if override != "" {
		tier := ParseTier(override)
		if certification == "TV-MA" || certification == "R" || certification == "NC-17" {
			tier = TierAdult
		}
		return tier, false
	}
	if certification == "TV-MA" || certification == "R" || certification == "NC-17" {
		return TierAdult, false
	}
	if !ValidCertification(mediaType, certification) || certification == "NR" {
		return TierAdult, true
	}
	if slices.Contains(tags, "horror") || slices.Contains(tags, "war") {
		return TierAdult, true
	}
	if slices.Contains(tags, "anime") {
		return TierAdult, true
	}
	if mediaType == "movie" {
		if certification == "G" && (slices.Contains(tags, "family") || slices.Contains(tags, "kids")) {
			return TierYoungChild, false
		}
		return TierAdult, true
	}
	switch certification {
	case "TV-Y":
		return TierYoungChild, false
	case "TV-Y7", "TV-Y7-FV":
		if slices.Contains(tags, "animation") {
			return TierYoungChild, false
		}
		return TierTeen, false
	case "TV-G":
		if slices.Contains(tags, "animation") {
			return TierYoungChild, false
		}
		return TierTeen, false
	case "TV-PG":
		return TierTeen, false
	default:
		return TierAdult, true
	}
}
