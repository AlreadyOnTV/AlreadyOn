// Package profile stores viewer profiles and their per-profile
// preferences in the database, so every client sees the same set (#117).
// PINs are handled in pin.go.
//
// Profiles are not accounts. Each profile belongs to exactly one user
// account (#283); callers must check Profile.UserID against the signed-in
// user before acting on a profile id. The audience tier is a household-level
// content filter (see internal/eligibility).
package profile

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/alreadyonapp/alreadyon/server/internal/jsonopt"
)

const (
	RoleAdmin  = "admin"
	RoleViewer = "viewer"

	MaxNameLength = 40
	MaxLevel      = 3
	// MaxProfilesPerUser bounds how many profiles one account can create,
	// so a shared demo account cannot fill the database.
	MaxProfilesPerUser = 12
)

var (
	ErrNotFound            = errors.New("profile not found")
	ErrInvalidName         = errors.New("name must be 1-40 characters with no control characters")
	ErrDuplicateName       = errors.New("another profile already has that name")
	ErrInvalidRole         = errors.New("role must be admin or viewer")
	ErrInvalidAudienceTier = errors.New("audienceTier must be young_child, teen, or adult")
	ErrInvalidIcon         = errors.New("icon must be a preset icon id")
	ErrInvalidLevel        = errors.New("fontLevel and brightnessLevel must be 0-3")
	ErrInvalidTimeFormat   = errors.New("guideTimeFormat must be 12h-padded, 12h, or 24h")
	ErrInvalidAudioLang    = errors.New("profile: audio language must be a two-letter lowercase code")
	ErrInvalidSubtitleLang = errors.New("subtitleLanguage must be a two-letter ISO 639-1 code such as en")
	ErrLastAdmin           = errors.New("there must be at least one admin profile")
	ErrTooManyProfiles     = errors.New("this account already has the maximum number of profiles")
)

// PresetIconIDs are the built-in avatars. The artwork itself lives in the
// web app; the server only needs the ids to validate a choice.
var PresetIconIDs = []string{"fox", "cat", "dog", "lion", "panda", "koala", "owl", "frog", "monkey", "rabbit"}

// UploadedIconPrefix marks an icon that is a server-stored upload rather
// than a preset id. Only the icon upload endpoint may set one.
const UploadedIconPrefix = "/profile-icons/"

var subtitleLangPattern = regexp.MustCompile(`^[a-z]{2}$`)

// Profile is one viewer profile. Preference fields are nil when the
// profile has not chosen one. There is deliberately no PIN or hash here.
type Profile struct {
	ID int64
	// UserID is the account that owns the profile.
	UserID          int64
	Name            string
	Role            string
	AudienceTier    string
	HasPIN          bool
	Icon            *string
	FontLevel       *int
	BrightnessLevel *int
	GuideTimeFormat *string
	// Audio defaults to English until chosen.
	AudioLanguage *string
	// Subtitle defaults: off, and English when switched on, until chosen.
	SubtitlesEnabled *bool
	SubtitleLanguage *string
	// LastWatchedChannelID is set only by a successful tune, not preference edits.
	LastWatchedChannelID *int64
}

// Draft is what a new profile needs.
type Draft struct {
	Name         string
	Role         string
	AudienceTier string
}

// Optional is a JSON field that distinguishes "absent" (leave alone) from
// "null" (clear it) from a value (set it), which a plain pointer can't.
type Optional[T any] = jsonopt.Optional[T]

// Patch changes only the fields that are present, so two devices editing
// different fields don't overwrite each other.
type Patch struct {
	Name             *string          `json:"name"`
	Role             *string          `json:"role"`
	AudienceTier     *string          `json:"audienceTier"`
	Icon             Optional[string] `json:"icon"`
	FontLevel        Optional[int]    `json:"fontLevel"`
	BrightnessLevel  Optional[int]    `json:"brightnessLevel"`
	GuideTimeFormat  Optional[string] `json:"guideTimeFormat"`
	SubtitlesEnabled Optional[bool]   `json:"subtitlesEnabled"`
	AudioLanguage    Optional[string] `json:"audioLanguage"`
	SubtitleLanguage Optional[string] `json:"subtitleLanguage"`
}

const selectColumns = `id, user_id, name, role, audience_tier, pin_hash IS NOT NULL, icon,
	font_level, brightness_level, guide_time_format,
	subtitles_enabled, subtitle_language, audio_language, last_watched_channel_id`

type rowScanner interface{ Scan(dest ...any) error }

