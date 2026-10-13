// Exercise password reset against isolated account data, including short passwords.
package resetpassword

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/auth"
	"github.com/alreadyonapp/alreadyon/server/internal/storage"
)

func TestResetPasswordCommand(t *testing.T) {
	work := t.TempDir()
	bin := filepath.Join(work, "alreadyon-reset-password")
	if out, err := exec.Command("go", "build", "-o", bin, "../../../cmd/server").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	run := func(stdin string, args ...string) (string, error) {
		cmd := exec.Command(bin, append([]string{"--password-stdin"}, args...)...)
		cmd.Dir = work // the data directory is "data" relative to the working directory
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	if out, err := run("longenough2\n"); err == nil || !strings.Contains(out, "no account") {
		t.Fatalf("with no account: err=%v out=%q, want a pointer to the web UI", err, out)
	}
	db, err := storage.Open(filepath.Join(work, "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	u, err := auth.CreateAccount(ctx, db.DB, "mike", "longenough1")
	if err != nil {
		t.Fatal(err)
	}
	_, id, _, _ := auth.CreateSession(ctx, db.DB, time.Now(), u.ID, "")

	if out, err := run("123\n"); err == nil {
		t.Fatalf("weak password accepted: %q", out)
	}
	if out, err := run("longenough2\n"); err != nil || !strings.Contains(out, "Password changed") {
		t.Fatalf("reset: %v %q", err, out)
	}
	if _, ok, _ := auth.Authenticate(ctx, db.DB, "mike", "longenough2"); !ok {
		t.Error("new password rejected")
	}
	if auth.PrincipalActive(ctx, db.DB, -id, time.Now()) {
		t.Error("the reset must sign every browser out")
	}
	if _, err := os.Stat(filepath.Join(work, "data")); err != nil {
		t.Error(err)
	}
}
