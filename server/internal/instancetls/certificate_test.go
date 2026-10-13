package instancetls

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/storage"
)

// These isolated databases never use an agent preview or production identity.
func TestIdentityPersistenceAndIsolation(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now()
	first, err := Ensure(ctx, db.DB, []string{"localhost", "127.0.0.1", "::1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Ensure(ctx, db.DB, []string{"::1", "localhost", "127.0.0.1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if first.Fingerprint() != again.Fingerprint() || !bytes.Equal(first.certificate.Certificate[0], again.certificate.Certificate[0]) {
		t.Fatal("restart/order changed identity")
	}
	otherDB, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer otherDB.Close()
	other, err := Ensure(ctx, otherDB.DB, []string{"localhost"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if other.Fingerprint() == first.Fingerprint() {
		t.Fatal("fresh database reused CA")
	}
	copy := first.TrustPEM()
	copy[0] = 0
	if bytes.Equal(copy, first.TrustPEM()) {
		t.Fatal("public export mutated stored certificate")
	}
}

func TestBothListenersShareVerifiedIdentity(t *testing.T) {
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	m, err := Ensure(context.Background(), db.DB, []string{"localhost", "127.0.0.1"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(m.TrustPEM()) {
		t.Fatal("no trust anchor")
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}, Timeout: time.Second}
	defer client.CloseIdleConnections()
	for range 2 {
		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
		srv.TLS = m.Config()
		srv.StartTLS()
		resp, err := client.Get(srv.URL)
		if err != nil {
			srv.Close()
			t.Fatal(err)
		}
		resp.Body.Close()
		if !bytes.Equal(resp.TLS.PeerCertificates[0].Raw, m.certificate.Certificate[0]) {
			t.Fatal("listener identity differs")
		}
		// No trust bypass: a different DNS name must fail verification.
		if err := resp.TLS.PeerCertificates[0].VerifyHostname("other.example"); err == nil {
			t.Fatal("wrong name accepted")
		}
		srv.Close()
	}
}

func TestRenewalKeepsTrustAndCorruptionFailsClosed(t *testing.T) {
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Now()
	first, err := Ensure(ctx, db.DB, []string{"localhost"}, now)
	if err != nil {
		t.Fatal(err)
	}
	renewed, err := Ensure(ctx, db.DB, []string{"localhost", "tv.example"}, now.AddDate(1, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if first.Fingerprint() != renewed.Fingerprint() {
		t.Fatal("renewal changed trust")
	}
	if err := renewed.certificate.Leaf.VerifyHostname("tv.example"); err != nil {
		t.Fatal(err)
	}
	if _, err := Ensure(ctx, db.DB, []string{"localhost"}, now.AddDate(11, 0, 0)); err == nil {
		t.Fatal("expired CA silently replaced")
	}
	if _, err := db.Exec(`UPDATE instance_tls SET server_key_pem = 'corrupt' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := Ensure(ctx, db.DB, []string{"localhost"}, now); err == nil {
		t.Fatal("corrupt pair silently replaced")
	}
}

func TestInvalidNamesLeaveDatabaseUntouched(t *testing.T) {
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, names := range [][]string{nil, {"https://tv.example"}, {"tv.example:19900"}, {"*.example"}, {"bad name"}, {""}} {
		if _, err := Ensure(context.Background(), db.DB, names, time.Now()); err == nil {
			t.Fatalf("accepted %q", names)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM instance_tls`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("invalid names created identity")
	}
}
