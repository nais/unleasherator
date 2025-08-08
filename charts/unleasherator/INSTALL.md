# Unleasherator Installation Guide

## Prerequisites

- Kubernetes 1.20+
- Helm 3.8+

## Installation

### Basic Installation

```bash
# Install CRDs first
helm install unleasherator-crds ./charts/unleasherator-crds

# Install the operator
helm install unleasherator ./charts/unleasherator
```

### Installation Without Prometheus Operator

If your cluster doesn't have Prometheus Operator installed, disable ServiceMonitor creation:

```bash
# Install CRDs first
helm install unleasherator-crds ./charts/unleasherator-crds

# Install without ServiceMonitor
helm install unleasherator ./charts/unleasherator \
  --set monitoring.serviceMonitor.enabled=false
```

### Installation with Custom Prometheus Configuration

```bash
# Install with custom Prometheus settings
helm install unleasherator ./charts/unleasherator \
  --set monitoring.serviceMonitor.enabled=true \
  --set monitoring.serviceMonitor.interval=30s \
  --set monitoring.serviceMonitor.additionalLabels.release=prometheus
```

## Configuration

### Monitoring Configuration

| Parameter                                    | Description                          | Default |
| -------------------------------------------- | ------------------------------------ | ------- |
| `monitoring.serviceMonitor.enabled`          | Enable ServiceMonitor creation       | `true`  |
| `monitoring.serviceMonitor.additionalLabels` | Additional labels for ServiceMonitor | `{}`    |
| `monitoring.serviceMonitor.interval`         | Scrape interval override             | `""`    |
| `monitoring.serviceMonitor.scrapeTimeout`    | Scrape timeout override              | `""`    |

### Common Issues

#### ServiceMonitor CRD Not Found

**Error:**

```text
error: resource mapping not found for name: "unleasherator-controller-manager-metrics-monitor"
namespace: "unleasherator-system" from "STDIN": no matches for kind "ServiceMonitor"
in version "monitoring.coreos.com/v1"
```

**Solution:** Disable ServiceMonitor creation:

```bash
helm upgrade unleasherator ./charts/unleasherator \
  --set monitoring.serviceMonitor.enabled=false
```

Or install Prometheus Operator first:

```bash
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm install prometheus-operator-crds prometheus-community/prometheus-operator-crds
```

## Uninstallation

```bash
# Remove the operator
helm uninstall unleasherator

# Remove CRDs (this will delete all Unleash resources!)
helm uninstall unleasherator-crds
```
