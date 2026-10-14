package profile

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/alreadyonapp/alreadyon/server/internal/storage"
)

func init() { hashCost = bcrypt.MinCost }

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db.DB
}

func mustPatch(t *testing.T, body string) Patch {
	t.Helper()
	var p Patch
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatalf("unmarshal %s: %v", body, err)
	}
	return p
}

func TestFreshInstall_SeedsAdminKidsAndTeen(t *testing.T) {
	list, err := List(context.Background(), openDB(t), 1)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("got %d profiles, want 3", len(list))
	}
	admin, teen, kids := list[0], list[1], list[2]
	if teen.Name != "Teen" || teen.Role != RoleViewer || teen.AudienceTier != "teen" || teen.HasPIN {
		t.Errorf("second profile = %+v, want Teen/viewer/teen without a PIN", teen)
	}
	if admin.Name != "Admin" || admin.Role != RoleAdmin || admin.HasPIN {
		t.Errorf("first profile = %+v, want an Admin admin without a PIN", admin)
	}
	if kids.Name != "Kids" || kids.Role != RoleViewer || kids.AudienceTier != "young_child" || kids.HasPIN {
		t.Errorf("third profile = %+v, want Kids/viewer/young_child without a PIN", kids)
	}
}

func TestRememberChannel_PersistenceIsolationAndDeletion(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := storage.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { store.Close() }()
	if _, err := store.Exec(`INSERT INTO channels(id,number,name) VALUES(998,998,'First'),(999,999,'Second')`); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]int64{{1, 998}, {2, 998}, {2, 999}} {
		if err := RememberChannel(ctx, store.DB, pair[0], pair[1]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Update(ctx, store.DB, 1, mustPatch(t, `{"brightnessLevel":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := RememberChannel(ctx, store.DB, 123456, 998); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown profile: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = storage.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[int64]int64{1: 998, 2: 999} {
		p, err := Get(ctx, store.DB, id)
		if err != nil || p.LastWatchedChannelID == nil || *p.LastWatchedChannelID != want {
			t.Fatalf("profile %d after reopen = %+v, %v; want channel %d", id, p, err, want)
		}
	}
	if err := RememberChannel(ctx, store.DB, 1, 123456); err == nil {
		t.Fatal("nonexistent channel should violate the foreign key")
	}
	if _, err := store.Exec(`DELETE FROM channels WHERE id=999`); err != nil {
		t.Fatal(err)
	}
	p, err := Get(ctx, store.DB, 2)
	if err != nil || p.LastWatchedChannelID != nil {
		t.Fatalf("deleted channel = %+v, %v; want cleared reference", p, err)
	}
	p, err = Get(ctx, store.DB, 1)
	if err != nil || p.LastWatchedChannelID == nil || *p.LastWatchedChannelID != 998 {
		t.Fatalf("other profile after deletion = %+v, %v", p, err)
	}
}

func TestCreate_Validation(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	cases := []struct {
		name string
		d    Draft
		want error
	}{
		{"empty name", Draft{Name: "  ", Role: RoleViewer, AudienceTier: "adult"}, ErrInvalidName},
		{"long name", Draft{Name: string(make([]rune, 41)), Role: RoleViewer, AudienceTier: "adult"}, ErrInvalidName},
		{"control char", Draft{Name: "a\nb", Role: RoleViewer, AudienceTier: "adult"}, ErrInvalidName},
		{"bad role", Draft{Name: "X", Role: "root", AudienceTier: "adult"}, ErrInvalidRole},
		{"bad tier", Draft{Name: "X", Role: RoleViewer, AudienceTier: "toddler"}, ErrInvalidAudienceTier},
		{"duplicate ignoring case", Draft{Name: "kIDS", Role: RoleViewer, AudienceTier: "adult"}, ErrDuplicateName},
	}
	for _, c := range cases {
		if _, err := Create(ctx, db, 1, c.d); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
	}
	p, err := Create(ctx, db, 1, Draft{Name: "  Guest  ", Role: RoleViewer, AudienceTier: "teen"})
	if err != nil || p.Name != "Guest" {
		t.Fatalf("Create = %+v, %v; want a trimmed name", p, err)
	}
}

func TestUpdate_PartialPatchOnlyChangesSentFields(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	if _, err := Update(ctx, db, 1, mustPatch(t, `{"brightnessLevel":1,"fontLevel":2}`)); err != nil {
		t.Fatalf("first patch: %v", err)
	}
	// A second device changes a different field; the first must survive.
	p, err := Update(ctx, db, 1, mustPatch(t, `{"guideTimeFormat":"24h"}`))
	if err != nil {
		t.Fatalf("second patch: %v", err)
	}
	if p.BrightnessLevel == nil || *p.BrightnessLevel != 1 || p.FontLevel == nil || *p.FontLevel != 2 {
		t.Errorf("earlier fields lost: brightness=%v font=%v", p.BrightnessLevel, p.FontLevel)
	}
	if p.GuideTimeFormat == nil || *p.GuideTimeFormat != "24h" {
		t.Errorf("guideTimeFormat = %v, want 24h", p.GuideTimeFormat)
	}
	if p.Name != "Admin" {
		t.Errorf("name = %q, want it untouched", p.Name)
	}
}

func TestUpdate_NullClearsAPreference(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	Update(ctx, db, 1, mustPatch(t, `{"brightnessLevel":1}`))
	p, err := Update(ctx, db, 1, mustPatch(t, `{"brightnessLevel":null}`))
	if err != nil || p.BrightnessLevel != nil {
		t.Fatalf("brightness = %v, err = %v; want cleared", p.BrightnessLevel, err)
	}
}

func TestUpdate_Validation(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	cases := map[string]error{
		`{"role":"root"}`:                            ErrInvalidRole,
		`{"audienceTier":"x"}`:                       ErrInvalidAudienceTier,
		`{"fontLevel":4}`:                            ErrInvalidLevel,
		`{"brightnessLevel":-1}`:                     ErrInvalidLevel,
		`{"guideTimeFormat":"weird"}`:                ErrInvalidTimeFormat,
		`{"icon":"/etc/passwd"}`:                     ErrInvalidIcon,
		`{"icon":"` + UploadedIconPrefix + `x.png"}`: ErrInvalidIcon, // uploads only via the upload endpoint
		`{"name":"Kids"}`:                            ErrDuplicateName,
	}
	for body, want := range cases {
		if _, err := Update(ctx, db, 1, mustPatch(t, body)); !errors.Is(err, want) {
			t.Errorf("%s: err = %v, want %v", body, err, want)
		}
	}
	if _, err := Update(ctx, db, 1, mustPatch(t, `{"icon":"fox","guideTimeFormat":"24h"}`)); err != nil {
		t.Errorf("valid patch rejected: %v", err)
	}
	if _, err := Update(ctx, db, 999, mustPatch(t, `{}`)); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing profile: err = %v, want ErrNotFound", err)
	}
}

func TestLastAdmin_CannotBeDemotedOrDeleted(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	if _, err := Update(ctx, db, 1, mustPatch(t, `{"role":"viewer"}`)); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("demote: err = %v, want ErrLastAdmin", err)
	}
	if err := Delete(ctx, db, 1); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("delete: err = %v, want ErrLastAdmin", err)
	}
	second, err := Create(ctx, db, 1, Draft{Name: "Second", Role: RoleAdmin, AudienceTier: "adult"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := Update(ctx, db, 1, mustPatch(t, `{"role":"viewer"}`)); err != nil {
		t.Errorf("demote with another admin present: %v", err)
	}
	if err := Delete(ctx, db, second.ID); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("delete now-last admin: err = %v, want ErrLastAdmin", err)
	}
}

func TestDelete(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	if err := Delete(ctx, db, 2); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := Get(ctx, db, 2); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after delete: err = %v, want ErrNotFound", err)
	}
	if err := Delete(ctx, db, 2); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete: err = %v, want ErrNotFound", err)
	}
}

func TestSetUploadedIcon(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	p, err := SetUploadedIcon(ctx, db, 1, UploadedIconPrefix+"1-1.png")
	if err != nil || p.Icon == nil || *p.Icon != UploadedIconPrefix+"1-1.png" {
		t.Fatalf("SetUploadedIcon = %+v, %v", p, err)
	}
	if _, err := SetUploadedIcon(ctx, db, 1, "/elsewhere/x.png"); !errors.Is(err, ErrInvalidIcon) {
		t.Errorf("foreign path: err = %v, want ErrInvalidIcon", err)
	}
}

func TestPIN_SetVerifyClear(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	now := time.Now()

	for _, bad := range []string{"", "123", "1234567", "12a4", "١٢٣٤"} {
		if err := SetPIN(ctx, db, 1, bad); !errors.Is(err, ErrInvalidPIN) {
			t.Errorf("SetPIN(%q): err = %v, want ErrInvalidPIN", bad, err)
		}
	}
	if err := SetPIN(ctx, db, 1, "1234"); err != nil {
		t.Fatalf("SetPIN: %v", err)
	}
	if p, _ := Get(ctx, db, 1); !p.HasPIN {
		t.Error("HasPIN = false after SetPIN")
	}
	if r, err := VerifyPIN(ctx, db, 1, "1234", now); err != nil || !r.Valid {
		t.Errorf("right PIN: %+v, %v", r, err)
	}
	if r, _ := VerifyPIN(ctx, db, 1, "9999", now); r.Valid {
		t.Error("wrong PIN verified")
	}
	if err := ClearPIN(ctx, db, 1); err != nil {
		t.Fatalf("ClearPIN: %v", err)
	}
	if p, _ := Get(ctx, db, 1); p.HasPIN {
		t.Error("HasPIN = true after ClearPIN")
	}
	if r, _ := VerifyPIN(ctx, db, 1, "anything", now); !r.Valid {
		t.Error("a profile without a PIN should always verify")
	}
	if err := SetPIN(ctx, db, 999, "1234"); !errors.Is(err, ErrNotFound) {
		t.Errorf("SetPIN missing profile: err = %v, want ErrNotFound", err)
	}
}

func TestPIN_HashIsStoredNotThePIN(t *testing.T) {
	db := openDB(t)
	SetPIN(context.Background(), db, 1, "4321")
	var stored string
	if err := db.QueryRow(`SELECT pin_hash FROM profiles WHERE id = 1`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == "4321" || bcrypt.CompareHashAndPassword([]byte(stored), []byte("4321")) != nil {
		t.Errorf("pin_hash = %q, want a bcrypt hash of the PIN", stored)
	}
}

func TestPIN_WrongAttemptsBackOff(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	SetPIN(ctx, db, 1, "1234")
	now := time.Now()

	for i := 0; i < freeAttempts; i++ {
		if r, _ := VerifyPIN(ctx, db, 1, "0000", now); r.Valid || r.RetryAfter != 0 {
			t.Fatalf("attempt %d: %+v, want a plain failure", i+1, r)
		}
	}
	r, _ := VerifyPIN(ctx, db, 1, "0000", now)
	if r.RetryAfter != lockBase {
		t.Fatalf("third failure RetryAfter = %v, want %v", r.RetryAfter, lockBase)
	}
	// While locked even the right PIN is refused, and isn't compared.
	if r, _ := VerifyPIN(ctx, db, 1, "1234", now.Add(time.Second)); r.Valid || r.RetryAfter <= 0 {
		t.Errorf("during lockout: %+v, want locked", r)
	}
	// After the lock expires the right PIN works and resets the counter.
	if r, _ := VerifyPIN(ctx, db, 1, "1234", now.Add(lockBase+time.Second)); !r.Valid {
		t.Errorf("after lockout: %+v, want valid", r)
	}
	if r, _ := VerifyPIN(ctx, db, 1, "0000", now.Add(time.Minute)); r.RetryAfter != 0 {
		t.Errorf("failure after a success = %+v, want the counter reset", r)
	}
}

func TestLockDuration_DoublesUpToTheCap(t *testing.T) {
	if got := lockDuration(freeAttempts + 1); got != lockBase {
		t.Errorf("first lock = %v, want %v", got, lockBase)
	}
	if got := lockDuration(freeAttempts + 2); got != 2*lockBase {
		t.Errorf("second lock = %v, want %v", got, 2*lockBase)
	}
	if got := lockDuration(100); got != lockMax {
		t.Errorf("lock after many failures = %v, want the %v cap", got, lockMax)
	}
}

func TestSetPIN_ClearsALockout(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	SetPIN(ctx, db, 1, "1234")
	now := time.Now()
	for i := 0; i <= freeAttempts; i++ {
		VerifyPIN(ctx, db, 1, "0000", now)
	}
	SetPIN(ctx, db, 1, "5678")
	if r, _ := VerifyPIN(ctx, db, 1, "5678", now); !r.Valid {
		t.Errorf("after reset: %+v, want valid", r)
	}
}

func TestUpdate_SubtitleDefaults(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	p, err := Get(ctx, db, 1)
	if err != nil || p.SubtitlesEnabled != nil || p.SubtitleLanguage != nil {
		t.Fatalf("a new profile should have no subtitle choice: %+v %v", p, err)
	}
	p, err = Update(ctx, db, 1, mustPatch(t, `{"subtitlesEnabled":true,"subtitleLanguage":"es"}`))
	if err != nil || p.SubtitlesEnabled == nil || !*p.SubtitlesEnabled || p.SubtitleLanguage == nil || *p.SubtitleLanguage != "es" {
		t.Fatalf("subtitle defaults not saved: %+v %v", p, err)
	}
	// Off is a choice too, and is not the same as unset.
	p, _ = Update(ctx, db, 1, mustPatch(t, `{"subtitlesEnabled":false}`))
	if p.SubtitlesEnabled == nil || *p.SubtitlesEnabled || *p.SubtitleLanguage != "es" {
		t.Fatalf("disabling changed the language or was lost: %+v", p)
	}
	p, _ = Update(ctx, db, 1, mustPatch(t, `{"subtitlesEnabled":null,"subtitleLanguage":null}`))
	if p.SubtitlesEnabled != nil || p.SubtitleLanguage != nil {
		t.Fatalf("null should clear both: %+v", p)
	}
}

func TestAudioLanguagePreference(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	p, err := Update(ctx, db, 1, mustPatch(t, `{"audioLanguage":"ja"}`))
	if err != nil || p.AudioLanguage == nil || *p.AudioLanguage != "ja" {
		t.Fatalf("audio preference: %+v %v", p, err)
	}
	p, err = Get(ctx, db, 1)
	if err != nil || p.AudioLanguage == nil || *p.AudioLanguage != "ja" {
		t.Fatal("not persisted", err)
	}
	if _, err = Update(ctx, db, 1, mustPatch(t, `{"audioLanguage":"invalid"}`)); err != ErrInvalidAudioLang {
		t.Fatal("invalid language accepted", err)
	}
	p, err = Update(ctx, db, 1, mustPatch(t, `{"audioLanguage":null}`))
	if err != nil || p.AudioLanguage != nil {
		t.Fatal("reset failed", err)
	}
}

func TestSubtitleModePersistsPerProfileAndSupportsLegacyClients(t *testing.T) {
	db := openDB(t)
	ctx := context.Background()
	original, err := Get(ctx, db, 1)
	if err != nil || original.SubtitleMode != nil {
		t.Fatalf("default: %+v %v", original, err)
	}
	for _, mode := range []string{"always", "other_audio", "off"} {
		if _, err = Update(ctx, db, 1, mustPatch(t, `{"subtitleMode":"`+mode+`"}`)); err != nil {
			t.Fatal(err)
		}
		saved, err := Get(ctx, db, 1)
		if err != nil || saved.SubtitleMode == nil || *saved.SubtitleMode != mode || saved.SubtitlesEnabled == nil || *saved.SubtitlesEnabled != (mode == "always") {
			t.Fatalf("not persisted/legacy mismatch: %+v %v", saved, err)
		}
	}
	other, err := Get(ctx, db, 2)
	if err != nil || other.SubtitleMode != nil {
		t.Fatalf("leaked across profiles: %+v %v", other, err)
	}
	saved, err := Update(ctx, db, 1, mustPatch(t, `{"subtitleLanguage":"es"}`))
	if err != nil || saved.SubtitleMode == nil || *saved.SubtitleMode != "off" {
		t.Fatal("unrelated edit lost mode", err)
	}
	if _, err = Update(ctx, db, 1, mustPatch(t, `{"subtitleMode":"invalid"}`)); err != ErrInvalidSubtitleMode {
		t.Fatal("invalid mode accepted", err)
	}
	saved, err = Update(ctx, db, 1, mustPatch(t, `{"subtitlesEnabled":true}`))
	if err != nil || saved.SubtitleMode != nil || saved.SubtitlesEnabled == nil || !*saved.SubtitlesEnabled {
		t.Fatalf("legacy toggle: %+v %v", saved, err)
	}
	saved, err = Update(ctx, db, 1, mustPatch(t, `{"subtitleMode":null}`))
	if err != nil || saved.SubtitleMode != nil {
		t.Fatalf("nullable mode: %+v %v", saved, err)
	}
}
