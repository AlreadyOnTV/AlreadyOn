#!/usr/bin/env bash
# Fails on storage outside the active-profile and explicitly authorized
# 24-hour capability/network cache, per-profile device volume and metrics view preferences.
# Runtime settings stay in SQLite; see
# docs/DEVELOPMENT.md "Configuration".
set -euo pipefail

cd "$(dirname "$0")/.."

allowed='web/src/((users/deviceStorage|playback/(deviceCache|volumePreference))\.ts|pages/admin/ServerMetrics\.tsx)'
pattern='localStorage|sessionStorage|indexedDB|document\.cookie'

hits=$(grep -rnE "$pattern" web/src --include='*.ts' --include='*.tsx' | grep -vE "^${allowed}:" || true)

if [[ -n "$hits" ]]; then
  echo "Browser storage is only allowed in ${allowed}:" >&2
  echo "$hits" >&2
  echo >&2
  echo "Store settings on the server (see docs/DEVELOPMENT.md, Configuration)." >&2
  exit 1
fi
echo "web storage check: ok"
