# Unleasherator Helm Chart

Kubernetes operator for managing Unleash feature flag instances.

## Prometheus Alerts

The chart includes optional PrometheusRule resources for alerting. Alerts are automatically enabled based on cluster type:

| Cluster Type | Enabled Alerts                         |
| ------------ | -------------------------------------- |
| Management   | Unleash, ReleaseChannel, UnleashServer |
| Tenant/App   | ApiToken, RemoteUnleash                |

### Manual Configuration

Override the defaults in your values:

```yaml
prometheusRules:
  enabled: true  # master switch (default: true when any sub-resource is enabled)
  # Management cluster alerts
  unleash:
    enabled: true
  releaseChannel:
    enabled: true
  unleashServer:
    enabled: true
    namespace: "bifrost-unleash"  # namespace where Unleash servers run
  # App cluster alerts
  apiToken:
    enabled: true
  remoteUnleash:
    enabled: true
```

Set `prometheusRules.enabled: false` to disable all alerts at once, regardless of individual settings.

### Unleash Alerts

| Alert                             | Severity | Default For | Description                                         |
| --------------------------------- | -------- | ----------- | --------------------------------------------------- |
| `UnleashConnectionProblem`        | critical | 5m          | Unleash instance is not connected                   |
| `UnleashNotReconciled`            | warning  | 10m         | Unleash instance configuration out of sync          |
| `UnleashDegraded`                 | warning  | 5m          | Unleash instance is degraded                        |
| `UnleashReconcileErrors`          | warning  | 10m         | Controller reconciliation error rate >10%           |
| `UnleashReconcileSlow`            | warning  | 15m         | P95 reconciliation time >30s                        |
| `UnleashWorkqueueBacklog`         | warning  | 10m         | Controller workqueue depth >10                      |
| `UnleashFederationPublishFailing` | warning  | 10m         | Federation publishing failure rate >10%             |
| `UnleashMetricsAbsent`            | info     | 30m         | No metrics received (may be expected if no Unleash) |

### ReleaseChannel Alerts

| Alert                               | Severity | Default For | Description                                                 |
| ----------------------------------- | -------- | ----------- | ----------------------------------------------------------- |
| `ReleaseChannelFailed`              | critical | 5m          | ReleaseChannel is in failed state                           |
| `ReleaseChannelStuckInProgress`     | warning  | 1h          | ReleaseChannel stuck in non-final state                     |
| `ReleaseChannelInstancesOutOfSync`  | warning  | 30m         | Instances not updated to target image                       |
| `ReleaseChannelNoInstances`         | info     | 30m         | ReleaseChannel manages 0 instances (may be expected)        |
| `ReleaseChannelHighFailureRate`     | critical | 15m         | >10% rollout failure rate                                   |
| `ReleaseChannelHealthChecksFailing` | warning  | 10m         | Health check failure rate >1%                               |
| `ReleaseChannelSlowRollout`         | warning  | 15m         | P95 rollout duration >300s                                  |
| `ReleaseChannelHighConflictRate`    | warning  | 15m         | Resource conflicts >0.05/sec                                |
| `ReleaseChannelMetricsAbsent`       | info     | 30m         | No metrics received (may be expected if no ReleaseChannels) |

### ApiToken Alerts

| Alert                     | Severity | Default For | Description                                           |
| ------------------------- | -------- | ----------- | ----------------------------------------------------- |
| `ApiTokenFailed`          | critical | 5m          | ApiToken creation/sync failed                         |
| `ApiTokenNotCreated`      | warning  | 15m         | ApiToken not successfully created                     |
| `ApiTokenHighChurn`       | warning  | 15m         | Tokens created/deleted >0.5/sec                       |
| `ApiTokenExcessiveTokens` | warning  | 10m         | Too many tokens per environment (>50)                 |
| `ApiTokenMetricsAbsent`   | info     | 30m         | No metrics received (may be expected if no ApiTokens) |

### RemoteUnleash Alerts

These alerts apply to app clusters that use `RemoteUnleash` resources to connect to Unleash servers in a management cluster.

| Alert                                   | Severity | Default For | Description                                  |
| --------------------------------------- | -------- | ----------- | -------------------------------------------- |
| `RemoteUnleashConnectionProblem`        | critical | 5m          | Cannot connect to remote Unleash server      |
| `RemoteUnleashNotReconciled`            | warning  | 10m         | RemoteUnleash configuration out of sync      |
| `RemoteUnleashReconcileErrors`          | warning  | 10m         | Controller reconciliation error rate >10%    |
| `RemoteUnleashReconcileSlow`            | warning  | 15m         | P95 reconciliation time >30s                 |
| `RemoteUnleashWorkqueueBacklog`         | warning  | 10m         | Controller workqueue depth >10               |
| `RemoteUnleashFederationReceiveFailing` | warning  | 10m         | Federation receiving failure rate >10%       |
| `RemoteUnleashMetricsAbsent`            | info     | 30m         | No metrics (expected in management clusters) |

### Unleash Server Alerts

