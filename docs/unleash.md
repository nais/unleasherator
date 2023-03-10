# Unleash

Implementation status: `alpha`

`Unleash` represents an Unleash server that is managed directly by the operator.

## Implementation

The operator will create a deployment with the Unleash server and a service to expose it. It will also create a network policy to allow traffic from operator namespace.

## Features

- [x] Deployment
  - [ ] Custom Image
  - [ ] CloudSQL Proxy
- [x] Secret
- [x] Service
- [ ] Ingress

## Spec

```yaml
apiVersion: unleash.nais.io/v1alpha1
kind: Unleash
spec:
  # The number of replicas to run
  size: 1
  # The database configuration
  database:
    # The name of the secret containing the database credentials
    secretName: postgres-postgresql
    # The key in the secret containing the database password
    secretPassKey: postgres-password
    # The host of the database
    host: postgres-postgresql
    # The name of the database
    databaseName: postgres
    # The port of the database
    port: "5432"
    # The user to connect to the database with
    user: postgres
    # Whether to use SSL when connecting to the database
    ssl: "false"
```
