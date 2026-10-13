// Command clear-browser-sessions revokes browser cookies without changing the
// account, API keys, settings or catalog. Local deployment scripts use it;
// production operators normally manage sessions through the web UI instead.
package clearsessions

import (
	"context"
	"fmt"
	"os"

	"github.com/alreadyonapp/alreadyon/server/internal/auth"
	"github.com/alreadyonapp/alreadyon/server/internal/config"
	"github.com/alreadyonapp/alreadyon/server/internal/storage"
)

// Run executes the existing local administration command.
func Run() {
	cfg, err := config.Load()
	if err != nil {
		fail(err)
	}
	db, err := storage.Open(cfg.DataDir)
	if err != nil {
		fail(err)
	}
	defer db.Close()
	n, err := auth.DeleteSessionsExcept(context.Background(), db.DB, 0)
	if err != nil {
		fail(err)
	}
	fmt.Printf("Signed out %d browser session(s).\n", n)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "clear-browser-sessions:", err)
	os.Exit(1)
}
