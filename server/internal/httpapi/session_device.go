package httpapi

import "strings"

// Browsers do not supply a reliable computer hostname; never invent one from a peer IP.
// Keep deviceType in the API for compatibility, while Settings presents only the device label.
func sessionDevice(agent string) (string, string) {
	if agent == "AlreadyOn-Android/TV" {
		return "Google TV", "TV"
	}
	if agent == "AlreadyOn-Android/Mobile" {
		return "Android Phone", "Phone"
	}
	switch {
	case strings.Contains(agent, "Windows"):
		return "Windows", "Web Browser"
	case strings.Contains(agent, "Android"):
		return "Android Phone", "Web Browser"
	case strings.Contains(agent, "Mac OS X"):
		return "Mac", "Web Browser"
	case strings.Contains(agent, "Linux"):
		return "Linux", "Web Browser"
	default:
		return "Unknown Device", "Web Browser"
	}
}
