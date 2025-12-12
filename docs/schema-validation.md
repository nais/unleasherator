# Schema Validation

Unleasherator maintains its own API client types for the Unleash API. To prevent API compatibility issues (like the [v7 tokenName issue](https://github.com/nais/unleasherator/issues/618)), we validate our types against official Unleash OpenAPI schemas.

## How It Works

1. **Schema Storage**: Official OpenAPI schemas from Unleash v5, v6, and v7 are stored in [`hack/schemas/`](../hack/schemas/)
2. **Validation Tests**: Tests in [`internal/unleashclient/schema_test.go`](../internal/unleashclient/schema_test.go) verify our types match expected API contracts
3. **CI Enforcement**: Schema validation runs automatically in CI on every PR

## Running Schema Validation

Validate that your types match Unleash API expectations:

```bash
make test-schemas
```

This runs focused tests on API types to ensure:
- Required fields are present (e.g., `tokenName` for v7)
- JSON tags are correct
- Field types match API expectations
- Backward compatibility is maintained

## Updating Schemas

When Unleash releases a new version or you need to refresh schemas:

```bash
make fetch-schemas
```

This will:
1. Start Docker containers for Unleash v5, v6, and v7
2. Fetch their OpenAPI schemas from `/docs/openapi.json`
3. Save schemas to `hack/schemas/`
4. Clean up containers

**Requirements**: Docker must be running.

## When to Update Schemas

- Before each Unleasherator release
- When upgrading Unleash dependencies
- When adding new API client methods
- When Unleash releases a new major version
- When investigating API compatibility issues

## Adding New API Types

When adding new types to `internal/unleashclient/`:

1. **Implement the type** with correct JSON tags:
   ```go
   type NewRequest struct {
       Field1 string `json:"field1"`
       Field2 int    `json:"field2,omitempty"`
   }
   ```

2. **Add validation tests** in `schema_test.go`:
   ```go
   func TestNewRequest_HasRequiredFields(t *testing.T) {
       req := unleashclient.NewRequest{
           Field1: "value",
           Field2: 42,
       }

       data, err := json.Marshal(req)
       require.NoError(t, err)

       var fields map[string]interface{}
       json.Unmarshal(data, &fields)

       assert.Contains(t, fields, "field1", "field1 is required")
       // field2 is optional, only present if non-zero
   }
   ```

3. **Run validation**:
   ```bash
   make test-schemas
   ```

4. **Document in godoc** which Unleash version(s) the type supports

## CI Integration

Schema validation is integrated into the CI pipeline in `.github/workflows/image.yml`:

```yaml
- name: Validate API types against Unleash schemas
  run: make test-schemas
```

This ensures that PRs introducing type mismatches fail CI before merging.

## Future Improvements

Currently we validate types manually through tests. Future improvements could include:

1. **Full type generation**: Generate types from OpenAPI schemas using `oapi-codegen`
2. **Automated schema updates**: Fetch schemas automatically in CI
3. **Multi-version support**: Generate separate types for each Unleash version
4. **Breaking change detection**: Automated detection of schema changes between versions

See the [schema validation recommendations](https://github.com/nais/unleasherator/issues/618#recommendations) for more details.

## Troubleshooting

### Schema tests fail after Unleash upgrade

This indicates a breaking change in the Unleash API:

1. **Fetch latest schemas**: `make fetch-schemas`
2. **Review test failures**: Identify which fields/types changed
3. **Update types**: Modify structs in `internal/unleashclient/`
4. **Test compatibility**: Ensure backward compatibility with older versions
5. **Update docs**: Document version-specific behavior

### Docker not available

The `fetch-schemas` target requires Docker. If Docker is not available:

1. Schemas are already committed to `hack/schemas/`
2. You can skip schema updates unless debugging compatibility issues
3. CI will still validate against committed schemas

### Tests pass locally but fail in CI

Ensure your schema files are up to date and committed:

```bash
make fetch-schemas
git add hack/schemas/
git commit -m "Update Unleash schemas"
```

## Related Documentation

- [API Token Resource](./apitoken.md)
- [Observability](./observability.md)
- [Contributing Guide](../CONTRIBUTING.md)
