package playback

import (
	"testing"
	"time"
)

func TestSessionsScopeExpiryAndRevocation(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)}
	s := NewSessions(clock)
	token, v, err := s.Issue(1, 7)
	if err != nil {
		t.Fatal(err)
	}
	if v.ExpiresAt.Sub(clock.Now()) != 5*time.Minute {
		t.Fatal("wrong TTL")
	}
	if got, err := s.Validate(token, 7); err != nil || got != v {
		t.Fatalf("%+v %v", got, err)
	}
	for _, bad := range []string{"", token + "x", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"} {
		if _, err := s.Validate(bad, 7); err == nil {
			t.Fatal("invalid token accepted")
		}
	}
	if _, err := s.Validate(token, 8); err == nil {
		t.Fatal("wrong channel accepted")
	}
	clock.advance(SessionTTL)
	if _, err := s.Validate(token, 7); err == nil {
		t.Fatal("expired token accepted")
	}
	token, _, _ = s.Issue(1, 7)
	s.Revoke(token)
	if _, err := s.Validate(token, 7); err == nil {
		t.Fatal("revoked token accepted")
	}
}

func TestSessionClaimAndRevokeDuringStart(t *testing.T) {
	s := NewSessions(&testClock{now: time.Now()})
	token, _, _ := s.Issue(1, 7)
	if _, err := s.Claim(token, 2, 7); err == nil {
		t.Fatal("wrong owner claimed session")
	}
	if _, err := s.Claim(token, 1, 8); err == nil {
		t.Fatal("wrong channel claimed session")
	}
	if _, err := s.Claim(token, 1, 7); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(token, 1, 7); err == nil {
		t.Fatal("parallel start accepted")
	}
	s.Revoke(token)
	if err := s.Bind(token, "worker"); err == nil {
		t.Fatal("cancelled start attached a worker")
	}
	token, _, _ = s.Issue(1, 7)
	if _, err := s.Claim(token, 1, 7); err != nil {
		t.Fatal(err)
	}
	if err := s.Bind(token, "worker"); err != nil {
		t.Fatal(err)
	}
	if got := s.Revoke(token); got.StreamKey != "worker" {
		t.Fatal("revocation lost attached worker")
	}
}

func TestAuthenticatedRenewalKeepsLongPlaybackAlive(t *testing.T) {
	clock := &testClock{now: time.Now()}
	sessions := NewSessions(clock)
	token, _, _ := sessions.Issue(1, 7)
	if _, err := sessions.Claim(token, 1, 7); err != nil {
		t.Fatal(err)
	}
	if err := sessions.Bind(token, "worker"); err != nil {
		t.Fatal(err)
	}
	clock.advance(4 * time.Minute)
	if _, err := sessions.Renew(token, 2, 7); err == nil {
		t.Fatal("wrong owner renewed")
	}
	if _, err := sessions.Renew(token, 1, 8); err == nil {
		t.Fatal("wrong scope renewed")
	}
	if _, err := sessions.Renew(token, 1, 7); err != nil {
		t.Fatal(err)
	}
	clock.advance(4 * time.Minute)
	if _, err := sessions.Validate(token, 7); err != nil {
		t.Fatal("active viewer expired")
	}
	clock.advance(time.Minute)
	if _, err := sessions.Renew(token, 1, 7); err == nil {
		t.Fatal("expired token resurrected")
	}
}

func TestPauseStreamingAllowsSessionToResume(t *testing.T) {
	clock := &testClock{now: time.Now()}
	sessions := NewSessions(clock)
	token, _, err := sessions.Issue(1, 7)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sessions.Claim(token, 1, 7); err != nil {
		t.Fatal(err)
	}
	if err = sessions.Bind(token, "worker"); err != nil {
		t.Fatal(err)
	}
	clock.advance(4 * time.Minute)
	if paused := sessions.PauseStreaming(); paused != 1 {
		t.Fatalf("paused %d sessions", paused)
	}
	session, err := sessions.Validate(token, 7)
	if err != nil || session.StreamKey != "" {
		t.Fatal("paused session retained its stream binding:", session.StreamKey, err)
	}
	if _, err = sessions.Claim(token, 1, 7); err != nil {
		t.Fatal("paused session could not resume:", err)
	}
}

