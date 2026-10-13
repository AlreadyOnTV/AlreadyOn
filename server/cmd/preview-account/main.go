// Command preview-account provisions the checkout-only browser preview account.
// It is not included in runtime images and accepts no database path override.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/alreadyonapp/alreadyon/server/internal/auth"
	"github.com/alreadyonapp/alreadyon/server/internal/playback"
	"github.com/alreadyonapp/alreadyon/server/internal/storage"
)

func main() {
	reset := flag.Bool("reset", false, "restore the account using private local credentials")
	credentials := flag.String("credentials", "../.private/preview-login.json", "private local account JSON file")
	flag.Parse()
	if err := provision(*reset, *credentials); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func provision(reset bool, credentialsPath string) error {
	credentials, err := readCredentials(credentialsPath)
	if errors.Is(err, os.ErrNotExist) && !reset {
		fmt.Println("No local provisioning credentials; use first-run setup or your existing account.")
		return nil
	}
	if err != nil {
		return err
	}
	// Require the server directory in a source checkout. Production /config is
	// never a target, including when this utility is invoked from the wrong cwd.
	module, err := os.ReadFile("go.mod")
	if err != nil || !strings.Contains(string(module), "module github.com/alreadyonapp/alreadyon/server\n") {
		return errors.New("run from the checkout's server directory")
	}
	if _, err := os.Stat("../scripts/preview.sh"); err != nil {
		return err
	}
	if info, err := os.Lstat("data"); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("preview data must not be a symlink")
	}
	db, err := storage.Open("data")
	if err != nil {
		return err
	}
	defer db.Close()
	ctx := context.Background()
	exists, err := auth.AccountExists(ctx, db.DB)
	if err != nil {
		return err
	}
	// Every preview deployment starts with fresh browser authentication while
	// preserving the account and every other row in this database.
	if _, err := auth.DeleteSessionsExcept(ctx, db.DB, 0); err != nil {
		return err
	}
	if !exists {
		if _, err := auth.CreateAccount(ctx, db.DB, credentials.Username, credentials.Password); err != nil {
			return err
		}
	} else {
		_, matches, err := auth.Authenticate(ctx, db.DB, credentials.Username, credentials.Password)
		if err != nil {
			return err
		}
		if !matches && !reset {
			fmt.Println("Existing preview account preserved. Explicit local password restoration requires make preview-login.")
			return nil
		}
		if !matches {
			if err := auth.ResetPassword(ctx, db.DB, credentials.Username, credentials.Password); err != nil {
				return err
			}
		}
	}
	// Mirror the browser port selected by preview.sh.
	port := os.Getenv("PREVIEW_PORT")
	if port == "" {
		port = "5173"
	}
	// Upgrade only this preview's known localhost media/origin settings. Remote
	// custom routes are never rewritten by development account provisioning.
	delivery, err := playback.GetDelivery(ctx, db.DB)
	if err != nil {
		return err
	}
	origin := "https://localhost:" + port
	found := false
	for i, value := range delivery.AllowedOrigins {
		if value == "http://localhost:"+port {
			delivery.AllowedOrigins[i] = origin
		}
		found = found || delivery.AllowedOrigins[i] == origin
	}
	if !found {
		delivery.AllowedOrigins = append(delivery.AllowedOrigins, origin)
	}
	if err := playback.SaveDelivery(ctx, db.DB, delivery); err != nil {
		return err
	}
	fmt.Printf("Local preview account configured (https://localhost:%s); credentials remain in the private file.\n", port)
	return nil
}

// Credentials are workstation configuration, never a shared account in source.
type previewCredentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func readCredentials(path string) (previewCredentials, error) {
	var credentials previewCredentials
	info, err := os.Stat(path)
	if err != nil {
		return credentials, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return credentials, errors.New("preview credentials must be a private regular file (chmod 600)")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return credentials, err
	}
	if err := json.Unmarshal(data, &credentials); err != nil {
		return credentials, errors.New("invalid preview credentials JSON")
	}
	if strings.TrimSpace(credentials.Username) == "" || credentials.Password == "" {
		return credentials, errors.New("preview credentials require username and password")
	}
	return credentials, nil
}
