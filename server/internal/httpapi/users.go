package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/alreadyonapp/alreadyon/server/internal/auth"
	"github.com/alreadyonapp/alreadyon/server/internal/logging"
	"github.com/alreadyonapp/alreadyon/server/internal/profile"
)

// userResponse is an account as /api/users reports it. It never carries a
// password or hash.
type userResponse struct {
	ID                 int64  `json:"id"`
	Username           string `json:"username"`
	Role               string `json:"role"`
	Disabled           bool   `json:"disabled"`
	MustChangePassword bool   `json:"mustChangePassword"`
	PasswordLocked     bool   `json:"passwordLocked"`
	// MaxStreams is how many streams the account may play at once; 0 means
	// only the server-wide limits apply.
	MaxStreams    int   `json:"maxStreams"`
	CreatedAtUnix int64 `json:"createdAtUnix"`
	UpdatedAtUnix int64 `json:"updatedAtUnix"`
}

func toUserResponse(u auth.User) userResponse {
	return userResponse{
		ID: u.ID, Username: u.Username, Role: string(u.Role), Disabled: u.Disabled,
		MustChangePassword: u.MustChangePassword, PasswordLocked: u.PasswordLocked,
		MaxStreams: u.MaxStreams, CreatedAtUnix: u.CreatedAtUnix, UpdatedAtUnix: u.UpdatedAtUnix,
	}
}

func writeUserError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrUserNotFound):
		writeJSONError(w, http.StatusNotFound, "user not found")
	case errors.Is(err, auth.ErrUsernameTaken):
		writeJSONCode(w, http.StatusConflict, "username_taken", "That username is already in use.")
	case errors.Is(err, auth.ErrLastOwner):
		writeJSONCode(w, http.StatusConflict, "last_owner", "The last enabled owner cannot be removed, disabled or demoted.")
	case errors.Is(err, auth.ErrTooManyUsers):
		writeJSONCode(w, http.StatusConflict, "too_many_users", strings.TrimPrefix(err.Error(), "auth: "))
	case errors.Is(err, auth.ErrInvalidUsername), errors.Is(err, auth.ErrInvalidPassword),
		errors.Is(err, auth.ErrInvalidRole), errors.Is(err, auth.ErrInvalidMaxStream), errors.Is(err, auth.ErrLockedMustChange):
		writeJSONError(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), "auth: "))
	default:
		writeJSONError(w, http.StatusInternalServerError, "internal error")
	}
}

func userIDParam(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid user id")
		return 0, false
	}
	return id, true
}

// handleListUsers serves GET /api/users (owner only).
func handleListUsers(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		list, err := auth.ListUsers(r.Context(), db)
		if err != nil {
			writeUserError(w, err)
			return
		}
		out := make([]userResponse, 0, len(list))
		for _, u := range list {
			out = append(out, toUserResponse(u))
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// handleGetUser serves GET /api/users/{id} (owner only).
func handleGetUser(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		id, ok := userIDParam(w, r)
		if !ok {
			return
		}
		u, err := auth.GetUser(r.Context(), db, id)
		if err != nil {
			writeUserError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toUserResponse(u))
	}
}

// handleCreateUser serves POST /api/users (owner only). The new account gets
// the default Admin, Teen and Kids profiles. maxStreams defaults to
// auth.DefaultViewerStreams for a viewer and 0 (unlimited) for an owner.
func handleCreateUser(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := requireUser(w, r)
		if !ok {
			return
		}
		var body struct {
			Username           string `json:"username"`
			Password           string `json:"password"`
			Role               string `json:"role"`
			MustChangePassword bool   `json:"mustChangePassword"`
			PasswordLocked     bool   `json:"passwordLocked"`
			MaxStreams         *int   `json:"maxStreams"`
		}
		if !decodeStrict(w, r, &body) {
			return
		}
		role := auth.Role(body.Role)
		streams := 0
		if role == auth.RoleViewer {
			streams = auth.DefaultViewerStreams
		}
		if body.MaxStreams != nil {
			streams = *body.MaxStreams
		}
		u, err := auth.CreateUser(r.Context(), db, auth.NewUser{
			Username: body.Username, Password: body.Password, Role: role,
			MustChangePassword: body.MustChangePassword, PasswordLocked: body.PasswordLocked, MaxStreams: streams,
		})
		if err != nil {
			writeUserError(w, err)
			return
		}
		logging.Info("user %q (%s, id %d) created by %s", u.Username, u.Role, u.ID, actor.Username)
		writeJSON(w, http.StatusCreated, toUserResponse(u))
	}
}

// handlePatchUser serves PATCH /api/users/{id} (owner only). Changing the
// role or disabling the account signs it out everywhere.
func handlePatchUser(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := requireUser(w, r)
		if !ok {
			return
		}
		id, ok := userIDParam(w, r)
		if !ok {
			return
		}
		var body struct {
			Username           *string `json:"username"`
			Role               *string `json:"role"`
			Disabled           *bool   `json:"disabled"`
			MustChangePassword *bool   `json:"mustChangePassword"`
			PasswordLocked     *bool   `json:"passwordLocked"`
			MaxStreams         *int    `json:"maxStreams"`
		}
		if !decodeStrict(w, r, &body) {
			return
		}
		patch := auth.UserPatch{
			Username: body.Username, Disabled: body.Disabled, MustChangePassword: body.MustChangePassword,
			PasswordLocked: body.PasswordLocked, MaxStreams: body.MaxStreams,
		}
		if body.Role != nil {
			role := auth.Role(*body.Role)
			patch.Role = &role
		}
		u, err := auth.UpdateUser(r.Context(), db, id, patch)
		if err != nil {
			writeUserError(w, err)
			return
		}
		logging.Info("user %q (id %d) updated by %s", u.Username, u.ID, actor.Username)
		writeJSON(w, http.StatusOK, toUserResponse(u))
	}
}

// handleSetUserPassword serves PUT /api/users/{id}/password (owner only): an
// owner sets another account's password without knowing the old one. The
// account is signed out everywhere.
func handleSetUserPassword(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := requireUser(w, r)
		if !ok {
			return
		}
		id, ok := userIDParam(w, r)
		if !ok {
			return
		}
		var body struct {
			Password           string `json:"password"`
			MustChangePassword bool   `json:"mustChangePassword"`
		}
		if !decodeStrict(w, r, &body) {
			return
		}
		if err := auth.SetUserPassword(r.Context(), db, id, body.Password, body.MustChangePassword); err != nil {
			writeUserError(w, err)
			return
		}
		logging.Info("password for user id %d reset by %s", id, actor.Username)
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleDeleteUser serves DELETE /api/users/{id} (owner only). The account's
// profiles, sessions and API keys go with it. An owner cannot delete itself,
// so a mistaken click cannot lock the signed-in owner out mid-request.
func handleDeleteUser(db *sql.DB, iconDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := requireUser(w, r)
		if !ok {
			return
		}
		id, ok := userIDParam(w, r)
		if !ok {
			return
		}
		if id == actor.ID {
			writeJSONCode(w, http.StatusConflict, "self_delete", "You cannot delete the account you are signed in with.")
			return
		}
		icons, err := auth.DeleteUser(r.Context(), db, id)
		if err != nil {
			writeUserError(w, err)
			return
		}
		for _, icon := range icons {
			if strings.HasPrefix(icon, profile.UploadedIconPrefix) {
				deleteIfUploadedProfileIcon(iconDir, &icon)
			}
		}
		logging.Info("user id %d deleted by %s", id, actor.Username)
		w.WriteHeader(http.StatusNoContent)
	}
}
