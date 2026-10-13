package httpapi

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/auth"
	"github.com/alreadyonapp/alreadyon/server/internal/playback"
)

type httpClock struct{ now time.Time }

func (c *httpClock) Now() time.Time { return c.now }

func TestPlaybackHTTPAuthorizationAndCORS(t *testing.T) {
	db := openTestDB(t)
	raw, key, err := auth.GenerateAPIKey(context.Background(), db, testOwnerID(t, db), "test")
	if err != nil {
		t.Fatal(err)
	}
	clock := &httpClock{time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)}
	sessions := playback.NewSessions(clock)
	p := NewPlaybackHTTP(db, sessions, nil)
	management := auth.RequireAuth(db, NewRouter(t.TempDir(), t.TempDir(), db, p))
	req := httptest.NewRequest("POST", "http://localhost:19900/api/playback/probe", nil)
	rec := httptest.NewRecorder()
	management.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("unauthorized issuance: %d", rec.Code)
	}
	req.Header.Set("Authorization", "Bearer "+raw)
	rec = httptest.NewRecorder()
	management.ServeHTTP(rec, req)
	if rec.Code != 201 {
		t.Fatal(rec.Body.String())
	}
	var issued struct {
		Token    string `json:"token"`
		MediaURL string `json:"mediaUrl"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &issued); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rec.Body.String(), raw) || issued.MediaURL != "http://localhost:19900/media/0" {
		t.Fatal("invalid credential exchange")
	}
	media := management
	check := func(path, origin string, want int) {
		t.Helper()
		req := httptest.NewRequest("GET", "http://localhost:19900"+path, nil)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		media.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
		}
	}
	check("/media/0/probe?bytes=16", "", 401)
	// Even a valid management API key must not replace a scoped media token.
	keyOnly := httptest.NewRequest("GET", "http://localhost:19900/media/0/probe", nil)
	keyOnly.Header.Set("Authorization", "Bearer "+raw)
	keyResponse := httptest.NewRecorder()
	management.ServeHTTP(keyResponse, keyOnly)
	if keyResponse.Code != 401 {
		t.Fatalf("management credential authorized media: %d", keyResponse.Code)
	}
	check("/media/1/probe?token="+issued.Token, "", 401)
	check("/media/0/probe?token="+issued.Token, "https://evil.example", 403)
	check("/media/0/probe?bytes=16&token="+issued.Token, "http://localhost:19900", 200)
	check("/media/0/probe?bytes=999999999&token="+issued.Token, "", 400)
	clock.now = clock.now.Add(playback.SessionTTL)
	check("/media/0/probe?token="+issued.Token, "", 401)
	token, _, err := sessions.Issue(key.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.RevokeAPIKey(context.Background(), db, key.ID); err != nil {
		t.Fatal(err)
	}
	check("/media/0/probe?token="+token, "", 401)
}

func TestPlaybackProbeBudgetAndDeliverySettings(t *testing.T) {
	db := openTestDB(t)
	clock := &httpClock{time.Now()}
	s := playback.NewSessions(clock)
	token, _, err := s.Issue(1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !s.SpendProbe(token, 32<<20) || s.SpendProbe(token, 1) {
		t.Fatal("probe was unbounded")
	}
	for _, url := range []string{"https://user:password@media.example", "https://media.example/path", "javascript:bad", "https://media.example?token=bad"} {
		if err := (playback.Delivery{MediaURL: url}).Validate(); err == nil {
			t.Fatalf("accepted %s", url)
		}
	}
	d := playback.Delivery{MediaURL: "https://media.example", AllowedOrigins: []string{"https://tv.example"}}
	if err := playback.SaveDelivery(context.Background(), db, d); err != nil {
		t.Fatal(err)
	}
	got, err := playback.GetDelivery(context.Background(), db)
	if err != nil || got.MediaURL != d.MediaURL {
		t.Fatal("delivery did not persist")
	}
}

// A stale delivery override and spoofed forwarding headers must never send a
// local or proxied browser back to the removed streaming listener.
func TestMediaOriginSharesApplicationListener(t *testing.T) {
	db := openTestDB(t)
	if err := playback.SaveDelivery(context.Background(), db, playback.Delivery{
		MediaURL:       "https://obsolete.example:19901",
		AllowedOrigins: []string{"https://explicit-client.example"},
	}); err != nil {
		t.Fatal(err)
	}
	p := NewPlaybackHTTP(db, nil, nil)
	for _, origin := range []string{
		"https://localhost:5175", "https://tv.example", "https://tv.example:19900",
		"https://192.168.1.5:19900", "https://[::1]:19900",
	} {
		t.Run(origin, func(t *testing.T) {
			r := httptest.NewRequest("GET", origin+"/api/playback/probe", nil)
			r.TLS = &tls.ConnectionState{}
			r.Header.Set("X-Forwarded-Host", "attacker.example:19901")
			r.Header.Set("X-Forwarded-Proto", "http")
			r.Header.Set("X-Forwarded-Port", "19901")
			got, err := p.mediaBase(r)
			if err != nil || got != origin {
				t.Fatalf("media origin = %q, %v; want %q", got, err, origin)
			}
			d, err := playback.GetDelivery(r.Context(), db)
			if err != nil {
				t.Fatal(err)
			}
			if !p.originAllowed(r, origin, d) {
				t.Fatal("same-origin media refused")
			}
			if p.originAllowed(r, "https://attacker.example:19901", d) {
				t.Fatal("spoofed origin accepted")
			}
			if !p.originAllowed(r, "https://explicit-client.example", d) {
				t.Fatal("explicit client refused")
			}
		})
	}
}
