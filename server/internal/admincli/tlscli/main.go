// Command tls prepares/export-verifies the local instance trust identity.
// Private keys are never printed; trust output is public PEM only.
package tlscli

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/alreadyonapp/alreadyon/server/internal/config"
	"github.com/alreadyonapp/alreadyon/server/internal/instancetls"
	"github.com/alreadyonapp/alreadyon/server/internal/storage"
)

// Run executes the existing local administration command.
func Run() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 || len(os.Args) > 3 {
		return fmt.Errorf("usage: alreadyon-tls prepare|trust|fingerprint|hostname|self-signed|health [preview-port]")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if os.Args[1] == "health" {
		return health(cfg)
	}
	if len(os.Args) != 2 {
		return fmt.Errorf("only health accepts a preview port")
	}
	if os.Args[1] == "hostname" {
		name, err := healthName(cfg)
		if err == nil {
			fmt.Println(name)
		}
		return err
	}
	if os.Args[1] != "prepare" && os.Args[1] != "trust" && os.Args[1] != "fingerprint" && os.Args[1] != "self-signed" {
		return fmt.Errorf("unknown TLS operation")
	}
	db, err := storage.Open(cfg.DataDir)
	if err != nil {
		return err
	}
	defer db.Close()
	if os.Args[1] == "self-signed" {
		return instancetls.RestoreSelfSigned(context.Background(), db.DB, cfg.DataDir)
	}
	m, err := instancetls.Open(context.Background(), db.DB, cfg.DataDir)
	if err != nil {
		return err
	}
	switch os.Args[1] {
	case "trust":
		_, err = os.Stdout.Write(m.TrustPEM())
	case "fingerprint":
		fmt.Println(m.Status().Fingerprint)
	}
	return err
}

func healthName(cfg config.Config) (string, error) {
	certData, err := os.ReadFile(filepath.Join(cfg.DataDir, "tls", "server.pem"))
	if err != nil {
		return "", err
	}
	block, _ := pem.Decode(certData)
	if block == nil {
		return "", fmt.Errorf("invalid server certificate")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", err
	}
	return instancetls.HealthName(leaf)
}

func health(cfg config.Config) error {
	// Trust only the instance's persisted CA, with normal hostname verification.
	data, err := os.ReadFile(filepath.Join(cfg.DataDir, "tls", "ca.pem"))
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(data) {
		return fmt.Errorf("invalid instance CA")
	}
	// Connect over loopback while validating the actual certificate's name.
	// Publicly issued domain certificates cannot contain localhost SANs.
	name, err := healthName(cfg)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: name, MinVersion: tls.VersionTLS12}}}
	defer client.CloseIdleConnections()
	var peer []byte
	targets := []struct {
		url  string
		code int
	}{
		{"https://127.0.0.1" + cfg.ManagementAddr + "/healthz", 200},
		{"https://127.0.0.1" + cfg.ManagementAddr + "/", 200},
		{"https://127.0.0.1" + cfg.ManagementAddr + "/media/0/probe", 401},
	}
	if len(os.Args) == 3 {
		port, err := strconv.Atoi(os.Args[2])
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("invalid preview port")
		}
		targets = append(targets, struct {
			url  string
			code int
		}{fmt.Sprintf("https://127.0.0.1:%d/healthz", port), 200})
	}
	for _, target := range targets {
		response, err := client.Get(target.url)
		if err != nil {
			return fmt.Errorf("TLS health request failed")
		}
		response.Body.Close()
		if response.StatusCode != target.code {
			return fmt.Errorf("TLS health returned HTTP %d", response.StatusCode)
		}
		cert := response.TLS.PeerCertificates[0].Raw
		if peer != nil && string(peer) != string(cert) {
			return fmt.Errorf("HTTPS endpoints use different certificates")
		}
		peer = cert
	}
	return nil
}
