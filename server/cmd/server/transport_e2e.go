//go:build e2e

package main

import (
	"net"
	"net/http"
)

// Historical synthetic browser fixtures use explicitly loopback-only HTTP.
// Production container tests exercise TLS with a trusted instance CA instead.
func serveListener(server *http.Server, listener net.Listener) error { return server.Serve(listener) }
