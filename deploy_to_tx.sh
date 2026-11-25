#!/usr/bin/env bash
set -euo pipefail

# Configuration
IMAGE_NAME="new-api:local-fix"
TAR_FILE="new-api-local.tar"
REMOTE_HOST="tx"
REMOTE_PATH="/tmp/${TAR_FILE}"
REMOTE_START_SCRIPT="/home/sam/apps/new-api/start.sh"

RESTART_REMOTE="false"
DEBUG_MODE="false"

usage() {
  cat <<EOF
Usage: $0 [--restart] [--debug]

Options:
  --restart   Automatically run ${REMOTE_START_SCRIPT} on ${REMOTE_HOST} after loading the image
  --debug     Pass --debug to ${REMOTE_START_SCRIPT} (requires --restart)
  -h, --help  Show this help message
EOF
}

for arg in "$@"; do
  case "$arg" in
    --restart)
      RESTART_REMOTE="true"
      ;;
    --debug)
      DEBUG_MODE="true"
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown option: $arg" >&2
      usage >&2
      exit 1
      ;;
  esac
done

if [ "${DEBUG_MODE}" = "true" ] && [ "${RESTART_REMOTE}" != "true" ]; then
  echo "Error: --debug requires --restart" >&2
  usage >&2
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "${SCRIPT_DIR}"

CURRENT_BRANCH="$(git rev-parse --abbrev-ref HEAD)"
if [ "${CURRENT_BRANCH}" != "sam-dev" ]; then
  echo "Error: production builds must run from branch 'sam-dev' (current: '${CURRENT_BRANCH}')" >&2
  exit 1
fi

cleanup() {
  rm -f "${SCRIPT_DIR}/${TAR_FILE}"
}
trap cleanup EXIT

TOTAL_STEPS=4
if [ "${RESTART_REMOTE}" = "true" ]; then
  TOTAL_STEPS=5
fi

echo "🚀 [1/${TOTAL_STEPS}] Building Docker image for linux/amd64 (target for TX server)..."
docker buildx build --platform linux/amd64 --build-arg GOPROXY="https://goproxy.cn,direct" -t "${IMAGE_NAME}" --load .

echo "📦 [2/${TOTAL_STEPS}] Saving image to ${TAR_FILE}..."
docker save -o "${TAR_FILE}" "${IMAGE_NAME}"

echo "☁️  [3/${TOTAL_STEPS}] Uploading image to TX server (${REMOTE_HOST}:${REMOTE_PATH})..."
scp "${TAR_FILE}" "${REMOTE_HOST}:${REMOTE_PATH}"

echo "🔧 [4/${TOTAL_STEPS}] Loading image on TX server..."
ssh "${REMOTE_HOST}" "docker load -i '${REMOTE_PATH}' && rm -f '${REMOTE_PATH}'"

echo "🧹 Cleaning up local tar file..."
rm -f "${TAR_FILE}"

if [ "${RESTART_REMOTE}" = "true" ]; then
  START_FLAGS=""
  if [ "${DEBUG_MODE}" = "true" ]; then
    START_FLAGS=" --debug"
  fi
  echo "🔄 [5/${TOTAL_STEPS}] Restarting container on TX server via ${REMOTE_START_SCRIPT}${START_FLAGS}..."
  ssh "${REMOTE_HOST}" "${REMOTE_START_SCRIPT}${START_FLAGS}"

  echo "⏳ Waiting for TX new-api health check..."
  for i in $(seq 1 15); do
    if ssh "${REMOTE_HOST}" "curl -sf http://127.0.0.1:3000/api/status >/dev/null"; then
      echo "✅ Done! Container restarted and /api/status is healthy."
      exit 0
    fi
    sleep 1
  done
  echo "⚠️  Container restarted, but /api/status did not respond within 15s. Check: ssh ${REMOTE_HOST} 'docker logs --tail 50 new-api'" >&2
  exit 1
fi

echo ""
echo "✅ Done! The image '${IMAGE_NAME}' is now loaded on the TX server."
echo ""
echo "⚠️  Container was NOT restarted automatically (default safe mode)."
echo "To restart on TX manually:"
echo "    ssh ${REMOTE_HOST} '${REMOTE_START_SCRIPT}'          # normal mode"
echo "    ssh ${REMOTE_HOST} '${REMOTE_START_SCRIPT} --debug'  # debug mode"
echo "Or run deploy with --restart (outside a session depending on this gateway):"
echo "    ./deploy_to_tx.sh --restart [--debug]"
echo ""
