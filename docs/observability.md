# Unleasherator Observability Guide

This document provides comprehensive guidance for monitoring, observing, and operating the Unleasherator controller in production environments. It covers metrics, logging, alerting, troubleshooting, and operational best practices.

## Overview

Effective observability of the Unleasherator controller is crucial for:

- **Ensuring reliable Unleash deployments** across your infrastructure
- **Proactively identifying issues** before they impact users
- **Understanding performance characteristics** and capacity requirements
- **Troubleshooting problems** quickly when they occur
- **Planning capacity** and scaling decisions
- **Maintaining compliance** and audit requirements

## Architecture Overview

The Unleasherator controller manages several interconnected components:

```
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────┐
│  ReleaseChannel │───▶│  Unleash CRDs    │───▶│ Unleash Servers │
│  Controllers    │    │  Controllers     │    │ (Deployments)   │
└─────────────────┘    └──────────────────┘    └─────────────────┘
         │                        │                       │
         ▼                        ▼                       ▼
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────┐
│    Metrics      │    │     Events       │    │      Logs      │
│  (Prometheus)   │    │  (Kubernetes)    │    │  (Controller)   │
└─────────────────┘    └──────────────────┘    └─────────────────┘
```

## Metrics Collection

### Core Metrics Categories

The Unleasherator controller exposes metrics across several dimensions:

#### 1. ReleaseChannel Metrics

**Purpose**: Monitor centralized image rollout management

| Metric                                                  | Type      | Description                               | Labels                        |
| ------------------------------------------------------- | --------- | ----------------------------------------- | ----------------------------- |
| `unleasherator_releasechannel_status`                   | Gauge     | Current status of ReleaseChannels         | `namespace`, `name`           |
| `unleasherator_releasechannel_instances_total`          | Gauge     | Total instances managed by ReleaseChannel | `namespace`, `name`           |
| `unleasherator_releasechannel_instances_up_to_date`     | Gauge     | Instances running target image            | `namespace`, `name`           |
| `unleasherator_releasechannel_rollouts_total`           | Counter   | Total rollout events                      | `namespace`, `name`, `result` |
| `unleasherator_releasechannel_rollout_duration_seconds` | Histogram | Rollout completion time                   | `namespace`, `name`           |
| `unleasherator_releasechannel_instance_updates_total`   | Counter   | Instance update attempts                  | `namespace`, `name`, `result` |
| `unleasherator_releasechannel_conflicts_total`          | Counter   | Resource conflicts during updates         | `namespace`, `name`           |

#### 2. Unleash Instance Metrics

**Purpose**: Monitor individual Unleash server health and federation status

| Metric                                     | Type    | Description                                               | Labels                        |
| ------------------------------------------ | ------- | --------------------------------------------------------- | ----------------------------- |
| `unleasherator_unleash_status`             | Gauge   | Status of Unleash instances (1=connected, 0=disconnected) | `namespace`, `name`, `status` |
| `unleasherator_federation_published_total` | Counter | Federation messages published                             | `state`, `status`             |

#### 3. RemoteUnleash Metrics

**Purpose**: Monitor RemoteUnleash instances in app clusters

| Metric                                    | Type    | Description                                                     | Labels                        |
| ----------------------------------------- | ------- | --------------------------------------------------------------- | ----------------------------- |
| `unleasherator_remoteunleash_status`      | Gauge   | Status of RemoteUnleash instances (1=connected, 0=disconnected) | `namespace`, `name`, `status` |
| `unleasherator_federation_received_total` | Counter | Federation messages received                                    | `state`, `status`             |

#### 4. ApiToken Metrics

**Purpose**: Monitor API token lifecycle and management

| Metric                                   | Type    | Description                                                     | Labels                             |
| ---------------------------------------- | ------- | --------------------------------------------------------------- | ---------------------------------- |
| `unleasherator_apitoken_status`          | Gauge   | Status of ApiToken instances (1=created, 0.5=pending, 0=failed) | `namespace`, `name`, `status`      |
| `unleasherator_apitoken_existing_tokens` | Gauge   | Existing tokens in Unleash for ApiToken                         | `namespace`, `name`, `environment` |
| `unleasherator_apitoken_created_total`   | Counter | ApiTokens created in Unleash                                    | `namespace`, `name`                |
| `unleasherator_apitoken_deleted_total`   | Counter | ApiTokens deleted from Unleash                                  | `namespace`, `name`                |

