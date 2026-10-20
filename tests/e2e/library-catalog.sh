#!/usr/bin/env bash
# Focused browser coverage in a disposable database; never points at a preview.
set -euo pipefail
cd "$(dirname "$0")/../.."
repo_root="$PWD"
library_tmp="$(mktemp -d /tmp/alreadyon-library-e2e.XXXXXXXX)"
server_pid=''
cleanup() {
  result=$?
  if [[ -n "$server_pid" ]]; then kill -TERM "$server_pid" 2>/dev/null || true; wait "$server_pid" || true; fi
  # Keep screenshots and failure diagnostics outside the repository for review.
  echo "Library browser artifacts: $library_tmp"
  exit "$result"
}
trap cleanup EXIT INT TERM
chmod 700 "$library_tmp"
touch "$library_tmp/.synthetic-test"
management_port=$(python3 - <<'PY'
import socket
with socket.socket() as sock:
 sock.bind(('127.0.0.1',0))
 print(sock.getsockname()[1])
PY
)
(cd web && npm run build)
(cd server && go build -tags=e2e -ldflags "-X github.com/alreadyonapp/alreadyon/server/internal/config.dataDir=$library_tmp/data -X github.com/alreadyonapp/alreadyon/server/internal/config.webDir=$repo_root/web/dist" -o "$library_tmp/server" ./cmd/server)
ALREADYON_WEB_PORT="$management_port" "$library_tmp/server" > "$library_tmp/server.log" 2>&1 &
server_pid=$!
LIBRARY_TEST_ROOT="$library_tmp" LIBRARY_TEST_URL="http://localhost:$management_port" node tests/e2e/library-catalog.cjs