These alerts monitor the Unleash server instances themselves (database, HTTP, resources). Enable with `prometheusRules.unleashServer.enabled: true`.

| Alert                          | Severity | Default For | Description                               |
| ------------------------------ | -------- | ----------- | ----------------------------------------- |
| `UnleashDbPoolExhaustion`      | critical | 5m          | Database connection pool >90% used        |
| `UnleashDbPoolPendingAcquires` | warning  | 5m          | Requests waiting for database connections |
| `UnleashDbQuerySlow`           | warning  | 10m         | P90 database query time >1s               |
| `UnleashHttpRequestSlow`       | warning  | 10m         | P90 HTTP request time >2000ms             |
| `UnleashHttpErrorRate`         | warning  | 5m          | HTTP 5xx error rate >5%                   |
| `UnleashPodRestarts`           | warning  | 15m         | Pod restarted >3 times in 1 hour          |
| `UnleashOOMKill`               | critical | 5m          | Container experienced OOM kill            |
| `UnleashMemoryPressure`        | warning  | 10m         | Memory usage >90% of limit                |

### Customizing Alerts

Override any alert's `for`, `severity`, or `threshold` via values:

```yaml
prometheusRules:
  releaseChannel:
    enabled: true
    runbookUrl: "https://runbooks.example.com/unleasherator"
    labels:
      team: platform
    alerts:
      failed:
        for: "10m"
        severity: "warning"
      highFailureRate:
        threshold: 0.2  # 20%
      slowRollout:
        thresholdSeconds: 600  # 10 minutes

  apiToken:
    enabled: true
    runbookUrl: "https://runbooks.example.com/unleasherator"
    alerts:
      excessiveTokens:
        threshold: 20
      highChurn:
        threshold: 0.5
```

### Alert Configuration Reference

#### Unleash

| Alert                      | Configurable Fields                             |
| -------------------------- | ----------------------------------------------- |
| `connectionProblem`        | `for`, `severity`                               |
| `notReconciled`            | `for`, `severity`                               |
| `degraded`                 | `for`, `severity`                               |
| `reconcileErrors`          | `for`, `severity`, `threshold` (error rate 0-1) |
| `reconcileSlow`            | `for`, `severity`, `thresholdSeconds`           |
| `workqueueBacklog`         | `for`, `severity`, `threshold` (queue depth)    |
| `federationPublishFailing` | `for`, `severity`, `threshold` (failure rate)   |
| `metricsAbsent`            | `for`, `severity`                               |

#### ReleaseChannel

| Alert                 | Configurable Fields                               |
| --------------------- | ------------------------------------------------- |
| `failed`              | `for`, `severity`                                 |
| `stuckInProgress`     | `for`, `severity`                                 |
| `instancesOutOfSync`  | `for`, `severity`                                 |
| `noInstances`         | `for`, `severity`                                 |
| `highFailureRate`     | `for`, `severity`, `threshold` (failure rate 0-1) |
| `healthChecksFailing` | `for`, `severity`, `threshold` (failures/sec)     |
| `slowRollout`         | `for`, `severity`, `thresholdSeconds`             |
| `highConflictRate`    | `for`, `severity`, `threshold` (conflicts/sec)    |
| `metricsAbsent`       | `for`, `severity`                                 |

#### ApiToken

| Alert             | Configurable Fields                          |
| ----------------- | -------------------------------------------- |
| `failed`          | `for`, `severity`                            |
| `notCreated`      | `for`, `severity`                            |
| `highChurn`       | `for`, `severity`, `threshold` (ops/sec)     |
| `excessiveTokens` | `for`, `severity`, `threshold` (token count) |
| `metricsAbsent`   | `for`, `severity`                            |

#### RemoteUnleash

| Alert                      | Configurable Fields                           |
| -------------------------- | --------------------------------------------- |
| `connectionProblem`        | `for`, `severity`                             |
| `notReconciled`            | `for`, `severity`                             |
| `reconcileErrors`          | `for`, `severity`, `threshold` (error rate)   |
| `reconcileSlow`            | `for`, `severity`, `thresholdSeconds`         |
| `workqueueBacklog`         | `for`, `severity`, `threshold` (queue depth)  |
| `federationReceiveFailing` | `for`, `severity`, `threshold` (failure rate) |
| `metricsAbsent`            | `for`, `severity`                             |

#### Unleash Server

| Alert                  | Configurable Fields                               |
| ---------------------- | ------------------------------------------------- |
| `dbPoolExhaustion`     | `for`, `severity`, `threshold` (pool usage 0-1)   |
| `dbPoolPendingAcquires`| `for`, `severity`, `threshold` (pending count)    |
| `dbQuerySlow`          | `for`, `severity`, `thresholdSeconds`             |
| `httpRequestSlow`      | `for`, `severity`, `thresholdMs`                  |
| `httpErrorRate`        | `for`, `severity`, `threshold` (error rate 0-1)   |
| `podRestarts`          | `for`, `severity`, `threshold` (restart count)    |
| `oomEvents`            | `for`, `severity`                                 |
| `memoryPressure`       | `for`, `severity`, `threshold` (memory usage 0-1) |
