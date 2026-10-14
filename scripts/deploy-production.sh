#!/usr/bin/env bash
# Ship the locally tested image to the deployment host and recreate its one service in place.
# Pause the target installation's automatic updater before manual transfer.
set -euo pipefail

host=${1:?Usage: deploy-production.sh <ssh-host> [compose-directory]}
app_dir=${2:-/opt/apps/alreadyon}
target=ghcr.io/alreadyontv/alreadyon:nightly
local_image=alreadyon:local
engine=$(command -v podman || command -v docker)

die() { echo "ERROR: $*" >&2; exit 1; }

[[ -z $(git status --porcelain) ]] || die 'Commit or stash changes; the deployment host must run a committed revision'
revision=$(git rev-parse HEAD)
label=$("$engine" image inspect "$local_image" --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' 2>/dev/null) ||
  die "No $local_image image; run make image first"
[[ $label == "$revision" ]] || die "$local_image is $label but HEAD is $revision; run make image first"
if [[ $(ssh "$host" 'systemctl is-enabled alreadyon-nightly-sync.timer 2>/dev/null || true') == enabled ]]; then
  die 'the deployment host nightly sync is enabled and would replace this image; pause the automatic updater before manual deployment'
fi

echo "Copying $local_image ($revision) to $host"
"$engine" save --format docker-archive "$local_image" | gzip -1 | ssh "$host" 'gunzip | docker load --quiet'

# pull_policy: always in the compose file would otherwise re-pull the published tag.
ssh "$host" bash -s -- "$target" "$app_dir" "$revision" <<'REMOTE'
set -euo pipefail
target=$1 app_dir=$2 revision=$3
loaded=$(docker image ls --format '{{.Repository}}:{{.Tag}}' | grep -m1 -E '(^|/)alreadyon:local$')
docker tag "$loaded" "$target"
docker image rm "$loaded" >/dev/null
cd "$app_dir"
docker compose up -d --force-recreate --pull never alreadyon
expected=$(docker image inspect "$target" --format '{{.Id}}')
for _ in $(seq 60); do
  if [[ $(docker inspect --format '{{.Image}} {{.State.Health.Status}}' alreadyon) == "$expected healthy" ]] &&
     docker exec --user 1000:1000 alreadyon alreadyon-tls health >/dev/null; then
    running=$(docker inspect alreadyon --format '{{index .Config.Labels "org.opencontainers.image.revision"}}')
    [[ $running == "$revision" ]] || { echo "Running revision $running, expected $revision" >&2; exit 1; }
    docker image prune -f >/dev/null
    echo "the deployment host runs $revision: $(docker ps --filter name=^alreadyon$ --format '{{.Status}}')"
    exit 0
  fi
  sleep 3
done
echo 'alreadyon did not become healthy within 180 seconds' >&2
docker logs --tail 40 alreadyon >&2
exit 1
REMOTE
