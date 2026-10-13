package instancetls

import (
	"encoding/json"
	"github.com/alreadyonapp/alreadyon/server/internal/storage"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPublicHostnameAndChallengeBoundary(t *testing.T) {
	for _, input := range []string{"https://user:pass@tv.example.org", "http://tv.example.org", "https://tv.example.org/path", "https://tv.example.org/?key=x", "127.0.0.1", "*.example.org", "localhost", "https://tv.example.org:99999"} {
		if _, err := ParsePublicHostname(input); err == nil {
			t.Fatalf("accepted %q", input)
		}
	}
	host, err := ParsePublicHostname("https://TV.Example.org:19900/")
	if err != nil || host != "tv.example.org" {
		t.Fatal(host, err)
	}
	m := &Manager{challengeHost: host, challenges: map[string]string{"/.well-known/acme-challenge/live": "token.thumbprint"}}
	for _, tc := range []struct {
		method, path, host string
		status             int
	}{
		{"GET", "/.well-known/acme-challenge/live", host, 200},
		{"GET", "/.well-known/acme-challenge/old", host, 404},
		{"GET", "/.well-known/acme-challenge/live", "attacker.example.org", 404},
		{"GET", "/api/auth/login", host, 404},
		{"POST", "/.well-known/acme-challenge/live", host, 405},
	} {
		r := httptest.NewRequest(tc.method, "http://"+tc.host+tc.path, nil)
		w := httptest.NewRecorder()
		m.ChallengeHandler().ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s %s: %d", tc.method, tc.path, w.Code)
		}
		if tc.status != 200 && strings.Contains(w.Body.String(), "thumbprint") {
			t.Fatal("challenge leaked")
		}
	}
}

// Corrupt private account material fails before contacting a CA, exercising
// real asynchronous failure/cooldown/persistence without external DNS writes.
func TestACMEFailureRetainsIdentityAndCooldown(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	m, err := Open(t.Context(), db.DB, dir)
	if err != nil {
		t.Fatal(err)
	}
	original := m.Status().Fingerprint
	if err = m.RequestACME(t.Context(), "tv.example.org", "owner@example.org", true, false); err == nil {
		t.Fatal("production without staging accepted")
	}
	if err = m.RequestACME(t.Context(), "tv.example.org", "owner@example.org", false, true); err == nil {
		t.Fatal("terms not required")
	}
	if _, err = db.Exec(`UPDATE acme_tls SET staging_key=? WHERE id=1`, []byte("private-corrupt-account")); err != nil {
		t.Fatal(err)
	}
	if err = m.RequestACME(t.Context(), "tv.example.org", "owner@example.org", true, true); err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(3 * time.Second)
	for m.Status().ACME.Running && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	s := m.Status()
	if s.ACME.Running || s.Fingerprint != original || s.Mode != "self-signed" || s.ACME.NextAttempt < time.Now().Add(59*time.Minute).Unix() || !strings.Contains(s.ACME.Message, "retained") {
		t.Fatalf("invalid failure state: %+v", s)
	}
	if err = m.RequestACME(t.Context(), "tv.example.org", "owner@example.org", true, true); err == nil {
		t.Fatal("cooldown ignored")
	}
	restored, err := Open(t.Context(), db.DB, dir)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Status().Fingerprint != original || restored.Status().ACME.NextAttempt != s.ACME.NextAttempt {
		t.Fatal("restart lost identity/cooldown")
	}
	encoded, _ := json.Marshal(s)
	if strings.Contains(string(encoded), "private-corrupt-account") {
		t.Fatal("status leaked account key")
	}
	cert, key, _ := uploadFixture(t, time.Now())
	if _, err = publicACMEMaterial(cert, key, "tv.example"); err == nil {
		t.Fatal("private upload CA accepted as public trust")
	}
}

// Renewal uses the active certificate's hostname, even after staging another
// hostname. Failure/backoff must never change that active TLS identity.
func TestACMERenewalRetainsActiveHostname(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	m, err := Open(t.Context(), db.DB, dir)
	if err != nil {
		t.Fatal(err)
	}
	m.mode = "acme"
	m.material.certificate.Leaf.DNSNames = []string{"active.example.org"}
	m.material.certificate.Leaf.NotAfter = time.Now().Add(24 * time.Hour)
	if _, err = db.Exec(`UPDATE acme_tls SET agreement_accepted=1,enabled=1,hostname='pending.example.org',email='owner@example.org',staging_hostname='pending.example.org',account_key=? WHERE id=1`, []byte("corrupt-account")); err != nil {
		t.Fatal(err)
	}
	original := m.material.certificate.Certificate[0]
	m.RenewACME(t.Context())
	until := time.Now().Add(3 * time.Second)
	for m.Status().ACME.Running && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	s := m.Status()
	if s.ACME.Hostname != "active.example.org" || s.Mode != "acme" || s.ACME.LastAttempt == 0 || s.ACME.NextAttempt == 0 {
		t.Fatalf("renewal state %+v", s)
	}
	if string(original) != string(m.material.certificate.Certificate[0]) {
		t.Fatal("failed renewal replaced active certificate")
	}
}

// Consent is independent from the current certificate, including after a
// failed order; recovery must clear the requested ACME mode as well.
func TestACMEAgreementRevocationAndRecovery(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	m, err := Open(t.Context(), db.DB, dir)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := m.Status().Fingerprint
	if _, err = db.Exec(`UPDATE acme_tls SET agreement_accepted=1,enabled=1,configured=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err = m.SaveACME(t.Context(), "", "", false); err != nil {
		t.Fatal(err)
	}
	s := m.Status()
	if s.ACME.AgreementAccepted || s.ACME.Enabled || !s.ACME.Configured || s.Fingerprint != fingerprint {
		t.Fatal("revocation altered identity or left renewal enabled")
	}
	if err = m.UseSelfSigned(t.Context()); err != nil {
		t.Fatal(err)
	}
	if m.Status().ACME.Configured {
		t.Fatal("recovery retained ACME draft mode")
	}
}
