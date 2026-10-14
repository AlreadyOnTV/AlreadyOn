#!/usr/bin/env bash
# Pull the published :nightly image and recreate The installation's one existing service in place.
# the deployment host is a deployment target, not a backup, rollback, build, or test workspace.
set -euo pipefail

# Environment overrides exist only so repository tests can use a fake Docker.
image=${NIGHTLY_IMAGE:-ghcr.io/alreadyontv/alreadyon:nightly}
app_dir=${APP_DIR:-/opt/apps/alreadyon}
compose_file=$app_dir/docker-compose.yaml
service=alreadyon
health_timeout=${HEALTH_TIMEOUT:-180}

log() { printf '%s %s\n' "$(date '+%F %T')" "$*"; }
die() { log "ERROR: $*" >&2; exit 1; }

# Docker health plus the application's own endpoint; a started container is not a working one.
wait_healthy() {
  local expected=$1 deadline=$((SECONDS + health_timeout))
  while ((SECONDS < deadline)); do
    if [[ $(docker inspect --format '{{.Image}} {{.State.Health.Status}}' "$service" 2>/dev/null) == "$expected healthy" ]] &&
       docker exec --user 1000:1000 "$service" alreadyon-tls health; then
      return 0
    fi
    sleep 3
  done
  return 1
}

preflight() {
  [[ -f $app_dir/alreadyon.db ]] || die "No database in $app_dir; refusing an implicit fresh installation"
  docker compose -f "$compose_file" config --quiet || die 'Compose file is invalid'
  docker compose -f "$compose_file" config --images | grep -qx "$image" ||
    die "Compose must run $image directly"
  docker inspect "$service" >/dev/null 2>&1 || die "No $service container to replace"
}

case "${1:-}" in
  --check) preflight; docker manifest inspect "$image" >/dev/null || die "Cannot read $image; run 'docker login ghcr.io' as $USER with a read:packages token"
           log 'Preflight passed'; exit 0 ;;
  '') ;;
  *) echo 'Usage: alreadyon-nightly-sync [--check]' >&2; exit 2 ;;
esac

preflight
docker pull --quiet "$image" >/dev/null || die "Cannot pull $image; run 'docker login ghcr.io' as $USER with a read:packages token"
candidate=$(docker image inspect --format '{{.Id}}' "$image")
current=$(docker inspect --format '{{.Image}}' "$service")
revision=$(docker image inspect --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$image")
if [[ $candidate == "$current" ]]; then
  log "Already running ${revision:0:12}; nothing to do"
  exit 0
fi

# Recreate the one Compose service directly from :nightly. Do not create local
# aliases, rollback tags, database copies, recovery folders, or test containers.
cd "$app_dir"
docker compose up -d --no-deps --force-recreate "$service" >/dev/null
wait_healthy "$candidate" || die "Deployed ${revision:0:12}, but $service did not become healthy"
log "Deployed ${revision:0:12}"
