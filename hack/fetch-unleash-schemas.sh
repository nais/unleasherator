#!/bin/bash
# Fetch OpenAPI schemas from Unleash instances for validation
# This helps ensure our client types match the actual Unleash API

set -e

SCHEMAS_DIR="$(cd "$(dirname "$0")/schemas" && pwd)"
TEMP_DIR=$(mktemp -d)

cleanup() {
    echo "Cleaning up..."
    docker stop unleash-v5-schema unleash-v6-schema unleash-v7-schema 2>/dev/null || true
    docker rm unleash-v5-schema unleash-v6-schema unleash-v7-schema 2>/dev/null || true
    rm -rf "$TEMP_DIR"
}
trap cleanup EXIT

echo "📦 Fetching Unleash OpenAPI schemas..."
echo ""

# Start Unleash instances with minimal config
echo "Starting Unleash v5..."
docker run -d --name unleash-v5-schema \
    -p 4242:4242 \
    -e DATABASE_URL="postgres://unleash:password@host.docker.internal:5432/unleash" \
    -e DATABASE_SSL=false \
    -e ENABLE_OAS=true \
    unleashorg/unleash-server:5 >/dev/null 2>&1 || {
    echo "⚠️  v5 instance failed to start (this is OK if database not available)"
    echo "   Skipping v5 schema fetch"
}

echo "Starting Unleash v6..."
docker run -d --name unleash-v6-schema \
    -p 4243:4242 \
    -e DATABASE_URL="postgres://unleash:password@host.docker.internal:5432/unleash" \
    -e DATABASE_SSL=false \
    -e ENABLE_OAS=true \
    unleashorg/unleash-server:6 >/dev/null 2>&1 || {
    echo "⚠️  v6 instance failed to start (this is OK if database not available)"
    echo "   Skipping v6 schema fetch"
}

echo "Starting Unleash v7..."
docker run -d --name unleash-v7-schema \
    -p 4244:4242 \
    -e DATABASE_URL="postgres://unleash:password@host.docker.internal:5432/unleash" \
    -e DATABASE_SSL=false \
    unleashorg/unleash-server:7 >/dev/null 2>&1 || {
    echo "⚠️  v7 instance failed to start (this is OK if database not available)"
    echo "   Skipping v7 schema fetch"
}

echo "Waiting for Unleash instances to start..."
sleep 15

# Fetch schemas
mkdir -p "$SCHEMAS_DIR"

if docker ps | grep -q unleash-v5-schema; then
    echo "Fetching v5 schema..."
    if curl -sf -m 10 http://localhost:4242/docs/openapi.json > "$SCHEMAS_DIR/unleash-v5-openapi.json"; then
        echo "✅ v5 schema saved to $SCHEMAS_DIR/unleash-v5-openapi.json"
    else
        echo "⚠️  Failed to fetch v5 schema (instance may not be ready)"
    fi
fi

if docker ps | grep -q unleash-v6-schema; then
    echo "Fetching v6 schema..."
    if curl -sf -m 10 http://localhost:4243/docs/openapi.json > "$SCHEMAS_DIR/unleash-v6-openapi.json"; then
        echo "✅ v6 schema saved to $SCHEMAS_DIR/unleash-v6-openapi.json"
    else
        echo "⚠️  Failed to fetch v6 schema (instance may not be ready)"
    fi
fi

if docker ps | grep -q unleash-v7-schema; then
    echo "Fetching v7 schema..."
    if curl -sf -m 10 http://localhost:4244/docs/openapi.json > "$SCHEMAS_DIR/unleash-v7-openapi.json"; then
        echo "✅ v7 schema saved to $SCHEMAS_DIR/unleash-v7-openapi.json"
    else
        echo "⚠️  Failed to fetch v7 schema (instance may not be ready)"
    fi
fi

echo ""
echo "📋 Summary:"
ls -lh "$SCHEMAS_DIR"/*.json 2>/dev/null || echo "No schemas were fetched"
echo ""
echo "💡 Note: These schemas are used to validate our unleashclient types"
echo "   Run 'make test-schemas' to validate types against these schemas"
