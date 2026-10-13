// Command access-key provisions an initial management credential locally.
// There is deliberately no unauthenticated HTTP bootstrap endpoint.
package accesskey

import (
	"context"
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
	testKey := flag.Bool("test-key-stdin", false, "provision a persistent testing key read from standard input")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: access-key [--test-key-stdin] <name>")
		os.Exit(2)
	}
	cfg, err := config.Load()
	if err != nil {
		fail(err)
	}
	db, err := storage.Open(cfg.DataDir)
	if err != nil {
		fail(err)
	}
	defer db.Close()
	if *testKey {
		// Testing credentials may have very little entropy even when long.
		fmt.Fprintln(os.Stderr, "Warning: testing keys can be guessed over the LAN. Use generated keys for deployed servers; reserve testing keys for isolated local development.")
		input, err := readTestingKey()
		if err != nil {
			fail(err)
		}
		if len(input) > 130 {
			fail(fmt.Errorf("test key input is too long"))
		}
		_, err = auth.ProvisionTestAPIKey(context.Background(), db.DB, flag.Arg(0), strings.TrimSuffix(strings.TrimSuffix(string(input), "\n"), "\r"))
		if err != nil {
			fail(err)
		}
		fmt.Println("Persistent testing access key saved.")
		return
	}
	owner, err := auth.FirstOwner(context.Background(), db.DB)
	if err != nil {
		fail(err)
	}
	raw, _, err := auth.GenerateAPIKey(context.Background(), db.DB, owner.ID, flag.Arg(0))
	if err != nil {
		fail(err)
	}
	fmt.Println(raw)
}
func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }

// readTestingKey suppresses interactive terminal echo without changing piped
// provisioning. ReadPassword restores terminal state before returning errors.
func readTestingKey() ([]byte, error) {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		fmt.Fprint(os.Stderr, "Testing access key: ")
		input, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		return input, err
	}
	return io.ReadAll(io.LimitReader(os.Stdin, 131))
}
