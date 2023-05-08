# RemoteUnleash

Status: `draft`

`RemoteUnleash` represents a remote Unleash server that is not managed directly by the operator. This is useful if you want to use other features of the operator such as `ApiTokens`, but do not want to manage the Unleash server itself.

## Spec

`RemoteUnleash` has the following spec:

```yaml
apiVersion: unleash.nais.io/v1alpha1
kind: RemoteUnleash
spec:
  # Unleash server config
  unleashServer:
    url: https://unleash.nais.io

  # Admin secret to use for the Unleash API
  adminSecret:
    name: unleasherator-api-token # must start with unleasherator-
    key: token
    namespace: default
```

`adminSecret.name` must start with `unleasherator-`.