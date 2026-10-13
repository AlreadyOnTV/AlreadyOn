package httpapi

import (
	"errors"
	"net/http"

	"github.com/alreadyonapp/alreadyon/server/internal/ddns"
)

// RegisterDDNS is mounted behind management authentication/CSRF. Provider
// failures are stored as sanitized status; secret inputs are never returned.
func RegisterDDNS(mux *http.ServeMux, m *ddns.Manager) {
	respond := func(w http.ResponseWriter, s ddns.Status, err error) {
		switch {
		case err == nil:
			writeJSON(w, 200, s)
		case errors.Is(err, ddns.ErrInvalid):
			writeJSONError(w, 400, "Enter a valid public hostname and required provider credentials. Save the configuration before testing.")
		case errors.Is(err, ddns.ErrBusy):
			w.Header().Set("Retry-After", "1")
			writeJSONError(w, 409, err.Error())
		default:
			writeJSONError(w, 500, "could not access DDNS configuration")
		}
	}
	mux.HandleFunc("GET /api/settings/ddns", func(w http.ResponseWriter, r *http.Request) { s, err := m.Status(r.Context()); respond(w, s, err) })
	mux.HandleFunc("PUT /api/settings/ddns", func(w http.ResponseWriter, r *http.Request) {
		var u ddns.Update
		if !decodeStrict(w, r, &u) {
			return
		}
		s, err := m.Save(r.Context(), u)
		if err == nil && u.Provider != "none" && !u.ClearSecret {
			s, err = m.Run(r.Context(), u.Provider)
			if errors.Is(err, ddns.ErrBusy) {
				s, err = m.Status(r.Context())
			}
		}
		respond(w, s, err)
	})
	mux.HandleFunc("POST /api/settings/ddns/test", func(w http.ResponseWriter, r *http.Request) {
		var u struct {
			Provider string `json:"provider"`
		}
		if !decodeStrict(w, r, &u) {
			return
		}
		s, err := m.Run(r.Context(), u.Provider)
		respond(w, s, err)
	})
}
