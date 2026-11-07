#!/usr/bin/env bash

set -euo pipefail

# Configurations
POSTGRES_IMAGE="docker.io/postgres"
POSTGRES_TAG="18.6"
CONTAINER_NAME="pulsecheck_database_dev"
VOLUME_NAME="pulsecheck_postgres_dev"

# Check if Docker exists
if ! command -v docker &> /dev/null; then
    echo "Error: docker command not found. Please install Docker." >&2
    exit 1
fi

# Clean up existing stale container if it exists
if docker ps -a --format "{{.Names}}" | grep -q "^$CONTAINER_NAME$"; then
    echo "Removing existing container: ${CONTAINER_NAME}..."
    docker rm -f $CONTAINER_NAME &>/dev/null
fi

echo "Starting PostgreSQL ${POSTGRES_TAG} container..."

# Run the container
docker run -d \
    --name $CONTAINER_NAME \
    -p 5432:5432 \
    -v "${VOLUME_NAME}:/var/lib/postgresql" \
    -e POSTGRES_PASSWORD="postgres_dev_password" \
    "$POSTGRES_IMAGE:$POSTGRES_TAG"

echo "PostgreSQL is running on port 5432."