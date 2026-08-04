#!/bin/bash
set -e

# Configuration
IMAGE_NAME="new-api:local-fix"
TAR_FILE="new-api-local.tar"
REMOTE_HOST="tx"
REMOTE_PATH="/tmp/${TAR_FILE}"

echo "🚀 [1/4] Building Docker image for linux/amd64 (target for TX server)..."
# We build specifically for linux/amd64 as most remote servers are amd64. 
# If TX is arm64, change this to linux/arm64
docker buildx build --platform linux/amd64 --build-arg GOPROXY="https://goproxy.cn,direct" -t ${IMAGE_NAME} --load .

echo "📦 [2/4] Saving image to ${TAR_FILE}..."
docker save -o ${TAR_FILE} ${IMAGE_NAME}

echo "☁️  [3/4] Uploading image to TX server (${REMOTE_HOST}:${REMOTE_PATH})..."
scp ${TAR_FILE} ${REMOTE_HOST}:${REMOTE_PATH}

echo "🔧 [4/4] Loading image on TX server..."
ssh ${REMOTE_HOST} "docker load -i ${REMOTE_PATH} && rm -f ${REMOTE_PATH}"

echo ""
echo "✅ Done! The image '${IMAGE_NAME}' is now loaded on the TX server."
echo "🧹 Cleaning up local tar file..."
rm -f ${TAR_FILE}

echo ""
echo "⚠️ IMPORTANT: DO NOT restart the container from this Wopal session!"
echo "To restart the container, open a SEPARATE terminal and run:"
echo ""
echo "    ssh ${REMOTE_HOST}"
echo "    docker stop new-api && docker rm new-api"
echo "    # Replace the following with your actual docker run parameters:"
echo "    docker run -d --name new-api --restart always -p 3000:3000 \\"
echo "      -v /your/data/path:/data \\"
echo "      -e SQL_DSN=\"root:s.x@tcp(mysql:3306)/oneapi\" \\"
echo "      -e TZ=\"Asia/Shanghai\" \\"
echo "      -e REDIS_CONN_STRING=\"redis://redis\" \\"
echo "      ${IMAGE_NAME}"
echo ""
