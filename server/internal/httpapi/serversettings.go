package httpapi

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/alreadyonapp/alreadyon/server/internal/serversettings"
)

type serverSettingsResponse struct {
	ServerName       string `json:"serverName"`
	Transcoding      string `json:"transcoding"`
	TLSMode          string `json:"tlsMode"`
	ACMEProvider     string `json:"acmeProvider"`
	ACMEDomain       string `json:"acmeDomain"`
	DDNSProvider     string `json:"ddnsProvider"`
	HardwareDecoding bool   `json:"hardwareDecoding"`
}

func toServerSettingsResponse(s serversettings.Settings) serverSettingsResponse {
	return serverSettingsResponse{s.ServerName, s.Transcoding, s.TLSMode, s.ACMEProvider, s.ACMEDomain, s.DDNSProvider, s.HardwareDecoding}
}

func handleGetServerSettings(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s, err := serversettings.Get(r.Context(), db)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, toServerSettingsResponse(s))
	}
}

// handlePutServerSettings applies the fields present in the body and
// leaves the rest alone.
// onEncodingChange runs after the encoder or hardware decoding choice changed.
func handlePutServerSettings(db *sql.DB, onEncodingChange func()) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ServerName       *string `json:"serverName"`
			Transcoding      *string `json:"transcoding"`
			TLSMode          *string `json:"tlsMode"`
			ACMEProvider     *string `json:"acmeProvider"`
			ACMEDomain       *string `json:"acmeDomain"`
			DDNSProvider     *string `json:"ddnsProvider"`
			HardwareDecoding *bool   `json:"hardwareDecoding"`
		}
		if !decodeStrict(w, r, &body) {
			return
		}
		before, _ := serversettings.Get(r.Context(), db)
		s, err := serversettings.Save(r.Context(), db, serversettings.Update{
			ServerName: body.ServerName, Transcoding: body.Transcoding, TLSMode: body.TLSMode, ACMEProvider: body.ACMEProvider,
			ACMEDomain: body.ACMEDomain, DDNSProvider: body.DDNSProvider, HardwareDecoding: body.HardwareDecoding,
		})
		switch {
		case err == nil:
			if onEncodingChange != nil && (s.Transcoding != before.Transcoding || s.HardwareDecoding != before.HardwareDecoding) {
				onEncodingChange()
			}
			writeJSON(w, http.StatusOK, toServerSettingsResponse(s))
		case errors.Is(err, serversettings.ErrInvalidServerName), errors.Is(err, serversettings.ErrInvalidTranscoding), errors.Is(err, serversettings.ErrInvalidTLSMode),
			errors.Is(err, serversettings.ErrInvalidACME), errors.Is(err, serversettings.ErrInvalidDomain),
			errors.Is(err, serversettings.ErrInvalidDDNS), errors.Is(err, serversettings.ErrDecodeNeedsEncode):
			writeJSONError(w, http.StatusBadRequest, err.Error())
		default:
			writeJSONError(w, http.StatusInternalServerError, "internal error")
		}
	}
}