func scan(row rowScanner) (Profile, error) {
	var p Profile
	var icon, format, subtitleLang sql.NullString
	var font, brightness, subtitles sql.NullInt64
	if err := row.Scan(&p.ID, &p.UserID, &p.Name, &p.Role, &p.AudienceTier, &p.HasPIN, &icon, &font, &brightness, &format, &subtitles, &subtitleLang, &p.AudioLanguage, &p.LastWatchedChannelID); err != nil {
		return Profile{}, err
	}
	p.SubtitleLanguage = nullString(subtitleLang)
	if subtitles.Valid {
		on := subtitles.Int64 == 1
		p.SubtitlesEnabled = &on
	}
	p.Icon, p.GuideTimeFormat = nullString(icon), nullString(format)
	p.FontLevel, p.BrightnessLevel = nullInt(font), nullInt(brightness)
	return p, nil
}

func nullString(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	return &s.String
}

func nullInt(n sql.NullInt64) *int {
	if !n.Valid {
		return nil
	}
	v := int(n.Int64)
	return &v
}

// List returns one account's profiles in switcher order: admin profiles
// first, then by audience from least to most restricted (Unrestricted, Teen,
// Kids), oldest first within each group. Clients display this order as-is.
func List(ctx context.Context, db *sql.DB, userID int64) ([]Profile, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+selectColumns+` FROM profiles WHERE user_id = ?
		ORDER BY role <> 'admin',
			CASE audience_tier WHEN 'adult' THEN 0 WHEN 'teen' THEN 1 ELSE 2 END,
			id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Profile
	for rows.Next() {
		p, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Get returns one profile, or ErrNotFound. It does not check ownership.
func Get(ctx context.Context, db *sql.DB, id int64) (Profile, error) {
	return get(ctx, db, id)
}

// RememberChannel commits just the channel identity, leaving preferences untouched.
func RememberChannel(ctx context.Context, db *sql.DB, id, channelID int64) error {
	res, err := db.ExecContext(ctx, `UPDATE profiles SET last_watched_channel_id = ?, updated_at_unix = unixepoch() WHERE id = ?`, channelID, id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNotFound
	}
	return nil
}

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func get(ctx context.Context, q queryer, id int64) (Profile, error) {
	p, err := scan(q.QueryRowContext(ctx, `SELECT `+selectColumns+` FROM profiles WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Profile{}, ErrNotFound
	}
	return p, err
}

// Create adds a profile with no PIN and no stored preferences to an account.
func Create(ctx context.Context, db *sql.DB, userID int64, d Draft) (Profile, error) {
	name, err := validateName(d.Name)
	if err != nil {
		return Profile{}, err
	}
	if err := validateRole(d.Role); err != nil {
		return Profile{}, err
	}
	if err := validateTier(d.AudienceTier); err != nil {
		return Profile{}, err
	}
	// The cap is checked in the same statement as the insert so concurrent
	// creates cannot overshoot it.
	res, err := db.ExecContext(ctx, `INSERT INTO profiles (user_id, name, role, audience_tier)
		SELECT ?, ?, ?, ? WHERE (SELECT COUNT(*) FROM profiles WHERE user_id = ?) < ?`,
		userID, name, d.Role, d.AudienceTier, userID, MaxProfilesPerUser)
	if err != nil {
		if isUniqueViolation(err) {
			return Profile{}, ErrDuplicateName
		}
		return Profile{}, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return Profile{}, err
	} else if n == 0 {
		return Profile{}, ErrTooManyProfiles
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Profile{}, err
	}
	return get(ctx, db, id)
}

