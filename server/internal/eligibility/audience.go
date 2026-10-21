package eligibility

// Audience separates channel identity from cumulative viewing permission.
// Ratings supply the household's default audience; genres alone do not describe
// violence or nudity severity. Missing/invalid ratings have a complete automatic
// classification too: unrestricted, rather than requiring manual approval to air.
// Owner overrides remain supported, with R/NC-17/TV-MA as an unrestricted floor.
func Audience(mediaType, certification string, tags []string, override string) (Tier, bool) {
	if certification == "TV-MA" || certification == "R" || certification == "NC-17" {
		return TierAdult, false
	}
	if override != "" {
		return ParseTier(override), false
	}
	return RequiredTier(mediaType, certification), false
}
