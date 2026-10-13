package instancetls

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/storage"
)

// Upload fixtures live in isolated databases and never touch preview trust.
func uploadFixture(t *testing.T, now time.Time) ([]byte, []byte, []byte) {
	t.Helper()
	ca, caKey, err := createRoot(now)
	if err != nil {
		t.Fatal(err)
	}
	root, err := tls.X509KeyPair(ca, caKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, key, err := createLeaf(now, root.Leaf, root.PrivateKey, []string{"tv.example"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return cert, key, ca
}

func TestUploadValidationPersistenceAndRestore(t *testing.T) {
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
	cert, key, ca := uploadFixture(t, time.Now())
	_, wrongKey, _ := uploadFixture(t, time.Now())
	expired, expiredKey, expiredCA := uploadFixture(t, time.Now().AddDate(-11, 0, 0))
	for _, input := range []struct{ cert, key, ca []byte }{
		{cert, wrongKey, ca}, {expired, expiredKey, expiredCA}, {cert, key, nil},
		{append(append([]byte(nil), cert...), key...), key, ca}, {cert, key, cert},
		{cert, append(append([]byte(nil), key...), wrongKey...), ca},
	} {
		if err := m.Upload(t.Context(), input.cert, input.key, input.ca); err == nil {
			t.Fatal("invalid upload accepted")
		}
		if m.Status().Fingerprint != original || m.Status().Mode != "self-signed" {
			t.Fatal("invalid upload changed identity")
		}
	}
	config := m.Config()
	var listeners []*httptest.Server
	for range 2 {
		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
		srv.TLS = m.Config()
		srv.StartTLS()
		t.Cleanup(srv.Close)
		listeners = append(listeners, srv)
	}
	if err := m.Upload(t.Context(), cert, key, ca); err != nil {
		t.Fatal(err)
	}
	if m.Status().Mode != "user-provided" || m.Status().Fingerprint == original {
		t.Fatal("upload did not activate")
	}
	active, err := config.GetCertificate(&tls.ClientHelloInfo{})
	if err != nil || active.Leaf.VerifyHostname("tv.example") != nil {
		t.Fatal("live handshake certificate did not change")
	}
	if err := active.Leaf.VerifyHostname("localhost"); err == nil {
		t.Fatal("test needs domain-only certificate")
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(ca)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "tv.example", MinVersion: tls.VersionTLS12}}, Timeout: time.Second}
	defer client.CloseIdleConnections()
	for _, listener := range listeners {
		response, err := client.Get(listener.URL)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if !bytes.Equal(response.TLS.PeerCertificates[0].Raw, active.Certificate[0]) {
			t.Fatal("listeners did not use uploaded identity")
		}
	}
	if bytes.Contains(m.TrustPEM(), []byte("PRIVATE KEY")) {
		t.Fatal("private key exposed")
	}
	if err := m.SetNames(t.Context(), []string{"other.example"}); err == nil {
		t.Fatal("generated names overwrote uploaded certificate")
	}
	if err := m.Renew(t.Context()); err != nil {
		t.Fatal(err)
	}
	again, err := Open(t.Context(), db.DB, dir)
	if err != nil || again.Status().Mode != "user-provided" || again.Status().Fingerprint != m.Status().Fingerprint {
		t.Fatal("upload did not survive restart")
	}
	if err := again.UseSelfSigned(t.Context()); err != nil {
		t.Fatal(err)
	}
	if again.Status().Mode != "self-signed" || again.Status().Fingerprint != original {
		t.Fatal("restore changed original CA")
	}
	restored, err := Open(t.Context(), db.DB, dir)
	if err != nil || restored.Status().Fingerprint != original || restored.Status().Mode != "self-signed" {
		t.Fatal("restore did not persist")
	}
}

func TestTenYearLeafUpgradeKeepsCA(t *testing.T) {
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now()
	m, err := Ensure(t.Context(), db.DB, []string{"localhost"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !m.certificate.Leaf.NotAfter.Equal(m.root.NotAfter) {
		t.Fatal("initial leaf does not have ten-year CA lifetime")
	}
	var ca, caKey []byte
	if err := db.DB.QueryRow(`SELECT root_cert_pem, root_key_pem FROM instance_tls WHERE id=1`).Scan(&ca, &caKey); err != nil {
		t.Fatal(err)
	}
	pair, err := tls.X509KeyPair(ca, caKey)
	if err != nil {
		t.Fatal(err)
	}
	shortRoot := *pair.Leaf
	shortRoot.NotAfter = now.AddDate(1, 0, 0)
	cert, key, err := createLeaf(now, &shortRoot, pair.PrivateKey, []string{"localhost"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB.Exec(`UPDATE instance_tls SET server_cert_pem=?, server_key_pem=? WHERE id=1`, cert, key); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Ensure(t.Context(), db.DB, []string{"localhost"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if upgraded.Fingerprint() != m.Fingerprint() || !upgraded.certificate.Leaf.NotAfter.Equal(m.root.NotAfter) {
		t.Fatal("upgrade changed trust or retained one-year expiry")
	}
	if _, err := HealthName(&x509.Certificate{DNSNames: []string{"*.example"}}); err == nil {
		t.Fatal("wildcard-only health identity accepted")
	}
}
