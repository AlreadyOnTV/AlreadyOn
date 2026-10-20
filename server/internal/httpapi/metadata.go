package httpapi

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/alreadyonapp/alreadyon/server/internal/metadata"
	"github.com/alreadyonapp/alreadyon/server/internal/refresh"
)

// metadataSettingsResponse never carries the actual credential value —
// only whether one is currently configured. A saved key is never sent
// back to the browser after the fact (not even to redisplay it): the
// field always renders empty, and Save only needs to know a key is
// already set to decide what an empty submission means (see
// handlePutMetadataSettings).
type metadataSettingsResponse struct {
	TMDbAPIKeySet   bool `json:"tmdbApiKeySet"`
	KeylessMetadata bool `json:"keylessMetadata"`
}

func toMetadataSettingsResponse(s metadata.Settings) metadataSettingsResponse {
	return metadataSettingsResponse{
		TMDbAPIKeySet:   s.TMDbAPIKey != "",
		KeylessMetadata: s.KeylessEnabled,
	}
}

func handleGetMetadataSettings(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s, err := metadata.GetSettings(r.Context(), db)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, toMetadataSettingsResponse(s))
	}
}

// metadataSettingsUpdateRequest: a field left out of the request body
// entirely (nil) means "leave this setting as-is" — the browser never
// holds the real key to send back, so "the user didn't touch this field"
// has to be expressible without resending it. TMDbAPIKey present as ""
// means "explicitly clear it" (the UI's Clear button); present as a
// non-empty string means "replace it with this value".
type metadataSettingsUpdateRequest struct {
	TMDbAPIKey      *string `json:"tmdbApiKey"`
	KeylessMetadata *bool   `json:"keylessMetadata"`
}

func handlePutMetadataSettings(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body metadataSettingsUpdateRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		// A non-empty value is validated regardless of which caller sent
		// it (not just the Server Settings page's own client-side check) —
		// "" (explicit clear) and nil (leave alone) both skip this, since
		// neither is a key to validate the shape of.
		if body.TMDbAPIKey != nil && *body.TMDbAPIKey != "" && !metadata.IsValidTMDbKey(*body.TMDbAPIKey) {
			writeJSONError(w, http.StatusBadRequest,
				`TMDb API key doesn't look like a valid "API Read Access Token" (a long, JWT-shaped value with two "." separators) — not the short "API Key"`)
			return
		}

		current, err := metadata.GetSettings(r.Context(), db)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		next := current
		if body.TMDbAPIKey != nil {
			next.TMDbAPIKey = *body.TMDbAPIKey
		}
		next.KeylessEnabled = true // Legacy toggle values are accepted but cannot disable automatic enrichment.

		if err := metadata.SaveSettings(r.Context(), db, next); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if next != current {
			// The server's worker serializes this with scans/startup and uses
			// the latest settings if several toggles arrive during enrichment.
			refresh.NotifyMetadataChanged()
		}
		writeJSON(w, http.StatusOK, toMetadataSettingsResponse(next))
	}
}
