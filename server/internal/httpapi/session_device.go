package httpapi

import "strings"

// User agents expose app/browser identity, not a private machine hostname.
// These display hints never participate in authentication or authorization.
func sessionDevice(agent string) (string, string) {
	if agent == "AlreadyOn-Android/TV" {
		return "AlreadyOn for Google TV", "TV"
	}
	if agent == "AlreadyOn-Android/Mobile" {
		return "AlreadyOn for Android", "Phone"
	}
	if agent == "" {
		return "Unknown device", "Web Browser"
	}
	browser := "Browser"
	switch {
	case strings.Contains(agent, "Edg/"):
		browser = "Edge"
	case strings.Contains(agent, "Firefox/"):
		browser = "Firefox"
	case strings.Contains(agent, "Chrome/"):
		browser = "Chrome"
	case strings.Contains(agent, "Safari/"):
		browser = "Safari"
	}
	os := "unknown system"
	switch {
	case strings.Contains(agent, "iPad"):
		os = "iPadOS"
	case strings.Contains(agent, "iPhone"):
		os = "iOS"
	case strings.Contains(agent, "Android"):
		os = "Android"
	case strings.Contains(agent, "Windows"):
		os = "Windows"
	case strings.Contains(agent, "Mac OS X"):
		os = "macOS"
	case strings.Contains(agent, "Linux"):
		os = "Linux"
	}
	return browser + " on " + os, "Web Browser"
}
