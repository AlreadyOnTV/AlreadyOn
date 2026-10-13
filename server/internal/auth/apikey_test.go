package auth

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/alreadyonapp/alreadyon/server/internal/storage"
)

func openAPIKeyTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	mustOwner(t, db.DB)
	return db.DB
}

// ownerID returns the id of the database's first owner account.
func ownerID(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	u, err := FirstOwner(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}

func TestGenerateAPIKey_ReturnsAUniqueRawKeyEachTime(t *testing.T) {
	db := openAPIKeyTestDB(t)

	rawA, keyA, err := GenerateAPIKey(context.Background(), db, ownerID(t, db), "First key")
	if err != nil {
		t.Fatalf("GenerateAPIKey (1st): %v", err)
	}
	rawB, keyB, err := GenerateAPIKey(context.Background(), db, ownerID(t, db), "Second key")
	if err != nil {
		t.Fatalf("GenerateAPIKey (2nd): %v", err)
	}

	if rawA == rawB {
		t.Fatalf("rawA == rawB == %q, want distinct raw keys", rawA)
	}
	if keyA.ID == keyB.ID {
		t.Errorf("keyA.ID == keyB.ID == %d, want distinct ids", keyA.ID)
	}
	if !strings.HasPrefix(rawA, "ao_") || !strings.HasPrefix(rawB, "ao_") {
		t.Errorf("raw keys = %q, %q, want both prefixed with ao_", rawA, rawB)
	}
	if keyA.Prefix == "" || !strings.HasPrefix(rawA, keyA.Prefix) {
		t.Errorf("keyA.Prefix = %q, want a non-empty prefix of rawA (%q)", keyA.Prefix, rawA)
	}
}

func TestGenerateAPIKey_EmptyName_ReturnsError(t *testing.T) {
	db := openAPIKeyTestDB(t)

	if _, _, err := GenerateAPIKey(context.Background(), db, ownerID(t, db), "   "); !errors.Is(err, ErrInvalidAPIKeyName) {
		t.Errorf("err = %v, want ErrInvalidAPIKeyName", err)
	}
}

func TestListAPIKeys_NeverIncludesTheRawOrHashedValue(t *testing.T) {
	db := openAPIKeyTestDB(t)
	rawKey, _, err := GenerateAPIKey(context.Background(), db, ownerID(t, db), "Living room TV")
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}

	keys, err := ListAPIKeys(context.Background(), db)
	if err != nil {
		t.Fatalf("ListAPIKeys: %v", err)
	}
	if len(keys) != 1 || keys[0].Name != "Living room TV" {
		t.Fatalf("keys = %+v, want one key named %q", keys, "Living room TV")
	}
	if keys[0].Prefix == rawKey {
		t.Errorf("keys[0].Prefix equals the full raw key — it must only ever be a short prefix")
	}
}

func TestListAPIKeys_NewestFirst(t *testing.T) {
	db := openAPIKeyTestDB(t)
	if _, _, err := GenerateAPIKey(context.Background(), db, ownerID(t, db), "Older"); err != nil {
		t.Fatalf("GenerateAPIKey (older): %v", err)
	}
	if _, _, err := GenerateAPIKey(context.Background(), db, ownerID(t, db), "Newer"); err != nil {
		t.Fatalf("GenerateAPIKey (newer): %v", err)
	}

	keys, err := ListAPIKeys(context.Background(), db)
	if err != nil {
		t.Fatalf("ListAPIKeys: %v", err)
	}
	if len(keys) != 2 || keys[0].Name != "Newer" || keys[1].Name != "Older" {
		t.Fatalf("keys = %+v, want [Newer, Older]", keys)
	}
}

func TestRevokeAPIKey_RemovesIt(t *testing.T) {
	db := openAPIKeyTestDB(t)
	_, key, err := GenerateAPIKey(context.Background(), db, ownerID(t, db), "Temporary")
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}

	if err := RevokeAPIKey(context.Background(), db, key.ID); err != nil {
		t.Fatalf("RevokeAPIKey: %v", err)
	}
	keys, err := ListAPIKeys(context.Background(), db)
	if err != nil {
		t.Fatalf("ListAPIKeys: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("keys = %+v, want empty after revoking the only key", keys)
	}
}

func TestRevokeAPIKey_UnknownID_ReturnsNotFound(t *testing.T) {
	db := openAPIKeyTestDB(t)
	if err := RevokeAPIKey(context.Background(), db, 999); !errors.Is(err, ErrAPIKeyNotFound) {
		t.Errorf("err = %v, want ErrAPIKeyNotFound", err)
	}
}

func TestPersistentTestingKeyRequiresProvisioningAndSupportsRevocation(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := storage.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const raw = "4826"
	if _, err := ProvisionTestAPIKey(ctx, db.DB, "Local testing", raw); !errors.Is(err, ErrNoAccount) {
		t.Fatalf("provisioning without an owner: err = %v, want ErrNoAccount", err)
	}
	mustOwner(t, db.DB)
	if _, _, err := VerifyAPIKey(ctx, db.DB, raw); err == nil {
		t.Fatal("unprovisioned test key accepted")
	}
	key, err := ProvisionTestAPIKey(ctx, db.DB, "Local testing", raw)
	if err != nil {
		t.Fatal(err)
	}
	same, err := ProvisionTestAPIKey(ctx, db.DB, "Local testing", raw)
	if err != nil || same.ID != key.ID {
		t.Fatalf("provisioning was not idempotent: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := storage.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if id, u, err := VerifyAPIKey(ctx, reopened.DB, raw); err != nil || id != key.ID || !u.IsOwner() {
		t.Fatalf("persistent key rejected: %v", err)
	}
	if _, _, err := VerifyAPIKey(ctx, reopened.DB, "4827"); err == nil {
		t.Fatal("wrong short key accepted")
	}
	keys, err := ListAPIKeys(ctx, reopened.DB)
	if err != nil || len(keys) != 1 || strings.Contains(keys[0].Prefix, raw) {
		t.Fatalf("unsafe display: %+v %v", keys, err)
	}
	if err := RevokeAPIKey(ctx, reopened.DB, key.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := VerifyAPIKey(ctx, reopened.DB, raw); err == nil {
		t.Fatal("revoked test key accepted")
	}
	for _, bad := range []string{"", "123", "1234\n", strings.Repeat("x", 129)} {
		if _, err := ProvisionTestAPIKey(ctx, reopened.DB, "test", bad); err == nil {
			t.Fatal("invalid test key accepted")
		}
	}
}
