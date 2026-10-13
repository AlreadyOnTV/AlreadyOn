package httpapi

import (
	"database/sql"
	"errors"
	"github.com/alreadyonapp/alreadyon/server/internal/auth"
	"github.com/alreadyonapp/alreadyon/server/internal/serversettings"
	"net"
	"net/http"
	"strconv"
	"time"
)

// Direct private socket peers only: proxy headers never grant LAN pairing.
func tvLAN(w http.ResponseWriter, r *http.Request) bool {
	ip := net.ParseIP(auth.ClientAddress(r))
	if r.Header.Get("Forwarded") != "" || r.Header.Get("X-Forwarded-For") != "" || r.TLS == nil || ip == nil || !(ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()) {
		writeJSONError(w, http.StatusForbidden, "TV pairing requires HTTPS on the local network")
		return false
	}
	return true
}

func handleTVServer(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !tvLAN(w, r) {
			return
		}
		settings, err := serversettings.Get(r.Context(), db)
		if err != nil {
			writeJSONError(w, 500, "could not read server name")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"name": settings.ServerName, "service": "alreadyon", "pairingEnabled": auth.TVPairingEnabled(r.Context(), db), "protocol": 1})
	}
}

func handleTVPIN(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := requireUser(w, r)
		if !ok {
			return
		}
		switch r.Method {
		case http.MethodGet:
			enabled, err := auth.HasTVPIN(r.Context(), db, user.ID)
			if err != nil {
				writeJSONError(w, 500, "could not read TV pairing settings")
				return
			}
			writeJSON(w, 200, map[string]bool{"enabled": enabled})
		case http.MethodPut:
			var body struct {
				PIN string `json:"pin"`
			}
			if !decodeStrict(w, r, &body) {
				return
			}
			err := auth.SetTVPIN(r.Context(), db, user.ID, body.PIN)
			switch {
			case errors.Is(err, auth.ErrInvalidTVPIN):
				writeJSONError(w, 400, err.Error())
			case errors.Is(err, auth.ErrTVPINTaken):
				writeJSONError(w, 409, err.Error())
			case err != nil:
				writeJSONError(w, 500, "could not save TV PIN")
			default:
				writeJSON(w, 200, map[string]bool{"enabled": true})
			}
		case http.MethodDelete:
			if err := auth.ClearTVPIN(r.Context(), db, user.ID); err != nil {
				writeJSONError(w, 500, "could not remove TV PIN")
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}
	}
}

func handleTVLogin(db *sql.DB, limiter *auth.Limiter, cookieName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !tvLAN(w, r) {
			return
		}
		client := "tv:" + auth.ClientAddress(r)
		for _, bucket := range []string{client, "tv:all"} {
			if ok, wait := limiter.Allow(bucket); !ok {
				w.Header().Set("Retry-After", strconv.Itoa(int(wait.Round(time.Second)/time.Second)))
				writeJSONError(w, 429, "Too many TV pairing attempts. Try again later.")
				return
			}
		}
		var body struct {
			Username string `json:"username"`
			PIN      string `json:"pin"`
		}
		if !decodeStrict(w, r, &body) {
			return
		}
		user, ok, err := auth.AuthenticateTVPIN(r.Context(), db, body.Username, body.PIN)
		if err != nil {
			writeJSONError(w, 500, "could not authenticate TV")
			return
		}
		if !ok {
			limiter.Fail(client)
			limiter.Fail("tv:all")
			writeJSONError(w, 401, "Username or TV PIN was not accepted")
			return
		}
		limiter.Succeed(client)
		signInSession(w, r, db, cookieName, http.StatusOK, user, true)
	}
}
