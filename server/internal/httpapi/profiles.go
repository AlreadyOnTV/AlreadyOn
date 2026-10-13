package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/auth"
	"github.com/alreadyonapp/alreadyon/server/internal/profile"
)

const (
	maxProfileJSONBytes = 16 << 10
	maxProfileIconBytes = 1 << 20 // icons are shown at most ~64px; the web app sends 128px
)

// profileResponse never carries a PIN or hash — only whether one is set.
type profileResponse struct {
	ID                   int64   `json:"id"`
	Name                 string  `json:"name"`
	Role                 string  `json:"role"`
	AudienceTier         string  `json:"audienceTier"`
	HasPin               bool    `json:"hasPin"`
	Icon                 *string `json:"icon"`
	FontLevel            *int    `json:"fontLevel"`
	BrightnessLevel      *int    `json:"brightnessLevel"`
	GuideTimeFormat      *string `json:"guideTimeFormat"`
	SubtitlesEnabled     *bool   `json:"subtitlesEnabled"`
	AudioLanguage        *string `json:"audioLanguage"`
	SubtitleLanguage     *string `json:"subtitleLanguage"`
	LastWatchedChannelID *int64  `json:"lastWatchedChannelId"`
}

func toProfileResponse(p profile.Profile) profileResponse {
	return profileResponse{
		ID: p.ID, Name: p.Name, Role: p.Role, AudienceTier: p.AudienceTier, HasPin: p.HasPIN,
		Icon: p.Icon, FontLevel: p.FontLevel, BrightnessLevel: p.BrightnessLevel,
		GuideTimeFormat:  p.GuideTimeFormat,
		SubtitlesEnabled: p.SubtitlesEnabled, SubtitleLanguage: p.SubtitleLanguage,
		AudioLanguage:        p.AudioLanguage,
		LastWatchedChannelID: p.LastWatchedChannelID,
	}
}

func writeProfileError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, profile.ErrNotFound):
		writeJSONError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, profile.ErrDuplicateName), errors.Is(err, profile.ErrLastAdmin), errors.Is(err, profile.ErrTooManyProfiles):
		writeJSONError(w, http.StatusConflict, err.Error())
	case errors.Is(err, profile.ErrInvalidAudioLang), errors.Is(err, profile.ErrInvalidName), errors.Is(err, profile.ErrInvalidRole),
		errors.Is(err, profile.ErrInvalidAudienceTier), errors.Is(err, profile.ErrInvalidIcon),
		errors.Is(err, profile.ErrInvalidLevel),
		errors.Is(err, profile.ErrInvalidTimeFormat), errors.Is(err, profile.ErrInvalidSubtitleLang),
		errors.Is(err, profile.ErrInvalidPIN):
		writeJSONError(w, http.StatusBadRequest, err.Error())
	default:
		writeJSONError(w, http.StatusInternalServerError, "internal error")
	}
}

func profileID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid profile id")
		return 0, false
	}
	return id, true
}

// ownedProfile loads the {id} profile if the signed-in user may act on it:
// a Viewer only on its own account's profiles, an Owner on any. Someone
// else's profile is reported as not found so ids cannot be probed.
func ownedProfile(w http.ResponseWriter, r *http.Request, db *sql.DB) (profile.Profile, bool) {
	u, ok := requireUser(w, r)
	if !ok {
		return profile.Profile{}, false
	}
	id, ok := profileID(w, r)
	if !ok {
		return profile.Profile{}, false
	}
	p, err := profile.Get(r.Context(), db, id)
	if err == nil && p.UserID != u.ID && !u.IsOwner() {
		err = profile.ErrNotFound
	}
	if err != nil {
		writeProfileError(w, err)
		return profile.Profile{}, false
	}
	return p, true
}