#### 5. Controller Health Metrics (from controller-runtime)

**Purpose**: Monitor controller-runtime and operator health. These metrics are automatically provided by the [controller-runtime](https://github.com/kubernetes-sigs/controller-runtime) library and apply to all controllers (unleash, releasechannel, apitoken, remoteunleash).

| Metric                                        | Type      | Description                     | Labels                 |
| --------------------------------------------- | --------- | ------------------------------- | ---------------------- |
| `controller_runtime_reconcile_total`          | Counter   | Total reconciliations by result | `controller`, `result` |
| `controller_runtime_reconcile_time_seconds`   | Histogram | Reconciliation duration         | `controller`           |
| `controller_runtime_reconcile_errors_total`   | Counter   | Total reconciliation errors     | `controller`           |
| `workqueue_adds_total`                        | Counter   | Work queue additions            | `name`                 |
| `workqueue_depth`                             | Gauge     | Current work queue depth        | `name`                 |
| `workqueue_queue_duration_seconds`            | Histogram | Time items spend in queue       | `name`                 |
| `workqueue_work_duration_seconds`             | Histogram | Time to process items           | `name`                 |
| `workqueue_longest_running_processor_seconds` | Gauge     | Longest running processor       | `name`                 |
| `workqueue_retries_total`                     | Counter   | Total retries                   | `name`                 |

> **Note**: Reconciliation metrics, error counts, and duration tracking are provided by controller-runtime. Custom `unleasherator_*` metrics focus on domain-specific status and business events (federation, token lifecycle, rollout progress) that controller-runtime cannot provide.

### Prometheus Configuration

#### ServiceMonitor Setup

For Prometheus Operator environments:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: unleasherator-controller
  namespace: unleasherator-system
  labels:
    app.kubernetes.io/name: unleasherator
spec:
  selector:
    matchLabels:
      control-plane: controller-manager
  endpoints:
  - path: /metrics
    port: https
    scheme: https
    tlsConfig:
      insecureSkipVerify: true
    interval: 30s
    scrapeTimeout: 10s
```

#### Direct Prometheus Configuration

```yaml
scrape_configs:
  - job_name: 'unleasherator'
    kubernetes_sd_configs:
      - role: endpoints
        namespaces:
          names:
            - unleasherator-system
    relabel_configs:
      - source_labels: [__meta_kubernetes_service_name]
        action: keep
        regex: unleasherator-controller-manager-metrics-service
      - source_labels: [__meta_kubernetes_endpoint_port_name]
        action: keep
        regex: https
    scrape_interval: 30s
    scrape_timeout: 10s
```

## Logging Strategy

### Log Levels and Configuration

The Unleasherator controller uses structured logging with configurable levels:

```yaml
# Controller manager configuration
apiVersion: v1
kind: ConfigMap
metadata:
  name: manager-config
  namespace: unleasherator-system
data:
  controller_manager_config.yaml: |
    apiVersion: controller-runtime.sigs.k8s.io/v1alpha1
    kind: ControllerManagerConfig
    health:
      healthProbeBindAddress: :8081
    metrics:
      bindAddress: 127.0.0.1:8080
    webhook:
      port: 9443
    leaderElection:
      leaderElect: true
      resourceName: controller-leader-election-unleasherator
    logger:
      development: false
      level: info  # debug, info, warn, error
      encoder: json  # json, console
      stacktraceLevel: error
```

### Key Log Messages to Monitor

#### Success Patterns
```json
{"level":"info","msg":"Successfully reconciled Unleash resources","controller":"unleash"}
{"level":"info","msg":"ReleaseChannel rollout completed","controller":"releasechannel","instances_updated":5}
{"level":"info","msg":"ApiToken synchronized successfully","controller":"apitoken"}
```

#### Warning Patterns
```json
{"level":"warn","msg":"Deployment rollout taking longer than expected","timeout":"5m"}
{"level":"warn","msg":"Resource conflict detected, retrying","retry_count":2}
{"level":"warn","msg":"Unleash instance not responding to health checks"}
```

#### Error Patterns
```json
{"level":"error","msg":"Failed to update Unleash instance","error":"context deadline exceeded"}
{"level":"error","msg":"ReleaseChannel rollout failed","instances_failed":2}
{"level":"error","msg":"Cannot connect to Unleash API","endpoint":"http://unleash:4242"}
```

### Log Aggregation

#### Fluent Bit Configuration

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: fluent-bit-config
data:
  fluent-bit.conf: |
    [SERVICE]
        Flush         1
        Log_Level     info
        Daemon        off
        Parsers_File  parsers.conf

    [INPUT]
        Name              tail
        Path              /var/log/containers/*unleasherator*.log
        Parser            docker
        Tag               unleasherator.*
        Refresh_Interval  5

    [FILTER]
        Name                parser
        Match               unleasherator.*
        Key_Name            log
        Parser              json
        Reserve_Data        On

    [OUTPUT]
        Name  es
        Match unleasherator.*
        Host  elasticsearch.logging.svc.cluster.local
        Port  9200
        Index unleasherator-logs
```

## Alerting Rules

### Critical Alerts

#### Controller Health

```yaml
groups:
- name: unleasherator-critical
  rules:
  - alert: UnleasheratorControllerDown
    expr: up{job="unleasherator"} == 0
    for: 5m
    labels:
      severity: critical
    annotations:
      summary: "Unleasherator controller is down"
      description: "The Unleasherator controller has been down for more than 5 minutes."

  - alert: UnleasheratorHighErrorRate
    expr: |
      rate(controller_runtime_reconcile_total{controller=~"unleash|releasechannel|apitoken",result="error"}[5m]) /
      rate(controller_runtime_reconcile_total{controller=~"unleash|releasechannel|apitoken"}[5m]) > 0.1
    for: 5m
    labels:
      severity: critical
    annotations:
      summary: "High error rate in Unleasherator controllers"
      description: "Error rate is {{ $value | humanizePercentage }} for {{ $labels.controller }} controller."
```

#### ReleaseChannel Alerts

```yaml
  - alert: ReleaseChannelRolloutFailed
    expr: |
      increase(unleasherator_releasechannel_rollouts_total{result="failed"}[1h]) > 0
    for: 0m
    labels:
      severity: warning
    annotations:
      summary: "ReleaseChannel rollout failed"
      description: "ReleaseChannel {{ $labels.name }} in {{ $labels.namespace }} had a failed rollout."

  - alert: ReleaseChannelInstancesOutOfSync
    expr: |
      (unleasherator_releasechannel_instances_total -
       unleasherator_releasechannel_instances_up_to_date) > 0
    for: 30m
    labels:
      severity: warning
    annotations:
      summary: "ReleaseChannel instances out of sync"
      description: "{{ $value }} instances in ReleaseChannel {{ $labels.name }} are not running the target image."

  - alert: ReleaseChannelSlowRollout
    expr: |
      histogram_quantile(0.95,
        rate(unleasherator_releasechannel_rollout_duration_seconds_bucket[5m])
      ) > 600
    for: 10m
    labels:
      severity: warning
    annotations:
      summary: "ReleaseChannel rollout taking too long"
      description: "95th percentile rollout duration is {{ $value }}s for ReleaseChannel {{ $labels.name }}."
```

#### Unleash Instance Alerts

```yaml
  - alert: UnleashInstancesDown
    expr: |
      unleasherator_unleash_ready_instances / unleasherator_unleash_instances_total < 0.8
    for: 5m
    labels:
      severity: warning
    annotations:
      summary: "Multiple Unleash instances down"
      description: "Less than 80% of Unleash instances are ready in namespace {{ $labels.namespace }}."

  - alert: UnleashReconciliationStuck
    expr: |
      rate(unleasherator_unleash_reconcile_total[5m]) == 0 and
      unleasherator_unleash_instances_total > 0
    for: 10m
    labels:
      severity: warning
    annotations:
      summary: "Unleash reconciliation appears stuck"
      description: "No reconciliation activity detected for Unleash instances in {{ $labels.namespace }}."
```

## Dashboards

### Grafana Dashboard Structure

#### Executive Overview Dashboard

**Panels:**
- Overall system health (single stat)
- Unleash instances status (pie chart)
- ReleaseChannel rollout success rate (stat)
- Recent alerts (table)
- Resource utilization trends (time series)

#### ReleaseChannel Operations Dashboard

**Panels:**
- Rollout success rate by ReleaseChannel (time series)
- Rollout duration percentiles (heatmap)
- Instance sync status (bar gauge)
- Rollout events timeline (logs panel)
- Conflict rate trends (time series)

#### Detailed Monitoring Dashboard

**Panels:**
- Controller reconciliation rates (time series)
- Work queue depth and processing time (time series)
- Error rates by controller (time series)
- Resource usage (CPU/memory) (time series)
- API server request rates (time series)

### Sample Dashboard JSON

```json
{
  "dashboard": {
    "title": "Unleasherator - ReleaseChannel Operations",
    "panels": [
      {
        "title": "Rollout Success Rate",
        "type": "stat",
        "targets": [
          {
            "expr": "rate(unleasherator_releasechannel_rollouts_total{result=\"success\"}[24h]) / rate(unleasherator_releasechannel_rollouts_total[24h]) * 100",
            "legendFormat": "Success Rate"
          }
        ],
        "fieldConfig": {
          "defaults": {
            "unit": "percent",
            "min": 0,
            "max": 100,
            "thresholds": {
              "steps": [
                {"color": "red", "value": 0},
                {"color": "yellow", "value": 90},
                {"color": "green", "value": 95}
              ]
            }
          }
        }
      },
      {
        "title": "Instance Sync Status",
        "type": "bargauge",
        "targets": [
          {
            "expr": "unleasherator_releasechannel_instances_up_to_date",
            "legendFormat": "{{ name }} - Up to Date"
          },
          {
            "expr": "unleasherator_releasechannel_instances_total - unleasherator_releasechannel_instances_up_to_date",
            "legendFormat": "{{ name }} - Outdated"
          }
        ]
      }
    ]
  }
}
```

## Troubleshooting Guide

### Common Issues and Resolutions

#### 1. ReleaseChannel Rollouts Failing

**Symptoms:**
- High failure rate in `unleasherator_releasechannel_rollouts_total{result="failed"}`
- Instances stuck in pending state
- Timeout errors in logs

**Investigation Steps:**
```bash
# Check ReleaseChannel status
kubectl describe releasechannel <name> -n <namespace>

# Check Unleash instance events
kubectl get events --field-selector involvedObject.kind=Unleash -n <namespace>

# Check controller logs
kubectl logs -n unleasherator-system deployment/unleasherator-controller-manager -c manager | grep releasechannel
```

**Common Causes:**
- Image pull failures
- Insufficient resources
- Network connectivity issues
- RBAC permission problems

#### 2. High Resource Conflicts

**Symptoms:**
- Increasing `unleasherator_releasechannel_conflicts_total`
- Frequent retry messages in logs
- Slow reconciliation

**Investigation Steps:**
```bash
# Check for concurrent operations
kubectl get events --field-selector reason=FailedUpdate -n <namespace>

# Monitor reconciliation frequency
kubectl logs -n unleasherator-system deployment/unleasherator-controller-manager | grep "conflict"
```

**Solutions:**
- Reduce reconciliation frequency
- Implement backoff strategies
- Check for external tools modifying resources

#### 3. Controller Performance Issues

**Symptoms:**
- High `controller_runtime_reconcile_time_seconds`
- Growing work queue depth
- Memory/CPU saturation

**Investigation Steps:**
```bash
# Check controller resource usage
kubectl top pod -n unleasherator-system

# Analyze reconciliation patterns
kubectl logs -n unleasherator-system deployment/unleasherator-controller-manager | grep "reconcile_duration"
```

**Solutions:**
- Increase controller resource limits
- Optimize reconciliation logic
- Implement rate limiting

### Diagnostic Commands

#### Health Check Script

```bash
#!/bin/bash
# unleasherator-health-check.sh

echo "=== Unleasherator Health Check ==="

# Controller pod status
echo "Controller Status:"
kubectl get pods -n unleasherator-system -l app.kubernetes.io/name=unleasherator

# Metrics endpoint
echo -e "\nMetrics Endpoint:"
kubectl port-forward -n unleasherator-system svc/unleasherator-controller-manager-metrics-service 8080:8443 &
PF_PID=$!
sleep 2
curl -k https://localhost:8080/metrics | grep unleasherator | head -5
kill $PF_PID

# Recent errors
echo -e "\nRecent Errors:"
kubectl logs -n unleasherator-system deployment/unleasherator-controller-manager --tail=50 | grep ERROR

# CRD status
echo -e "\nCRD Status:"
kubectl get crd | grep unleash

echo -e "\n=== Health Check Complete ==="
```

## Capacity Planning

### Resource Requirements

#### Controller Resources

**Minimum:**
```yaml
resources:
  requests:
    cpu: 100m
    memory: 128Mi
  limits:
    cpu: 500m
    memory: 512Mi
```

**Production (>100 Unleash instances):**
```yaml
resources:
  requests:
    cpu: 500m
    memory: 512Mi
  limits:
    cpu: 1000m
    memory: 1Gi
```

#### Scaling Guidelines

| Unleash Instances | Controller CPU | Controller Memory | Reconciliation Rate |
| ----------------- | -------------- | ----------------- | ------------------- |
| 1-10              | 100m           | 128Mi             | ~1/sec              |
| 11-50             | 200m           | 256Mi             | ~5/sec              |
| 51-100            | 500m           | 512Mi             | ~10/sec             |
| 100+              | 1000m+         | 1Gi+              | ~20/sec             |

### Performance Tuning

#### Controller Configuration

```yaml
apiVersion: controller-runtime.sigs.k8s.io/v1alpha1
kind: ControllerManagerConfig
metadata:
  name: manager-config
spec:
  # Adjust based on load
  leaderElection:
    leaderElect: true
    leaseDuration: 15s
    renewDeadline: 10s
    retryPeriod: 2s

  # Controller-specific settings
  controller:
    groupKindConcurrency:
      unleash.nais.io/Unleash: 10
      unleash.nais.io/ReleaseChannel: 5
      unleash.nais.io/ApiToken: 5
```

## Security Considerations

### Metrics Security

1. **Secure metrics endpoints** with TLS and authentication
2. **Limit metric access** to monitoring systems only
3. **Audit metrics collection** for compliance requirements
4. **Sanitize sensitive labels** to prevent information leakage

### Log Security

1. **Avoid logging sensitive data** (tokens, passwords)
2. **Implement log rotation** and retention policies
3. **Secure log transport** with TLS encryption
4. **Control log access** with RBAC

## Operational Runbooks

### Incident Response

#### Severity Levels

**Critical (P1):**
- Controller completely down
- All ReleaseChannels failing
- Data loss scenarios

**High (P2):**
- Single ReleaseChannel failing
- Performance degradation
- Security alerts

**Medium (P3):**
- Intermittent issues
- Capacity warnings
- Non-critical features affected

#### Response Procedures

1. **Immediate Assessment** (2 minutes)
   - Check controller pod status
   - Review active alerts
   - Identify blast radius

2. **Mitigation** (5 minutes)
   - Scale controller if resource constrained
   - Restart controller if hung
   - Disable failing ReleaseChannels if needed

3. **Investigation** (15 minutes)
   - Analyze logs and metrics
   - Check external dependencies
   - Identify root cause

4. **Resolution** (varies)
   - Apply fixes
   - Verify resolution
   - Update documentation

### Maintenance Procedures

#### Regular Health Checks

**Daily:**
- Review dashboard alerts
- Check error rates
- Verify backup status

**Weekly:**
- Analyze performance trends
- Review capacity utilization
- Update operational documentation

**Monthly:**
- Conduct disaster recovery tests
- Review and update alerting rules
- Optimize resource allocation

## Best Practices Summary

### Monitoring Best Practices

1. **Layer your monitoring** - Use metrics, logs, and traces
2. **Alert on symptoms** not just causes
3. **Set meaningful SLOs** for your use cases
4. **Practice alert fatigue management**
5. **Regularly review and tune** alerting rules

### Operational Best Practices

1. **Automate common operations** where possible
2. **Document all procedures** and keep them current
3. **Test disaster recovery** scenarios regularly
4. **Use GitOps** for configuration management
5. **Implement proper RBAC** and security controls
6. **Monitor the monitors** - ensure observability system health

### Performance Best Practices

1. **Right-size resources** based on actual usage
2. **Use horizontal scaling** when appropriate
3. **Implement circuit breakers** for external dependencies
4. **Optimize reconciliation frequency** based on requirements
5. **Monitor and optimize** database performance for Unleash

This comprehensive observability guide provides the foundation for running Unleasherator reliably in production. Regular review and updates of these practices will ensure continued operational excellence.
