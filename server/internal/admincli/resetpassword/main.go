// Command reset-password sets a new server password without knowing the old
// one. It only works from a shell on the machine (or in the container) that
// holds the database, which is what makes it safe: there is no HTTP route for
// it. Every browser is signed out, and the running server needs no restart.
package resetpassword

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/alreadyonapp/alreadyon/server/internal/auth"
	"github.com/alreadyonapp/alreadyon/server/internal/config"
	"github.com/alreadyonapp/alreadyon/server/internal/storage"
)

// Run executes the existing local administration command.
func Run() {
	username := flag.String("username", "", "also change the username")
	fromStdin := flag.Bool("password-stdin", false, "read the new password from standard input instead of prompting")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		fail(err)
	}
	// In a container the shell is root but the data belongs to the server's user;
	// files this tool creates must stay readable by the server.
	if err := dropToDataOwner(cfg.DataDir); err != nil {
		fail(fmt.Errorf("could not switch to the data directory's owner: %w", err))
	}
	password, err := readPassword(*fromStdin)
	if err != nil {
		fail(err)
	}
	db, err := storage.Open(cfg.DataDir)
	if err != nil {
		fail(err)
	}
	defer db.Close()
	switch err := auth.ResetPassword(context.Background(), db.DB, *username, password); {
	case errors.Is(err, auth.ErrNoAccount):
		fail(errors.New("the server has no account yet: open the web UI and create one instead"))
	case err != nil:
		fail(err)
	}
	fmt.Println("Password changed. Every browser has been signed out; sign in with the new password.")
}

func readPassword(fromStdin bool) (string, error) {
	if fromStdin {
		line, err := bufio.NewReader(io.LimitReader(os.Stdin, auth.MaxPasswordBytes+3)).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", errors.New("no terminal to prompt on: run it in an interactive shell (docker exec -it), or use --password-stdin")
	}
	fmt.Fprint(os.Stderr, "New password: ")
	first, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	fmt.Fprint(os.Stderr, "Repeat new password: ")
	second, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	if string(first) != string(second) {
		return "", errors.New("the passwords don't match")
	}
	return string(first), nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "reset-password:", strings.TrimPrefix(err.Error(), "auth: "))
	os.Exit(1)
}
