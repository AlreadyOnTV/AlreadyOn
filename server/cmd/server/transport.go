//go:build !e2e

package main

import (
	"net"
	"net/http"
)

// Production and agent previews require TLS, with no plaintext fallback.
func serveListener(server *http.Server, listener net.Listener) error {
	return server.ServeTLS(listener, "", "")
}