// A probe flood by one principal must leave capacity for another, and released
// or expired tokens must stop charging against that principal's quota.
func TestSessionQuotaIsolationAndRecovery(t *testing.T) {
	clock := &testClock{now: time.Now()}
	s := NewSessions(clock)
	var first string
	for i := 0; i < PerPrincipalSessionLimit; i++ {
		token, _, err := s.Issue(-1, 0)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = token
		}
	}
	if _, _, err := s.Issue(-1, 7); err != ErrBusy {
		t.Fatalf("quota: %v", err)
	}
	if _, _, err := s.Issue(1, 7); err != nil {
		t.Fatalf("other principal: %v", err)
	}
	s.Revoke(first)
	if _, _, err := s.Issue(-1, 7); err != nil {
		t.Fatalf("released quota: %v", err)
	}
	clock.advance(SessionTTL)
	if _, _, err := s.Issue(-1, 7); err != nil {
		t.Fatalf("expired quota: %v", err)
	}
}

func TestClaimForEnforcesAccountDeviceLimit(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)}
	s := NewSessions(clock)
	start := func(keyID, userID int64, limit int) (string, error) {
		token, _, err := s.Issue(keyID, 7)
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.ClaimFor(token, keyID, 7, userID, limit)
		return token, err
	}
	// Device 1 of account 10 streams; its own second stream (a channel change
	// or next-program preparation) is still allowed under a one-device limit.
	first, err := start(1, 10, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Bind(first, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := start(1, 10, 1); err != nil {
		t.Fatalf("same device refused: %v", err)
	}
	// A second device of the same account is refused, another account is not.
	if _, err := start(2, 10, 1); err != ErrStreamLimit {
		t.Fatalf("second device = %v, want ErrStreamLimit", err)
	}
	if _, err := start(3, 11, 1); err != nil {
		t.Fatalf("other account refused: %v", err)
	}
	if _, err := start(2, 10, 2); err != nil {
		t.Fatalf("second device under a limit of two refused: %v", err)
	}
	if _, err := start(4, 10, 0); err != nil {
		t.Fatalf("unlimited account refused: %v", err)
	}
	// Stopping or abandoning playback frees the slot.
	s2 := NewSessions(clock)
	s = s2
	first, _ = start(1, 10, 1)
	s.Revoke(first)
	if _, err := start(2, 10, 1); err != nil {
		t.Fatalf("slot not freed by stop: %v", err)
	}
	clock.advance(SessionTTL)
	if _, err := start(5, 10, 1); err != nil {
		t.Fatalf("slot not freed by expiry: %v", err)
	}
}

func TestReleaseForIsIdempotentWithoutReleasingOtherOwnersOrChannels(t *testing.T) {
	clock := &testClock{now: time.Now()}
	sessions := NewSessions(clock)
	token, issued, err := sessions.Issue(1, 107)
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range [][2]int64{{2, 107}, {1, 108}} {
		if _, err = sessions.ReleaseFor(token, scope[0], scope[1]); err == nil {
			t.Fatal("unowned/scoped token released")
		}
		if _, err = sessions.Validate(token, 107); err != nil {
			t.Fatal("unauthorized cleanup changed the token")
		}
	}
	clock.advance(SessionTTL)
	released, err := sessions.ReleaseFor(token, 1, 107)
	if err != nil || released.ID != issued.ID {
		t.Fatalf("owned expired token not cleaned up: %+v %v", released, err)
	}
	for range 2 {
		if released, err = sessions.ReleaseFor(token, 1, 107); err != nil || released.ID != "" {
			t.Fatalf("repeated cleanup failed: %+v %v", released, err)
		}
	}
	if _, err = sessions.ReleaseFor("", 1, 107); err == nil {
		t.Fatal("malformed cleanup accepted")
	}
}
