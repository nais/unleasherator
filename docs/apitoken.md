# ApiToken

Status: `draft`

`ApiToken` is a resource that allows you to create API tokens for Unleash.

## Implementation

Check if an `Unleash` or `RemoteUnleash`, resource exists in the same namespace, or a `GlobalUnleash`, with the name specified in the `unleashName` field. If it does, create an API token for the Unleash instance and store it in a secret with the name specified in the `secretName` in the same namespace.

Inside the secret, the API token will be stored in the `token` key.

## Spec

```yaml
apiVersion: unleash.nais.io/v1alpha1
kind: ApiToken
spec:
  # The name of the Unleash instance to create the token for
  unleashName: unleash
  # The name of the secret to store the token in
  secretName: unleash-api-token
```
