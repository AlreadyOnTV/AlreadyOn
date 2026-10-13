package instancetls

import (
	"bytes"
	"crypto/tls"
	"os"
	"path/filepath"
	"testing"

	"github.com/alreadyonapp/alreadyon/server/internal/storage"
)

// Manager tests use disposable directories, including every exported secret.
func TestManagerReloadAndExportPermissions(t *testing.T) {
	dataDir := t.TempDir()
	db, err := storage.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	m, err := Open(t.Context(), db.DB, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	config := m.Config()
	before, err := config.GetCertificate(&tls.ClientHelloInfo{})
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := m.Status().Fingerprint
	if err := m.SetNames(t.Context(), []string{"tv.example"}); err != nil {
		t.Fatal(err)
	}
	after, err := config.GetCertificate(&tls.ClientHelloInfo{})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(before.Certificate[0], after.Certificate[0]) {
		t.Fatal("leaf did not reload")
	}
	if m.Status().Fingerprint != fingerprint {
		t.Fatal("CA changed")
	}
	for _, host := range []string{"tv.example", "localhost", "127.0.0.1", "::1"} {
		if err := after.Leaf.VerifyHostname(host); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"ca.pem", "server.pem", "server.key", "server-bundle.pem"} {
		info, err := os.Stat(filepath.Join(dataDir, "tls", name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatal("TLS export is not owner-only")
		}
	}
	if _, err := os.Stat(filepath.Join(dataDir, "tls", "root.key")); !os.IsNotExist(err) {
		t.Fatal("CA private key exported")
	}
	again, err := Open(t.Context(), db.DB, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if again.Status().Fingerprint != fingerprint {
		t.Fatal("restart changed trust")
	}
}
