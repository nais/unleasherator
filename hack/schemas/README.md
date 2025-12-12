# Unleash OpenAPI Schemas

This directory contains OpenAPI schemas fetched from official Unleash releases.

## Purpose

These schemas are used to:
1. Validate that our `unleashclient` types match the official Unleash API
2. Detect breaking changes when Unleash releases new versions
3. Document the expected API contract for each version

## Updating Schemas

To fetch the latest schemas:

```bash
cd /path/to/unleasherator
./hack/fetch-unleash-schemas.sh
```

This will:
- Start Docker containers for Unleash v5, v6, and v7
- Fetch their OpenAPI schemas
- Save them to this directory

**Note:** Requires Docker to be running.

## Schema Files

- `unleash-v5-openapi.json` - Unleash v5.x API schema
- `unleash-v6-openapi.json` - Unleash v6.x API schema
- `unleash-v7-openapi.json` - Unleash v7.x API schema

## Validation

Run schema validation tests:

```bash
make test-schemas
```

Or manually:

```bash
go test ./internal/unleashclient -run TestSchema -v
```

## When to Update

- Before each Unleasherator release
- When upgrading Unleash dependencies
- When adding new API client methods
- When Unleash releases a new major version

## CI Integration

Schema validation runs automatically in CI to catch type mismatches early.