// Update applies the fields present in patch and returns the result. An
// account's last admin profile can't be demoted.
func Update(ctx context.Context, db *sql.DB, id int64, patch Patch) (Profile, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Profile{}, err
	}
	defer tx.Rollback()

	p, err := get(ctx, tx, id)
	if err != nil {
		return Profile{}, err
	}
	if patch.Name != nil {
		if p.Name, err = validateName(*patch.Name); err != nil {
			return Profile{}, err
		}
	}
	if patch.Role != nil {
		if err := validateRole(*patch.Role); err != nil {
			return Profile{}, err
		}
		if p.Role == RoleAdmin && *patch.Role != RoleAdmin {
			if err := requireOtherAdmin(ctx, tx, id); err != nil {
				return Profile{}, err
			}
		}
		p.Role = *patch.Role
	}
	if patch.AudienceTier != nil {
		if err := validateTier(*patch.AudienceTier); err != nil {
			return Profile{}, err
		}
		p.AudienceTier = *patch.AudienceTier
	}
	if patch.Icon.Set {
		if patch.Icon.Value != nil && !isPresetIcon(*patch.Icon.Value) {
			return Profile{}, ErrInvalidIcon
		}
		p.Icon = patch.Icon.Value
	}
	if patch.FontLevel.Set {
		if v := patch.FontLevel.Value; v != nil && (*v < 0 || *v > MaxLevel) {
			return Profile{}, ErrInvalidLevel
		}
		p.FontLevel = patch.FontLevel.Value
	}
	if patch.BrightnessLevel.Set {
		if v := patch.BrightnessLevel.Value; v != nil && (*v < 0 || *v > MaxLevel) {
			return Profile{}, ErrInvalidLevel
		}
		p.BrightnessLevel = patch.BrightnessLevel.Value
	}
	if patch.GuideTimeFormat.Set {
		if v := patch.GuideTimeFormat.Value; v != nil && *v != "12h-padded" && *v != "12h" && *v != "24h" {
			return Profile{}, ErrInvalidTimeFormat
		}
		p.GuideTimeFormat = patch.GuideTimeFormat.Value
	}
	if patch.SubtitlesEnabled.Set {
		p.SubtitlesEnabled = patch.SubtitlesEnabled.Value
	}
	if patch.AudioLanguage.Set {
		if v := patch.AudioLanguage.Value; v != nil && !subtitleLangPattern.MatchString(*v) {
			return Profile{}, ErrInvalidAudioLang
		}
		p.AudioLanguage = patch.AudioLanguage.Value
	}
	if patch.SubtitleLanguage.Set {
		if v := patch.SubtitleLanguage.Value; v != nil && !subtitleLangPattern.MatchString(*v) {
			return Profile{}, ErrInvalidSubtitleLang
		}
		p.SubtitleLanguage = patch.SubtitleLanguage.Value
	}

	if err := save(ctx, tx, p); err != nil {
		return Profile{}, err
	}
	if err := tx.Commit(); err != nil {
		return Profile{}, err
	}
	return p, nil
}

// SetUploadedIcon records an icon file stored by the upload endpoint. It
// is separate from Update because a client may only choose preset ids.
func SetUploadedIcon(ctx context.Context, db *sql.DB, id int64, ref string) (Profile, error) {
	if !strings.HasPrefix(ref, UploadedIconPrefix) {
		return Profile{}, ErrInvalidIcon
	}
	res, err := db.ExecContext(ctx, `UPDATE profiles SET icon = ?, updated_at_unix = unixepoch() WHERE id = ?`, ref, id)
	if err != nil {
		return Profile{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Profile{}, ErrNotFound
	}
	return get(ctx, db, id)
}

// Delete removes a profile. An account's last admin profile can't be deleted.
func Delete(ctx context.Context, db *sql.DB, id int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	p, err := get(ctx, tx, id)
	if err != nil {
		return err
	}
	if p.Role == RoleAdmin {
		if err := requireOtherAdmin(ctx, tx, id); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM profiles WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func save(ctx context.Context, tx *sql.Tx, p Profile) error {
	_, err := tx.ExecContext(ctx, `UPDATE profiles SET name = ?, role = ?, audience_tier = ?, icon = ?,
		font_level = ?, brightness_level = ?, guide_time_format = ?,
		subtitles_enabled = ?, subtitle_language = ?, audio_language = ?,
		updated_at_unix = unixepoch() WHERE id = ?`,
		p.Name, p.Role, p.AudienceTier, p.Icon, p.FontLevel, p.BrightnessLevel, p.GuideTimeFormat,
		p.SubtitlesEnabled, p.SubtitleLanguage, p.AudioLanguage, p.ID)
	if isUniqueViolation(err) {
		return ErrDuplicateName
	}
	return err
}

func requireOtherAdmin(ctx context.Context, tx *sql.Tx, id int64) error {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM profiles WHERE role = 'admin' AND id != ?
		AND user_id = (SELECT user_id FROM profiles WHERE id = ?)`, id, id).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrLastAdmin
	}
	return nil
}

func validateName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" || utf8.RuneCountInString(name) > MaxNameLength {
		return "", ErrInvalidName
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", ErrInvalidName
		}
	}
	return name, nil
}

func validateRole(role string) error {
	if role != RoleAdmin && role != RoleViewer {
		return ErrInvalidRole
	}
	return nil
}

func validateTier(tier string) error {
	switch tier {
	case "young_child", "teen", "adult":
		return nil
	}
	return ErrInvalidAudienceTier
}

func isPresetIcon(id string) bool {
	for _, p := range PresetIconIDs {
		if p == id {
			return true
		}
	}
	return false
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
