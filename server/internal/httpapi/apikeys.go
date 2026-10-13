package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/auth"
)

// apiKeyResponse never carries the raw or hashed key value — only what
// an admin needs to identify and revoke one (see auth.APIKey).
type apiKeyResponse struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	KeyPrefix string `json:"keyPrefix"`
	CreatedAt string `json:"createdAt"`
	// Username is the owner account the key acts as.
	Username string `json:"username,omitempty"`
}

func toAPIKeyResponse(k auth.APIKey) apiKeyResponse {
	return apiKeyResponse{
		ID:        k.ID,
		Name:      k.Name,
		KeyPrefix: k.Prefix,
		Username:  k.Username,
		CreatedAt: time.Unix(k.CreatedAtUnix, 0).UTC().Format(time.RFC3339),
	}
}

// apiKeyCreateResponse is the one and only response that ever carries
// the real key — the admin must copy it now, since GET /api/admin/api-keys
// never returns it again (see auth.GenerateAPIKey).
type apiKeyCreateResponse struct {
	apiKeyResponse
	Key string `json:"key"`
}

func handleListAPIKeys(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		keys, err := auth.ListAPIKeys(r.Context(), db)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		resp := make([]apiKeyResponse, 0, len(keys))
		for _, k := range keys {
			resp = append(resp, toAPIKeyResponse(k))
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func handleCreateAPIKey(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		rawKey, key, err := auth.GenerateAPIKey(r.Context(), db, user.ID, body.Name)
		if err != nil {
			if errors.Is(err, auth.ErrAPIKeyOwnerOnly) {
				writeJSONError(w, http.StatusForbidden, err.Error())
				return
			}
			if errors.Is(err, auth.ErrInvalidAPIKeyName) {
				writeJSONError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, apiKeyCreateResponse{apiKeyResponse: toAPIKeyResponse(key), Key: rawKey})
	}
}

func handleRevokeAPIKey(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid api key id")
			return
		}
		if err := auth.RevokeAPIKey(r.Context(), db, id); err != nil {
			if errors.Is(err, auth.ErrAPIKeyNotFound) {
				writeJSONError(w, http.StatusNotFound, err.Error())
				return
			}
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
