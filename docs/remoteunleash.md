# RemoteUnleash

Implementation status: `beta`

`RemoteUnleash` represents a remote Unleash server that is not managed directly by the operator.
This is useful if you want to use other features of the operator such as `ApiTokens`,
but do not want to manage the Unleash server itself.

## Spec

`RemoteUnleash` has the following spec:

```yaml
apiVersion: unleash.nais.io/v1
kind: RemoteUnleash
metadata:
  name: my-unleash
  namespace: my-namespace
spec:
  # Unleash server config
  unleashInstance:
    url: https://unleash.nais.io

  # Admin secret to use for the Unleash API
  adminSecret:
    name: unleasherator-my-unleash-admin-key
    key: token
    namespace: my-namespace
```

`adminSecret.name` must start with `unleasherator-`.

### Cross-Namespace Secrets
For security reasons, `RemoteUnleash` strictly limits cross-namespace secret references to prevent privilege escalation:
- Cross-namespace references are **only permitted** if the secret is located in the operator's namespace (e.g. `unleasherator-system`).
- To prevent confused deputy SSRF attacks, cross-namespace secrets must strictly follow this naming format: `unleasherator-<namespace>-admin-key` or start with `unleasherator-<namespace>-admin-key-`. For example, a `RemoteUnleash` in the `my-tenant` namespace can only access secrets in the operator namespace if their name is exactly `unleasherator-my-tenant-admin-key` or starts with `unleasherator-my-tenant-admin-key-`.
