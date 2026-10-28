#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
: "${RAPIDO_IMAGE:?Set the candidate image repository}"
: "${RAPIDO_TAG:?Set the candidate image tag}"
run_id="${GITHUB_RUN_ID:-$$}"
[[ "$run_id" =~ ^[0-9]+$ ]] || { echo 'Invalid smoke test run identifier'; exit 1; }
project="rapido-smoke-$run_id"
platform_file="$(mktemp)"
files=(-f docker-compose.prod.yml -f "$platform_file")
export RAPIDO_IMAGE RAPIDO_TAG
export POSTGRES_PASSWORD=smoke-test-database-password
export SUDO_USERNAME=smoke_admin SUDO_PASSWORD=smoke-test-admin-password
export POSTGRES_MAX_CONNECTIONS=200 PANEL_API_REPLICAS=1
compose() { docker compose -p "$project" "${files[@]}" --env-file /dev/null "$@"; }
cleanup() {
    local result=$?
    if [ "$result" != 0 ]; then
        compose ps -a || true
        compose logs --tail 40 migrate panel backend || true
    fi
    compose down -v --remove-orphans >/dev/null 2>&1 || true
    rm -f "$platform_file"
}
trap cleanup EXIT
wait_service() {
    local service="$1" architecture="$2" ready=0 attempt cid image_id
    for attempt in $(seq 1 120); do
        if compose exec -T "$service" wget -q -O /dev/null http://127.0.0.1:8000/dashboard/ </dev/null 2>/dev/null; then
            ready=1
            break
        fi
        sleep 1
    done
    if [ "$ready" != 1 ]; then
        compose ps -a
        compose logs --tail 60 "$service"
        echo "$service failed to start with the image's default command"
        return 1
    fi
    [ "$(compose exec -T "$service" awk '/^Uid:/ {print $2}' /proc/1/status </dev/null | tr -d '\r')" = 10001 ] \
        || { echo "$service did not drop root privileges"; return 1; }
    cid="$(compose ps -q "$service")"
    image_id="$(docker inspect --format '{{.Image}}' "$cid")"
    [ "$(docker image inspect --format '{{.Architecture}}' "$image_id")" = "$architecture" ] \
        || { echo "$service did not run the requested architecture"; return 1; }
    [ "$(docker inspect --format '{{.RestartCount}}' "$cid")" = 0 ] \
        || { echo "$service restarted during the startup test"; return 1; }
}
first=1
for platform in ${SMOKE_PLATFORMS:-linux/amd64 linux/arm64}; do
    case "$platform" in linux/amd64|linux/arm64) ;; *) echo 'Unsupported smoke test platform'; exit 1 ;; esac
    cat > "$platform_file" <<YAML
services:
  panel:
    platform: $platform
  backend:
    platform: $platform
YAML
    if [ "$first" = 1 ]; then
        compose up -d postgres redis migrate panel backend
        first=0
    else
        compose up -d --no-deps --force-recreate panel backend
    fi
    wait_service panel "${platform#linux/}"
    wait_service backend "${platform#linux/}"
    for asset in statics/fonts/inter-v20-latin.woff2 statics/fonts/vazirmatn-v16-arabic.woff2 statics/locales/fa.json statics/favicon/site.webmanifest; do
        compose exec -T panel wget -q -O /dev/null "http://127.0.0.1:8000/dashboard/$asset" </dev/null \
            || { echo "Dashboard asset is not readable: $asset"; exit 1; }
    done
    headers="$(compose exec -T panel wget -S -O /dev/null http://127.0.0.1:8000/api/tunnels/1/exits </dev/null 2>&1 || true)"
    if [ "${SMOKE_EXPECT_PRIVATE:-0}" = 1 ]; then
        printf '%s\n' "$headers" | grep -q '401 Unauthorized' \
            || { echo 'Private API routes are missing'; exit 1; }
        compose exec -T panel sh -c 'find /app/web/dist/assets -name "*ExitsPage*" | grep -q .' </dev/null \
            || { echo 'Private dashboard extension is missing'; exit 1; }
    else
        printf '%s\n' "$headers" | grep -q '404 Not Found' \
            || { echo 'Unexpected private API routes in a public build'; exit 1; }
    fi
    echo "Default container startup, HTTP readiness, and UID checks passed on $platform."
done
