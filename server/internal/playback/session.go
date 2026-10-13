// Package playback issues authorized playback sessions and manages
// FFmpeg subprocess lifecycles. See docs/PROJECT_BRIEF.md Epic 4.
package playback

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"sync"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/schedule"
)

const SessionTTL = 5 * time.Minute

// PerPrincipalSessionLimit leaves capacity for other viewers if one browser
// session or API key repeatedly probes without releasing its old tokens.
const PerPrincipalSessionLimit = 32

var ErrUnauthorized = errors.New("playback: unauthorized or expired session")

// ErrStreamLimit reports that the account already streams on as many devices
// as its owner-configured limit allows.
var ErrStreamLimit = errors.New("playback: account stream limit reached")

// Session contains no raw token. The token is returned once at issuance and
// only its hash is retained. Scope cannot be changed by refreshing a URL.
type Session struct {
	ID         string    `json:"id"`
	ChannelID  int64     `json:"channelId"`
	KeyID      int64     `json:"-"`
	ExpiresAt  time.Time `json:"expiresAt"`
	ProbeBytes int64     `json:"-"`
	StreamKey  string    `json:"-"`
	MediaBytes int64     `json:"-"`
	Preparing  bool      `json:"-"`
	// UserID is the account that claimed the session; 0 before a claim.
	UserID int64 `json:"-"`
}

type Sessions struct {
	mu    sync.Mutex
	clock schedule.Clock
	items map[[32]byte]Session
}

func NewSessions(clock schedule.Clock) *Sessions {
	return &Sessions{clock: clock, items: map[[32]byte]Session{}}
}
func (s *Sessions) Issue(keyID, channelID int64) (string, Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()
	for k, v := range s.items {
		if !now.Before(v.ExpiresAt) {
			delete(s.items, k)
		}
	}
	if keyID == 0 || channelID < 0 {
		return "", Session{}, ErrUnauthorized
	}
	owned := 0
	for _, v := range s.items {
		if v.KeyID == keyID {
			owned++
		}
	}
	if owned >= PerPrincipalSessionLimit || len(s.items) >= 1024 {
		return "", Session{}, ErrBusy
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", Session{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return "", Session{}, err
	}
	v := Session{ID: base64.RawURLEncoding.EncodeToString(idBytes), KeyID: keyID, ChannelID: channelID, ExpiresAt: now.Add(SessionTTL), ProbeBytes: 32 << 20}
	s.items[sha256.Sum256([]byte(token))] = v
	return token, v, nil
}
func (s *Sessions) Validate(token string, channelID int64) (Session, error) {
	if len(token) != 43 {
		return Session{}, ErrUnauthorized
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	h := sha256.Sum256([]byte(token))
	v, ok := s.items[h]
	if !ok || !s.clock.Now().Before(v.ExpiresAt) {
		delete(s.items, h)
		return Session{}, ErrUnauthorized
	}
	if v.ChannelID != channelID {
		return Session{}, ErrUnauthorized
	}
	return v, nil
}
func (s *Sessions) Revoke(token string) Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := sha256.Sum256([]byte(token))
	v := s.items[h]
	delete(s.items, h)
	return v
}

// Renewal is available only through an authenticated management heartbeat.
// Possession of a media URL alone cannot prolong its authorization.
func (s *Sessions) Renew(token string, keyID, channelID int64) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := sha256.Sum256([]byte(token))
	v, ok := s.items[h]
	if !ok || !s.clock.Now().Before(v.ExpiresAt) || v.KeyID != keyID || v.ChannelID != channelID || v.StreamKey == "" {
		return Session{}, ErrUnauthorized
	}
	v.ExpiresAt = s.clock.Now().Add(SessionTTL)
	s.items[h] = v
	return v, nil
}

// Claim makes an authorization single-use for starting a stream. Revocation
// can race preparation: Bind then fails, allowing the caller to release its lease.
func (s *Sessions) Claim(token string, keyID, channelID int64) (Session, error) {
	return s.ClaimFor(token, keyID, channelID, 0, 0)
}

// ClaimFor is Claim for an account limited to maxStreams simultaneous
// streaming devices (0 = unlimited). A device is one sign-in (keyID): its own
// channel changes and next-program preparation never count against it, while
// another device of the same account is refused with ErrStreamLimit once the
// limit is reached. The check and the claim happen under one lock, so
// concurrent tunes cannot overshoot the limit.
func (s *Sessions) ClaimFor(token string, keyID, channelID, userID int64, maxStreams int) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()
	h := sha256.Sum256([]byte(token))
	v, ok := s.items[h]
	if !ok || !now.Before(v.ExpiresAt) || v.KeyID != keyID || v.ChannelID != channelID || v.Preparing || v.StreamKey != "" {
		return Session{}, ErrUnauthorized
	}
	if userID != 0 && maxStreams > 0 {
		devices := map[int64]struct{}{}
		for k, o := range s.items {
			if k == h || o.UserID != userID || o.KeyID == keyID || !now.Before(o.ExpiresAt) {
				continue
			}
			if o.Preparing || o.StreamKey != "" {
				devices[o.KeyID] = struct{}{}
			}
		}
		if len(devices) >= maxStreams {
			return Session{}, ErrStreamLimit
		}
	}
	v.UserID = userID
	v.Preparing = true
	s.items[h] = v
	return v, nil
}

func (s *Sessions) Bind(token, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := sha256.Sum256([]byte(token))
	v, ok := s.items[h]
	if !ok || !s.clock.Now().Before(v.ExpiresAt) || !v.Preparing || v.StreamKey != "" {
		return ErrUnauthorized
	}
	v.StreamKey = key
	v.Preparing = false
	s.items[h] = v
	return nil
}

func (s *Sessions) SpendProbe(token string, n int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := sha256.Sum256([]byte(token))
	v, ok := s.items[h]
	if !ok || !s.clock.Now().Before(v.ExpiresAt) || n < 0 || v.ProbeBytes < n {
		return false
	}
	v.ProbeBytes -= n
	s.items[h] = v
	return true
}

// AddMediaBytes counts successfully written media payload, independently for every viewer.
func (s *Sessions) AddMediaBytes(token string, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := sha256.Sum256([]byte(token))
	if v, ok := s.items[h]; ok && n > 0 {
		v.MediaBytes += int64(n)
		s.items[h] = v
	}
}
