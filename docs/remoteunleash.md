# RemoteUnleash

Status: `alpha`

`RemoteUnleash` represents a remote Unleash server that is not managed directly by the operator. This is useful if you want to use other features of the operator such as `ApiTokens`, but do not want to manage the Unleash server itself.

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
    name: unleasherator-my-unleash-admin-key-k0hzn3uxcx
    key: token
    namespace: my-namespace
```

`adminSecret.name` must start with `unleasherator-`.