// callerProfile loads a profile that a playback request names. Watching is
// always done as one of the signed-in account's own profiles, so even an
// Owner cannot tune as another account's profile; others read as not found.
func callerProfile(r *http.Request, db *sql.DB, id int64) (profile.Profile, error) {
	u, ok := auth.UserFrom(r.Context())
	if !ok {
		return profile.Profile{}, profile.ErrNotFound
	}
	p, err := profile.Get(r.Context(), db, id)
	if err != nil {
		return profile.Profile{}, err
	}
	if p.UserID != u.ID {
		return profile.Profile{}, profile.ErrNotFound
	}
	return p, nil
}

// profileAccount resolves whose profiles a list or create acts on: the
// caller's own, or for an Owner another account named by ?userId=.
func profileAccount(w http.ResponseWriter, r *http.Request, db *sql.DB) (int64, bool) {
	u, ok := requireUser(w, r)
	if !ok {
		return 0, false
	}
	raw := r.URL.Query().Get("userId")
	if raw == "" {
		return u.ID, true
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid userId")
		return 0, false
	}
	if id == u.ID {
		return id, true
	}
	if !u.IsOwner() {
		writeJSONCode(w, http.StatusForbidden, "forbidden", "only an owner can manage another account's profiles")
		return 0, false
	}
	if _, err := auth.GetUser(r.Context(), db, id); err != nil {
		if errors.Is(err, auth.ErrUserNotFound) {
			writeJSONError(w, http.StatusNotFound, "user not found")
		} else {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
		}
		return 0, false
	}
	return id, true
}

// decodeStrict reads a small JSON body and rejects unknown fields, so a
// typo (or an attempt to send "pin" to a route that ignores it) is an
// error rather than a silent no-op.
func decodeStrict(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxProfileJSONBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}

func handleListProfiles(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		userID, ok := profileAccount(w, r, db)
		if !ok {
			return
		}
		list, err := profile.List(r.Context(), db, userID)
		if err != nil {
			writeProfileError(w, err)
			return
		}
		resp := make([]profileResponse, 0, len(list))
		for _, p := range list {
			resp = append(resp, toProfileResponse(p))
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func handleCreateProfile(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := profileAccount(w, r, db)
		if !ok {
			return
		}
		var body struct {
			Name         string `json:"name"`
			Role         string `json:"role"`
			AudienceTier string `json:"audienceTier"`
		}
		if !decodeStrict(w, r, &body) {
			return
		}
		p, err := profile.Create(r.Context(), db, userID, profile.Draft{Name: body.Name, Role: body.Role, AudienceTier: body.AudienceTier})
		if err != nil {
			writeProfileError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, toProfileResponse(p))
	}
}

func handleGetProfile(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		p, ok := ownedProfile(w, r, db)
		if !ok {
			return
		}
		writeJSON(w, http.StatusOK, toProfileResponse(p))
	}
}

func handlePatchProfile(db *sql.DB, iconDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		before, ok := ownedProfile(w, r, db)
		if !ok {
			return
		}
		var patch profile.Patch
		if !decodeStrict(w, r, &patch) {
			return
		}
		p, err := profile.Update(r.Context(), db, before.ID, patch)
		if err != nil {
			writeProfileError(w, err)
			return
		}
		if patch.Icon.Set {
			deleteIfUploadedProfileIcon(iconDir, before.Icon)
		}
		writeJSON(w, http.StatusOK, toProfileResponse(p))
	}
}

