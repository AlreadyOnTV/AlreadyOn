package httpapi

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/alreadyonapp/alreadyon/server/internal/instancetls"
)

// RegisterTLS exposes public trust metadata/download and administrator SAN
// changes behind the existing management authentication and CSRF boundary.
func RegisterTLS(mux *http.ServeMux, manager *instancetls.Manager) {
	mux.HandleFunc("GET /api/tv/trust", func(w http.ResponseWriter, r *http.Request) {
		if !tvLAN(w, r) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"certificate": string(manager.TrustPEM()), "fingerprint": manager.Status().Fingerprint})
	})
	mux.HandleFunc("PUT /api/settings/tls/acme", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Hostname string `json:"hostname"`
			Email    string `json:"email"`
			Agree    bool   `json:"agree"`
		}
		if !decodeStrict(w, r, &body) {
			return
		}
		if err := manager.SaveACME(r.Context(), body.Hostname, body.Email, body.Agree); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, manager.Status())
	})
	mux.HandleFunc("POST /api/settings/tls/acme", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Hostname string `json:"hostname"`
			Email    string `json:"email"`
			Agree    bool   `json:"agree"`
			Staging  bool   `json:"staging"`
		}
		if !decodeStrict(w, r, &body) {
			return
		}
		if err := manager.RequestACME(r.Context(), body.Hostname, body.Email, body.Agree, body.Staging); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, manager.Status())
	})
	mux.HandleFunc("POST /api/settings/tls/upload", func(w http.ResponseWriter, r *http.Request) {
		// A bounded, write-only JSON body keeps private keys out of URLs/logs.
		var body struct {
			Certificate string `json:"certificate"`
			PrivateKey  string `json:"privateKey"`
			CA          string `json:"ca"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 300<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, "provide bounded PEM certificate, privateKey and ca fields")
			return
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			writeJSONError(w, http.StatusBadRequest, "provide one JSON object")
			return
		}
		if err := manager.Upload(r.Context(), []byte(body.Certificate), []byte(body.PrivateKey), []byte(body.CA)); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, manager.Status())
	})
	mux.HandleFunc("POST /api/settings/tls/self-signed", func(w http.ResponseWriter, r *http.Request) {
		if err := manager.UseSelfSigned(r.Context()); err != nil {
			writeJSONError(w, http.StatusBadRequest, "could not restore the instance certificate")
			return
		}
		writeJSON(w, http.StatusOK, manager.Status())
	})
	mux.HandleFunc("GET /api/settings/tls", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, manager.Status())
	})
	mux.HandleFunc("GET /api/settings/tls/ca.pem", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-pem-file")
		w.Header().Set("Content-Disposition", `attachment; filename="alreadyon-instance-ca.pem"`)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(manager.TrustPEM())
	})
	mux.HandleFunc("PUT /api/settings/tls", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Names []string `json:"names"`
		}
		if !decodeStrict(w, r, &body) {
			return
		}
		if err := manager.SetNames(r.Context(), body.Names); err != nil {
			// Do not reveal database/key material or filesystem paths in errors.
			writeJSONError(w, http.StatusBadRequest, "could not apply certificate names; supply 1–32 DNS names/IP addresses without URLs or ports")
			return
		}
		writeJSON(w, http.StatusOK, manager.Status())
	})
}
