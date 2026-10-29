package instancetls

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/auth"
	"github.com/alreadyonapp/alreadyon/server/internal/storage"
)

// Reopen the persisted database with no exported TLS files, as when replacing
// a container. CA identity and native authentication must survive together.
func TestRedeployPreservesTrustAndNativeSession(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := Open(t.Context(), db.DB, dir)
	if err != nil {
		t.Fatal(err)
	}
	trust := manager.TrustPEM()
	owner, err := auth.CreateAccount(t.Context(), db.DB, "fixture", "synthetic-password")
	if err != nil {
		t.Fatal(err)
	}
	token, _, _, err := auth.CreateNativeSession(t.Context(), db.DB, time.Now(), owner.ID, "AlreadyOn-Android/TV")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(dir, "tls")); err != nil {
		t.Fatal(err)
	}
	db, err = storage.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	manager, err = Open(t.Context(), db.DB, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(trust, manager.TrustPEM()) {
		t.Fatal("container replacement changed CA")
	}
	if _, _, err := auth.LookupSession(t.Context(), db.DB, token, time.Now()); err != nil {
		t.Fatalf("container replacement lost native session: %v", err)
	}
	if _, err := auth.DeleteSessionsExcept(t.Context(), db.DB, 0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := auth.LookupSession(t.Context(), db.DB, token, time.Now()); err == nil {
		t.Fatal("explicit sign out did not revoke native session")
	}
	if !bytes.Equal(trust, manager.TrustPEM()) {
		t.Fatal("sign out changed CA")
	}
}