func handleDeleteProfile(db *sql.DB, iconDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		before, ok := ownedProfile(w, r, db)
		if !ok {
			return
		}
		if err := profile.Delete(r.Context(), db, before.ID); err != nil {
			writeProfileError(w, err)
			return
		}
		deleteIfUploadedProfileIcon(iconDir, before.Icon)
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleSetProfilePIN(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := ownedProfile(w, r, db)
		if !ok {
			return
		}
		var body struct {
			Pin string `json:"pin"`
		}
		if !decodeStrict(w, r, &body) {
			return
		}
		if err := profile.SetPIN(r.Context(), db, p.ID, body.Pin); err != nil {
			writeProfileError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleClearProfilePIN(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := ownedProfile(w, r, db)
		if !ok {
			return
		}
		if err := profile.ClearPIN(r.Context(), db, p.ID); err != nil {
			writeProfileError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleVerifyProfilePIN reports whether a PIN matches. It only tells the
// calling client; it grants nothing on the server.
func handleVerifyProfilePIN(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := ownedProfile(w, r, db)
		if !ok {
			return
		}
		var body struct {
			Pin string `json:"pin"`
		}
		if !decodeStrict(w, r, &body) {
			return
		}
		res, err := profile.VerifyPIN(r.Context(), db, p.ID, body.Pin, time.Now())
		if err != nil {
			writeProfileError(w, err)
			return
		}
		switch {
		case res.Valid:
			writeJSON(w, http.StatusOK, map[string]any{"valid": true})
		case res.RetryAfter > 0:
			secs := int(res.RetryAfter.Round(time.Second) / time.Second)
			w.Header().Set("Retry-After", strconv.Itoa(secs))
			writeJSON(w, http.StatusTooManyRequests, map[string]any{
				"valid": false, "error": "too many incorrect PINs, try again later", "retryAfterSeconds": secs,
			})
		default:
			writeJSON(w, http.StatusUnauthorized, map[string]any{"valid": false, "error": "incorrect PIN"})
		}
	}
}

// handleUploadProfileIcon serves POST /api/profiles/{id}/icon: a multipart
// upload (form field "file"). Content is sniffed rather than trusting the
// client's filename or Content-Type, and the stored name is generated.
func handleUploadProfileIcon(db *sql.DB, iconDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		before, ok := ownedProfile(w, r, db)
		if !ok {
			return
		}
		id := before.ID

		r.Body = http.MaxBytesReader(w, r.Body, maxProfileIconBytes)
		if err := r.ParseMultipartForm(maxProfileIconBytes); err != nil {
			writeJSONError(w, http.StatusBadRequest, "upload too large or not a valid multipart form (max 1MB)")
			return
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "missing \"file\" form field")
			return
		}
		defer file.Close()

		sniff := make([]byte, 512)
		n, err := io.ReadFull(file, sniff)
		if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
			writeJSONError(w, http.StatusBadRequest, "failed to read upload")
			return
		}
		sniff = sniff[:n]
		ext, ok := profileIconExtByContentType[http.DetectContentType(sniff)]
		if !ok {
			writeJSONError(w, http.StatusBadRequest, "unsupported image type — use PNG, JPEG, GIF, or WebP")
			return
		}

		dir := iconDir
		if err := os.MkdirAll(dir, 0o755); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "failed to prepare icon storage")
			return
		}
		filename := fmt.Sprintf("%d-%d%s", id, time.Now().UnixNano(), ext)
		if err := writeUpload(filepath.Join(dir, filename), sniff, file); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "failed to save upload")
			return
		}

		p, err := profile.SetUploadedIcon(r.Context(), db, id, profile.UploadedIconPrefix+filename)
		if err != nil {
			_ = os.Remove(filepath.Join(dir, filename))
			writeProfileError(w, err)
			return
		}
		deleteIfUploadedProfileIcon(iconDir, before.Icon)
		writeJSON(w, http.StatusOK, toProfileResponse(p))
	}
}

func writeUpload(path string, head []byte, rest io.Reader) error {
	dest, err := os.Create(path)
	if err != nil {
		return err
	}
	if _, err := dest.Write(head); err != nil {
		dest.Close()
		return err
	}
	if _, err := io.Copy(dest, rest); err != nil {
		dest.Close()
		return err
	}
	return dest.Close()
}

// deleteIfUploadedProfileIcon removes an icon file once nothing refers to
// it; a preset id or empty icon is not a file this code owns.
func deleteIfUploadedProfileIcon(iconDir string, icon *string) {
	if icon == nil || !strings.HasPrefix(*icon, profile.UploadedIconPrefix) {
		return
	}
	name := strings.TrimPrefix(*icon, profile.UploadedIconPrefix)
	_ = os.Remove(filepath.Join(iconDir, filepath.Base(name)))
}

// Uploaded avatars accept raster images; SVG can contain executable scripts.
var profileIconExtByContentType = map[string]string{
	"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp",
}
