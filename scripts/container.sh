#!/usr/bin/env bash
# Build, verify and run the production image with Docker or rootless Podman.
# `deploy` is the laptop release gate: a persistent container Mike validates at :19900.
set -euo pipefail
cd "$(dirname "$0")/.."
engine=${CONTAINER_ENGINE:-$(command -v podman >/dev/null && echo podman || echo docker)}
test_engine=$engine
engine_args=()
# Use Podman's native remote API for rootless device groups and user namespaces.
if [[ $engine == docker && -n ${DOCKER_HOST:-} ]] &&
   [[ $(docker version --format '{{range .Server.Components}}{{.Name}}{{end}}' 2>/dev/null) == *'Podman Engine'* ]]; then
  engine=podman
  engine_args=(--remote --url "$DOCKER_HOST")
fi
image=${CONTAINER_IMAGE:-alreadyon:local}
# Defaults are the contract; the overrides exist so this script can be exercised beside a live deployment.
name=${LOCAL_CONTAINER_NAME:-alreadyon}
web_port=${LOCAL_WEB_PORT:-19900}
media_dir=${LOCAL_MEDIA_DIR:-/data/media}

build() {
  # Podman defaults to OCI images, which drop the Dockerfile HEALTHCHECK.
  local format=()
  [[ $("$engine" --version 2>&1) == *podman* ]] && format=(--format docker)
  local bases=() resolved ci_base runtime_base
  # Check base age/recipes before every build and pin IDs for in-flight stability.
  if [[ $engine == podman && ${#engine_args[@]} == 0 ]]; then
    resolved=$(python3 scripts/local-bases.py --engine podman)
    read -r ci_base runtime_base <<< "$resolved"
    bases=(--pull=never --layers --build-arg "CI_BASE=$ci_base" --build-arg "RUNTIME_BASE=$runtime_base")
  fi
  "$engine" "${engine_args[@]}" build "${format[@]}" "${bases[@]}" --build-arg "VCS_REF=$(git rev-parse HEAD)" --build-arg "BUILD_TIMESTAMP=$(date +%s)" \
    --target runtime -f deploy/Dockerfile -t "$image" .
}

# Default bridge networking, the production ports and the production media path. The config volume
# keeps the local database across redeploys. label=disable avoids relabeling the shared media tree on SELinux hosts.
start() {
  local devices=()
  local config_mount="$name-config:/config"
  [[ -d /dev/dri ]] && devices+=(--device=/dev/dri:/dev/dri)
  if [[ $("$engine" --version 2>&1) == *podman* ]] &&
     [[ $("$engine" "${engine_args[@]}" info --format '{{.Host.Security.Rootless}}') == true ]]; then
    # Stay mapped to the host user; entrypoint privilege dropping would lose GPU access.
    devices+=(--userns=keep-id:uid=1000,gid=1000 --user=1000:1000 --annotation=run.oci.keep_original_groups=1)
    config_mount+=:U
  fi
  "$engine" "${engine_args[@]}" rm -f "$name" >/dev/null 2>&1 || true
  "$engine" "${engine_args[@]}" run -d --name "$name" --network bridge --restart unless-stopped --security-opt label=disable \
    "${devices[@]}" \
    -p "$web_port:19900" -p "19902:19902" \
    -v "$media_dir:/data/media:ro" -v "$config_mount" \
    "$1" >/dev/null
}

healthy() {
  local trust_dir certificate_name
  trust_dir=$(mktemp -d)
  for _ in {1..90}; do
    # Acquire the public CA through the trusted engine, never a network download.
    if "$engine" "${engine_args[@]}" cp "$name:/config/tls/ca.pem" "$trust_dir/ca.pem" >/dev/null 2>&1 &&
       certificate_name=$("$engine" "${engine_args[@]}" exec --user 1000:1000 "$name" alreadyon-tls hostname) &&
       { [[ $certificate_name != *:* ]] || certificate_name="[$certificate_name]"; } &&
       curl --noproxy '*' --fail --silent --max-time 3 --cacert "$trust_dir/ca.pem" --connect-to "$certificate_name:$web_port:127.0.0.1:$web_port" "https://$certificate_name:$web_port/healthz" >/dev/null &&
       curl --noproxy '*' --fail --silent --max-time 3 --cacert "$trust_dir/ca.pem" --connect-to "$certificate_name:$web_port:127.0.0.1:$web_port" "https://$certificate_name:$web_port/" >/dev/null &&
       "$engine" "${engine_args[@]}" exec --user 1000:1000 "$name" alreadyon-tls health; then
      rm "$trust_dir/ca.pem"; rmdir "$trust_dir"
      return 0
    fi
    sleep 2
  done
  [[ ! -f $trust_dir/ca.pem ]] || rm "$trust_dir/ca.pem"
  rmdir "$trust_dir"
  return 1
}

# A restart policy alone does not survive a reboot of rootless Podman: its restart unit must be enabled
# and the user session must linger so the container starts before anyone logs in.
ensure_autostart() {
  if (( ${#engine_args[@]} )); then
    echo 'On the engine host, enable podman-restart.service and user lingering for restart after reboot.' >&2
    return
  fi
  if [[ $("$engine" --version 2>&1) == *podman* ]]; then
    systemctl --user enable podman-restart.service >/dev/null 2>&1 || echo 'WARNING: could not enable podman-restart.service; the container will not start after a reboot.' >&2
    if [[ $(loginctl show-user "$USER" --property=Linger --value 2>/dev/null) != yes ]]; then
      loginctl enable-linger "$USER" 2>/dev/null || echo "WARNING: run 'loginctl enable-linger $USER' so the container starts without a login." >&2
    fi
  elif ! systemctl is-enabled --quiet docker.service 2>/dev/null; then
    echo 'WARNING: docker.service is not enabled; the container will not start after a reboot.' >&2
  fi
}

case "${1:-}" in
  build)
    build
    ;;
  deploy)
    [[ -d $media_dir ]] || { echo "Media directory $media_dir does not exist" >&2; exit 1; }
    # Build first: a failed build leaves the running container untouched.
    build
    previous=$("$engine" "${engine_args[@]}" inspect --format '{{.Image}}' "$name" 2>/dev/null || true)
    start "$image"
    if ! healthy; then
      echo 'The new container did not become healthy; recent logs:' >&2
      "$engine" "${engine_args[@]}" logs --tail 40 "$name" >&2 || true
      if [[ -n $previous ]]; then
        echo 'Restoring the previous container. Database migrations already applied are not reverted.' >&2
        start "$previous"
      else
        "$engine" "${engine_args[@]}" rm -f "$name" >/dev/null 2>&1 || true
      fi
      exit 1
    fi
    ensure_autostart
    echo "Deployed $name from $(git rev-parse --short HEAD): https://localhost:$web_port (UI/API/media)"
    echo "Public trust export: $engine exec --user 1000:1000 $name alreadyon-tls trust"
    echo "Verify fingerprint: $engine exec --user 1000:1000 $name alreadyon-tls fingerprint"
    ;;
  status)
    "$engine" "${engine_args[@]}" ps -a --filter "name=^$name\$" --format '{{.Names}}  {{.Status}}  {{.Ports}}'
    "$engine" "${engine_args[@]}" inspect --format 'restart={{.HostConfig.RestartPolicy.Name}}' "$name"
    "$engine" "${engine_args[@]}" exec --user 1000:1000 "$name" alreadyon-tls health && echo ' HTTPS healthy'
    ;;
  stop)
    "$engine" "${engine_args[@]}" rm -f "$name"
    echo "Removed $name; the $name-config volume (database) is kept."
    ;;
  test)
    CONTAINER_ENGINE="$test_engine" CONTAINER_IMAGE="$image" python3 tests/integration/container.py
    ;;
  *) echo 'Usage: scripts/container.sh build|deploy|status|stop|test' >&2; exit 2 ;;
esac
