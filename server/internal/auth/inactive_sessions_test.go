package auth

import (
	"context"
	"testing"
	"time"
)

func TestInactiveSessionsInvalidateBeforeActivityAndPrune(t *testing.T) {
	ctx, db := context.Background(), openAuthDB(t)
	user := mustOwner(t, db)
	now := time.Now().Truncate(time.Second)
	policy, err := GetInactiveSessionPolicy(ctx, db)
	if err != nil || !policy.Enabled || policy.Days != 14 {
		t.Fatal(policy, err)
	}
	for _, native := range []bool{false, true} {
		create := CreateSession
		if native {
			create = CreateNativeSession
		}
		token, id, _, err := create(ctx, db, now, user, "synthetic")
		if err != nil {
			t.Fatal(err)
		}
		late := now.Add(14*24*time.Hour + time.Second)
		if PrincipalActive(ctx, db, -id, late) {
			t.Fatal("inactive media principal revived session")
		}
		if _, _, err = LookupSession(ctx, db, token, late); err != ErrNoSession {
			t.Fatal("inactive login revived", err)
		}
		var active int64
		if err = db.QueryRow(`SELECT last_active_at_unix FROM browser_sessions WHERE id=?`, id).Scan(&active); err != nil || active != now.Unix() {
			t.Fatal("rejected request updated activity", active, err)
		}
		if err = PruneSessions(ctx, db, late); err != nil {
			t.Fatal(err)
		}
		var count int
		db.QueryRow(`SELECT COUNT(*) FROM browser_sessions WHERE id=?`, id).Scan(&count)
		if count != 0 {
			t.Fatal("inactive row was not removed")
		}
	}
	token, id, _, err := CreateNativeSession(ctx, db, now, user, "boundary")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = LookupSession(ctx, db, token, now.Add(14*24*time.Hour)); err != nil {
		t.Fatal("exact boundary is still active", err)
	}
	if !PrincipalActive(ctx, db, -id, now.Add(15*24*time.Hour)) {
		t.Fatal("recent media activity was ignored")
	}
}

func TestInactivePolicyDisabledLegacyAndValidation(t *testing.T) {
	ctx, db := context.Background(), openAuthDB(t)
	now := time.Now().Truncate(time.Second)
	uid := mustOwner(t, db)
	token, id, _, err := CreateNativeSession(ctx, db, now, uid, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	db.Exec(`UPDATE browser_sessions SET last_active_at_unix=NULL WHERE id=?`, id)
	if err = SetInactiveSessionPolicy(ctx, db, InactiveSessionPolicy{Enabled: false, Days: 14}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = LookupSession(ctx, db, token, now.Add(20*24*time.Hour)); err != nil {
		t.Fatal("disabled policy revoked", err)
	}
	db.Exec(`UPDATE browser_sessions SET last_active_at_unix=NULL WHERE id=?`, id)
	SetInactiveSessionPolicy(ctx, db, InactiveSessionPolicy{Enabled: true, Days: 7})
	if PrincipalActive(ctx, db, -id, now.Add(8*24*time.Hour)) {
		t.Fatal("legacy activity must fall back to creation time")
	}
	for _, days := range []int{0, -1, 3651} {
		if err = SetInactiveSessionPolicy(ctx, db, InactiveSessionPolicy{Enabled: true, Days: days}); err == nil {
			t.Fatal("invalid days accepted", days)
		}
	}
}
