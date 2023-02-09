# RemoteUnleash

Status: `draft`

`RemoteUnleash` represents a remote Unleash server that is not managed directly by the operator. This is useful if you want to use other features of the operator such as `ApiTokens`, but do not want to manage the Unleash server itself.

## Spec

`RemoteUnleash` has the following spec:

```yaml
apiVersion: unleash.nais.io/v1alpha1
kind: RemoteUnleash
spec:
  # The URL of the Unleash server
  url: https://unleash.nais.io
  # The name of the secret containing the API token
  apiTokenSecretName: unleash-api-token
  # The key in the secret containing the API token
  apiTokenSecretKey: token
```
