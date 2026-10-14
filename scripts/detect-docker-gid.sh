#!/bin/bash
# Detect the Docker group GID for docker-compose.yml
# Usage: ./scripts/detect-docker-gid.sh

set -e

if [ -S /var/run/docker.sock ]; then
    # Socket exists, check its group
    GID=$(stat -c '%g' /var/run/docker.sock 2>/dev/null || stat -f '%g' /var/run/docker.sock 2>/dev/null)
    if [ -n "$GID" ]; then
        echo "Detected Docker socket GID: $GID"
        echo "Add this to your .env file:"
        echo "DOCKER_GID=$GID"
        exit 0
    fi
fi

# Fallback: check docker group
if getent group docker >/dev/null 2>&1; then
    GID=$(getent group docker | cut -d: -f3)
    echo "Detected docker group GID: $GID"
    echo "Add this to your .env file:"
    echo "DOCKER_GID=$GID"
    exit 0
fi

echo "Warning: Could not detect Docker GID automatically"
echo "Please find it manually with: ls -l /var/run/docker.sock"
echo "Then add DOCKER_GID=<gid> to your .env file"
exit 1
