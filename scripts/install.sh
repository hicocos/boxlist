#!/bin/sh
# New isolated installation only. Never adopt/chown/recreate existing data or containers.
set -eu
umask 027
DIR=/srv/boxlist
PORT=5244
NAME=boxlist
IMAGE=
VERSION=
DRY_RUN=false
fail() { printf 'Error: %s\n' "$*" >&2; exit 1; }
usage() {
  printf '%s\n' \
    'Usage: install.sh --version custom-vX.Y.Z [options]' \
    '   or: install.sh --image REGISTRY/IMAGE:PINNED_TAG [options]' \
    '  --dir PATH    New, nonexistent absolute data directory (/srv/boxlist)' \
    '  --port PORT   Host HTTP port (5244), published on all host interfaces' \
    '  --name NAME   New container name (boxlist)' \
    '  --version TAG Use ghcr.io/hicocos/boxlist:TAG; no default release yet' \
    '  --image REF   Explicit pinned image tag or sha256 digest (no latest)' \
    '  --dry-run     Validate Docker/name/port/path and print; do not pull/write/run' \
    '  --help        Print help; no Docker needed' \
    'Requires root, Docker Engine, ss (iproute2), realpath and a preexisting parent.' \
    'Public release/image availability is pending. Existing installations are refused.'
}
while [ "$#" -gt 0 ]; do
  case "$1" in
    --help|-h) usage; exit 0 ;;
    --dry-run) DRY_RUN=true; shift ;;
    --dir|--port|--name|--version|--image)
      [ "$#" -ge 2 ] || fail "Missing value for $1"
      case "$1" in
        --dir) DIR=$2 ;; --port) PORT=$2 ;; --name) NAME=$2 ;;
        --version) VERSION=$2 ;; --image) IMAGE=$2 ;;
      esac
      shift 2 ;;
    *) fail "Unknown option: $1 (see --help)" ;;
  esac
done
[ -z "$VERSION" ] || [ -z "$IMAGE" ] || fail 'Choose --version OR --image, not both'
if [ -n "$VERSION" ]; then
  case "$VERSION" in *[!a-zA-Z0-9_.-]*|''|-*) fail 'Invalid version tag' ;; esac
  IMAGE=ghcr.io/hicocos/boxlist:$VERSION
fi
[ -n "$IMAGE" ] || fail 'No release is assumed: supply --version or --image (see --help)'
case "$IMAGE" in *[!a-zA-Z0-9_./:@-]*|-*) fail 'Invalid image reference' ;; esac
LAST_COMPONENT=${IMAGE##*/}
case "$LAST_COMPONENT" in
  *@sha256:*)
    DIGEST=${IMAGE##*@sha256:}
    [ "${#DIGEST}" -eq 64 ] || fail 'sha256 digest must have 64 hex characters'
    case "$DIGEST" in *[!a-fA-F0-9]*) fail 'Invalid sha256 digest' ;; esac ;;
  *:*)
    TAG=${IMAGE##*:}
    case "$TAG" in ''|latest|main|rolling|dev|*[!a-zA-Z0-9_.-]*) fail 'Use a fixed image tag, not a moving tag' ;; esac
    case "$TAG" in [a-zA-Z0-9_]*) : ;; *) fail 'Invalid image tag' ;; esac ;;
  *) fail 'Image must include a fixed tag or sha256 digest' ;;
esac
case "$PORT" in ''|*[!0-9]*|0*) fail 'Port must be decimal 1..65535 without leading zeroes' ;; esac
[ "${#PORT}" -le 5 ] && [ "$PORT" -ge 1 ] && [ "$PORT" -le 65535 ] || fail 'Port out of range'
case "$NAME" in ''|*[!a-zA-Z0-9_.-]*) fail 'Invalid container name' ;; esac
case "$NAME" in [a-zA-Z0-9]*) : ;; *) fail 'Container name must start with a letter or digit' ;; esac
case "$DIR" in /*) : ;; *) fail 'Data directory must be absolute' ;; esac
case "$DIR" in /|*[!a-zA-Z0-9_./-]*) fail 'Use a safe data path (letters, digits, /, _, ., - only)' ;; esac
for CMD in docker ss realpath id dirname mkdir chown chmod; do
  command -v "$CMD" >/dev/null 2>&1 || fail "Missing required command: $CMD"
done
CANONICAL=$(realpath -m -- "$DIR")
[ "$CANONICAL" = "$DIR" ] || fail 'Use a canonical path without symlinks, .., or trailing slash'
[ ! -e "$DIR" ] && [ ! -L "$DIR" ] || fail 'Data path already exists; refusing to touch it'
PARENT=$(dirname -- "$DIR")
[ -d "$PARENT" ] && [ -w "$PARENT" ] || fail 'Parent directory must already exist and be writable'
if [ "$DRY_RUN" = false ]; then
  [ "$(id -u)" -eq 0 ] || fail 'Run as root (only the new directory is assigned uid/gid 1001)'
fi
docker info >/dev/null 2>&1 || fail 'Docker daemon unavailable or permission denied'
if docker container inspect "$NAME" >/dev/null 2>&1; then
  fail "Container $NAME already exists; no containers have been changed"
fi
LISTENERS=$(ss -H -ltn "sport = :$PORT") || fail 'Unable to inspect listening TCP ports'
[ -z "$LISTENERS" ] || fail "TCP port $PORT is already listening"
# Covers published ports even when Docker disables its userland listening proxy.
PUBLISHED=$(docker ps --filter "publish=$PORT" --format '{{.ID}}') || fail 'Unable to inspect Docker published ports'
[ -z "$PUBLISHED" ] || fail "A running Docker container already publishes port $PORT"
printf 'Image: %s\nData: %s (new; uid/gid 1001)\nName: %s\nPort: %s -> 5244 (all interfaces)\n' "$IMAGE" "$DIR" "$NAME" "$PORT"
if [ "$DRY_RUN" = true ]; then
  printf '%s\n' 'Dry run complete: no images pulled, files written or containers started.'
  exit 0
fi
# Resolve image availability before creating any data directory.
docker pull "$IMAGE" || fail 'Image pull failed; data path has not been created'
# Re-check immediately before mkdir; mkdir is atomic and cannot adopt an existing dir.
[ ! -e "$DIR" ] && [ ! -L "$DIR" ] || fail 'Data path appeared during image pull'
mkdir -m 0750 -- "$DIR" || fail 'Could not create dedicated new data directory'
chown 1001:1001 -- "$DIR"
chmod 0750 -- "$DIR"
if ! docker run -d --name "$NAME" --restart unless-stopped \
  --publish "$PORT:5244" \
  --mount "type=bind,src=$DIR,dst=/opt/openlist/data" "$IMAGE"; then
  fail "Docker run failed; dedicated directory $DIR retained for inspection. No old data touched."
fi
docker container inspect --format '{{.Name}} {{.State.Status}} image={{.Config.Image}}' "$NAME"
printf '%s\n' \
  'Container created. A running state is not application readiness.' \
  "Check: docker logs $NAME (initial administrator password may appear; keep logs private)" \
  "Then open http://SERVER_IP:$PORT/ and change the initial administrator password." \
  'For Internet access, use HTTPS and a firewall. The HTTP port is published on all interfaces.'
