# ReleaseChannel

The ReleaseChannel feature provides a GitOps-friendly way to manage and coordinate container image upgrades across multiple Unleash instances in your Kubernetes cluster. It offers centralized image management with built-in safety mechanisms that protect against accidental configuration overwrites.

## Overview

ReleaseChannels solve the operational challenge of keeping multiple Unleash instances up-to-date with the latest container images. Instead of manually updating each instance individually, you can:

- **Define target images centrally** through ReleaseChannel resources
- **Automatically roll out updates** to connected Unleash instances
- **Maintain user control** with manual override capabilities
- **Ensure operational safety** with kubectl apply protection
- **Monitor rollout progress** through comprehensive status reporting

## How It Works

### Architecture Overview

ReleaseChannels use a hybrid approach combining Kubernetes spec fields with status subresources to ensure both functionality and safety:

1. **ReleaseChannel Definition** - Centrally defines the target container image
2. **Unleash Instance Connection** - Unleash instances reference ReleaseChannels by name
3. **Automatic Adoption** - ReleaseChannel controller manages connected instances
4. **Status Tracking** - Status subresources provide kubectl apply safety
5. **Priority System** - Clear precedence rules for image resolution

### Image Resolution Priority

When determining which container image to use, the system follows this priority order:

1. **Manual Override** (`spec.customImage`) - Highest priority for user overrides
2. **ReleaseChannel Image** (from status) - Controller-managed automatic updates
3. **Environment Variable** (`UNLEASH_IMAGE`) - Environment defaults
4. **Default Image** - Built-in fallback image

This priority system ensures users always have control while benefiting from automated management.

## Getting Started

### Step 1: Create a ReleaseChannel

Create a ReleaseChannel to define your target image:

```yaml
apiVersion: unleash.nais.io/v1
kind: ReleaseChannel
metadata:
  name: stable
  namespace: unleash-system
spec:
  image: "quay.io/unleash/unleash-server:6.3.0"
```

### Step 2: Connect Unleash Instances

Configure your Unleash instances to use the ReleaseChannel:

```yaml
apiVersion: unleash.nais.io/v1
kind: Unleash
metadata:
  name: my-unleash
  namespace: unleash-system
spec:
  releaseChannel:
    name: stable
  database:
    url: "postgres://unleash:unleash@postgres:5432/unleash"
```

### Step 3: Deploy and Verify

Apply both resources and verify the connection:

```bash
kubectl apply -f releasechannel.yaml
kubectl apply -f unleash.yaml

# Check ReleaseChannel status
kubectl get releasechannel stable

# Verify Unleash is using the correct image
kubectl describe unleash my-unleash
```

## Common Usage Patterns

### Coordinated Upgrades

Update multiple Unleash instances simultaneously by updating the ReleaseChannel:

```yaml
# Update the ReleaseChannel
apiVersion: unleash.nais.io/v1
kind: ReleaseChannel
metadata:
  name: stable
spec:
  image: "quay.io/unleash/unleash-server:6.4.0"  # New version
```

All connected Unleash instances will automatically update to the new image.

### Environment-Specific Channels

Use different ReleaseChannels for different environments:

```yaml
# Production channel - stable releases
apiVersion: unleash.nais.io/v1
kind: ReleaseChannel
metadata:
  name: production
spec:
  image: "quay.io/unleash/unleash-server:6.3.0"
---
# Staging channel - latest releases
apiVersion: unleash.nais.io/v1
kind: ReleaseChannel
metadata:
  name: staging
spec:
  image: "quay.io/unleash/unleash-server:6.4.0-beta.1"
```

### Manual Overrides

Temporarily override the ReleaseChannel image for specific instances:

```yaml
apiVersion: unleash.nais.io/v1
kind: Unleash
metadata:
  name: debug-unleash
spec:
  customImage: "my-registry/unleash:debug"  # Overrides ReleaseChannel
  releaseChannel:
    name: stable  # Still tracked for future updates
```

The manual override takes precedence, but the ReleaseChannel connection is preserved for when you remove the override.

## kubectl Apply Safety

One of the key benefits of ReleaseChannels is protection against accidental configuration loss during `kubectl apply` operations.

### The Problem

Without ReleaseChannels, if you accidentally run `kubectl apply` with incomplete YAML that omits the `customImage` field, your Unleash instance might revert to a default image, causing unexpected downgrades.

### The Solution

ReleaseChannels use Kubernetes status subresources to track the intended image separately from user-managed configuration:

```yaml
# User applies incomplete YAML (missing customImage)
apiVersion: unleash.nais.io/v1
kind: Unleash
metadata:
  name: my-unleash
spec:
  releaseChannel:
    name: stable
  # customImage accidentally omitted
```

**What happens:**

1. `customImage` gets cleared by kubectl apply
2. Status fields survive (status subresource protection)
3. System falls back to ReleaseChannel image from status
4. Service continues running with correct image
5. ReleaseChannel controller automatically repairs the configuration

### Verification

You can verify the safety mechanism by checking both spec and status:

```bash
kubectl get unleash my-unleash -o yaml
```

```yaml
spec:
  customImage: "quay.io/unleash/unleash-server:6.3.0"  # Managed by ReleaseChannel
  releaseChannel:
    name: "stable"
status:
  resolvedReleaseChannelImage: "quay.io/unleash/unleash-server:6.3.0"  # Safety backup
  releaseChannelName: "stable"  # Validation reference
```

## Monitoring and Status

### ReleaseChannel Status

Check the overall status of a ReleaseChannel:

```bash
kubectl get releasechannel stable -o yaml
```

```yaml
status:
  conditions:
  - type: Reconciled
    status: "True"
    reason: ReconcileSuccess
    message: ReleaseChannel reconciled successfully
  instances: 3
  instancesUpToDate: 3
  completed: true
  lastReconcileTime: "2025-08-05T10:30:00Z"
```

### Status Fields Explained

- **instances**: Total Unleash instances managed by this ReleaseChannel
- **instancesUpToDate**: Number of instances running the target image
- **completed**: Whether all instances are up-to-date
- **lastReconcileTime**: When the ReleaseChannel was last processed

### Monitoring Commands

```bash
# Overview of all ReleaseChannels
kubectl get releasechannels

# Detailed status of a specific ReleaseChannel
kubectl describe releasechannel stable

# Check which Unleash instances are connected
kubectl get unleash -o custom-columns=NAME:.metadata.name,CHANNEL:.spec.releaseChannel.name,IMAGE:.spec.customImage

# Monitor rollout progress
kubectl get releasechannel stable -w
```

## Advanced Configuration

### Multiple ReleaseChannels

Organize your deployments with multiple channels:

```yaml
# Stable production releases
apiVersion: unleash.nais.io/v1
kind: ReleaseChannel
metadata:
  name: stable
spec:
  image: "quay.io/unleash/unleash-server:6.3.0"
---
# Beta testing releases
apiVersion: unleash.nais.io/v1
kind: ReleaseChannel
metadata:
  name: beta
spec:
  image: "quay.io/unleash/unleash-server:6.4.0-beta.1"
---
# Development releases
apiVersion: unleash.nais.io/v1
kind: ReleaseChannel
metadata:
  name: development
spec:
  image: "quay.io/unleash/unleash-server:main"
```

### Gradual Rollouts

Implement gradual rollouts by updating ReleaseChannels in stages:

1. **Update beta channel first**

```bash
kubectl patch releasechannel beta --type='merge' -p='{\"spec\":{\"image\":\"quay.io/unleash/unleash-server:6.4.0\"}}'
```

2. **Verify beta instances are healthy**

```bash
kubectl get unleash -l releaseChannel=beta
```

3. **Promote to stable channel**

```bash
kubectl patch releasechannel stable --type='merge' -p='{\"spec\":{\"image\":\"quay.io/unleash/unleash-server:6.4.0\"}}'
```

## Troubleshooting

### Common Issues

#### Instances Not Updating

**Symptoms:** ReleaseChannel updated but Unleash instances still running old image

**Solutions:**

1. Verify the ReleaseChannel name matches exactly:

   ```bash
   kubectl get unleash my-unleash -o jsonpath='{.spec.releaseChannel.name}'
   kubectl get releasechannel
   ```

2. Check if manual override is present:

   ```bash
   kubectl get unleash my-unleash -o jsonpath='{.spec.customImage}'
   ```

3. Verify namespace alignment:

   ```bash
   kubectl get releasechannel -n <namespace>
   kubectl get unleash -n <namespace>
   ```

#### Status Not Updating

**Symptoms:** Status fields show outdated information

**Solutions:**

1. Check controller logs:

   ```bash
   kubectl logs -n unleasherator-system deployment/unleasherator-controller-manager -c manager | grep releasechannel
   ```

2. Force reconciliation by adding a label:

   ```bash
   kubectl label releasechannel stable reconcile=$(date +%s)
   ```

#### Rollout Stuck

**Symptoms:** ReleaseChannel shows partial completion

**Solutions:**

1. Check individual Unleash instance status:

   ```bash
   kubectl describe unleash <instance-name>
   ```

2. Look for resource conflicts:

   ```bash
   kubectl get events --field-selector involvedObject.kind=Unleash
   ```

### Debugging Commands

```bash
# Check ReleaseChannel controller logs
kubectl logs -n unleasherator-system -l app.kubernetes.io/name=unleasherator -c manager | grep releasechannel

# Check Unleash controller logs
kubectl logs -n unleasherator-system -l app.kubernetes.io/name=unleasherator -c manager | grep unleash

# View recent events
kubectl get events --sort-by='.lastTimestamp' | grep -E '(ReleaseChannel|Unleash)'

# Check resource conflicts
kubectl get events --field-selector reason=FailedUpdate
```

## Best Practices

### Naming Conventions

Use descriptive, environment-specific names:

- `production` or `prod` - Stable production releases
- `staging` or `stage` - Pre-production testing
- `development` or `dev` - Latest development builds
- `beta` - Beta testing releases
- `canary` - Canary deployment testing

### Image Management

1. **Use semantic versioning** in your images: `6.3.0`, `6.4.0-beta.1`
2. **Avoid `latest` tags** for production ReleaseChannels
3. **Test in lower environments** before promoting to production
4. **Keep a rollback plan** by maintaining previous ReleaseChannel definitions

### Operational Guidelines

1. **Monitor rollouts** using status fields and events
2. **Use gradual promotion** through multiple ReleaseChannels
3. **Maintain documentation** of your ReleaseChannel strategy
4. **Implement GitOps workflows** for ReleaseChannel management
5. **Test kubectl apply scenarios** in development environments

### Security Considerations

1. **Use private registries** for production images
2. **Implement image scanning** in your CI/CD pipeline
3. **Use signed images** where possible
4. **Restrict ReleaseChannel permissions** via RBAC
5. **Audit ReleaseChannel changes** through Git history

## Integration with GitOps

ReleaseChannels work excellently with GitOps workflows:

### Repository Structure

```
k8s/
├── base/
│   ├── releasechannel-stable.yaml
│   ├── releasechannel-beta.yaml
│   └── unleash-instances.yaml
├── environments/
│   ├── production/
│   │   └── kustomization.yaml
│   └── staging/
│       └── kustomization.yaml
```

### Automated Updates

Use tools like Renovate or Dependabot to automatically update ReleaseChannel images:

```yaml
# .github/renovate.json
{
  "kubernetes": {
    "fileMatch": ["k8s/.*\.yaml$"],
    "ignorePaths": ["k8s/environments/"]
  }
}
```

## Migration Guide

### From Manual Image Management

If you're currently managing Unleash images manually:

1. **Create ReleaseChannels** for your current image versions
2. **Update Unleash instances** to reference ReleaseChannels
3. **Verify connectivity** and status reporting
4. **Remove manual `customImage` fields** (optional)
5. **Update operational procedures** to use ReleaseChannels

### Rollback Procedure

If you need to rollback ReleaseChannel adoption:

1. **Set manual overrides** on all affected Unleash instances
2. **Remove ReleaseChannel references** from Unleash specs
3. **Delete ReleaseChannel resources** if no longer needed

The status subresource design ensures this process is safe and non-disruptive.

## Conclusion

ReleaseChannels provide a robust, safe, and user-friendly way to manage Unleash container images across your Kubernetes infrastructure. The combination of centralized management, kubectl apply safety, and flexible override capabilities makes them ideal for production environments where both automation and operational control are essential.

By leveraging ReleaseChannels, you can:

- Reduce operational overhead of image management
- Minimize risk of configuration errors
- Implement consistent deployment patterns
- Maintain full control when manual intervention is needed
- Scale your Unleash deployments with confidence

## Architecture

```mermaid
graph TD
    A[ReleaseChannel CRD] --> B[Controller]
    B --> C[Discovers Unleash Instances]
    C --> D{IsCandidate?}
    D -->|Yes| E{ShouldUpdate?}
    D -->|No| F[Skip]
    E -->|Yes| G[Update Instance]
    E -->|No| H[Already Up-to-Date]
    G --> I[Wait for Ready]
    I --> J[Update Status]
```

## Key Features

### 1. **Automatic Instance Discovery**

- Finds Unleash instances that reference the ReleaseChannel by name
- Uses `IsCandidate()` method to determine eligibility
- Works within namespace boundaries

### 2. **Intelligent Update Logic**

- Only updates instances that need it via `ShouldUpdate()` method
- Compares current `CustomImage` with ReleaseChannel target image
- Skips instances already running the target version

### 3. **Conflict Resolution**

- Implements retry logic with exponential backoff
- Handles Kubernetes optimistic concurrency control conflicts
- Up to 3 retry attempts with increasing delays

### 4. **Phase-Based Management**

- Idle: Checking for instances to update
- Completed: All instances updated successfully
- Failed: Rollout encountered errors

### 5. **Rich Status Reporting**

- Total instances managed
- Instances up-to-date
- Progress percentage
- Last reconciliation time
- Kubernetes conditions for detailed status

## Current Implementation Status

✅ **Implemented Features:**

- Basic image rollout to matching Unleash instances
- Conflict resolution with retry logic
- Phase-based status management
- Instance discovery and filtering
- Status reporting and progress tracking

🚧 **Planned Features (CRD Ready):**

- Canary deployment strategy
- Health checks after deployment
- Batch interval controls
- Automatic rollback capabilities
- Parallel deployment limits

## Configuration

### Basic ReleaseChannel

```yaml
apiVersion: unleash.nais.io/v1
kind: ReleaseChannel
metadata:
  name: unleash-stable
  namespace: unleash-system
spec:
  image: "unleashorg/unleash-server:5.7.0"
```

### Advanced ReleaseChannel with Canary

```yaml
apiVersion: unleash.nais.io/v1
kind: ReleaseChannel
metadata:
  name: unleash-beta
  namespace: unleash-system
spec:
  image: "unleashorg/unleash-server:5.8.0-beta.1"
  strategy:
    maxParallel: 3
    canary:
      enabled: true
      podSelector:
        matchLabels:
          deployment: "canary"
```

### Unleash Instance Configuration

```yaml
apiVersion: unleash.nais.io/v1
kind: Unleash
metadata:
  name: unleash-production
  namespace: unleash-system
  labels:
    deployment: "production"
spec:
  database:
    url: "postgres://unleash-db:5432/unleash"
  # Connect to ReleaseChannel
  releaseChannel:
    name: unleash-stable
```

## Status Monitoring

The ReleaseChannel provides comprehensive status information:

```yaml
status:
  conditions:
  - type: Reconciled
    status: "True"
    reason: ReconcileSuccess
    message: ReleaseChannel reconciled successfully
  instances: 5
  instancesUpToDate: 5
  canaryInstances: 2
  canaryInstancesUpToDate: 2
  completed: true
  lastReconcileTime: "2025-08-05T10:30:00Z"
  version: "unknown"
```

### Status Fields

- **instances**: Total number of Unleash instances managed by this ReleaseChannel
- **instancesUpToDate**: Number of instances running the target image
- **canaryInstances**: Number of instances identified as canary instances
- **canaryInstancesUpToDate**: Number of canary instances running the target image
- **completed**: Boolean indicating if all instances are up-to-date
- **lastReconcileTime**: Timestamp of last reconciliation

### kubectl Commands

Monitor ReleaseChannel status:

```bash
kubectl get releasechannels
kubectl describe releasechannel unleash-stable
```

View detailed status:

```bash
kubectl get releasechannel unleash-stable -o yaml
```

## Deployment Strategies

### 1. Sequential Deployment (Default)

```yaml
spec:
  strategy:
    maxParallel: 1  # Default
```

### 2. Parallel Deployment

```yaml
spec:
  strategy:
    maxParallel: 5  # Deploy to 5 instances simultaneously
```

### 3. Canary-First Deployment

```yaml
spec:
  strategy:
    maxParallel: 2
    canary:
      enabled: true
      podSelector:
        matchLabels:
          tier: "canary"
```

## Integration Points

### 1. **Unleash Controller Integration**

- ReleaseChannel updates `CustomImage` field
- Unleash controller handles the actual deployment
- Waits for Unleash instance to become ready

### 2. **RBAC Permissions**

- Read/write ReleaseChannel resources
- Read/write Unleash resources
- Update status subresources

### 3. **Event Recording**

- Success/failure events for each instance update
- Canary-specific events
- Integration with Kubernetes event system

## Troubleshooting

### Common Issues

1. **Instances not updating**
   - Check if `releaseChannel.name` matches in Unleash spec
   - Verify namespace alignment
   - Check RBAC permissions

2. **Canary deployment not working**
   - Verify label selector matches canary instance labels
   - Ensure `canary.enabled: true`
   - Check instance labeling

3. **Slow rollouts**
   - Increase `maxParallel` for faster deployments
   - Check individual Unleash instance readiness
   - Monitor resource constraints

### Debugging Commands

```bash
# Check ReleaseChannel logs
kubectl logs -l app.kubernetes.io/name=unleasherator -c manager

# Check events
kubectl get events --field-selector involvedObject.kind=ReleaseChannel

# Check Unleash instance status
kubectl get unleash -o wide
```

## Monitoring and Metrics

The ReleaseChannel controller exposes comprehensive Prometheus metrics for monitoring rollout performance, success rates, and operational health. These metrics help you:

- Track rollout success and failure rates
- Monitor rollout duration and performance
- Identify instances that are out of sync
- Alert on problematic conditions
- Understand ReleaseChannel adoption and usage patterns

### Available Metrics

The following metrics are exposed for ReleaseChannel operations:

#### Core Status Metrics

- **`unleasherator_releasechannel_status`** (Gauge)
  Current status of ReleaseChannels (1=active/completed, 0.5=in-progress, 0=failed)
  Labels: `namespace`, `name`

- **`unleasherator_releasechannel_instances_total`** (Gauge)
  Total number of Unleash instances managed by each ReleaseChannel
  Labels: `namespace`, `name`

- **`unleasherator_releasechannel_instances_up_to_date`** (Gauge)
  Number of instances currently running the target image
  Labels: `namespace`, `name`

#### Rollout Performance Metrics

- **`unleasherator_releasechannel_rollouts_total`** (Counter)
  Total number of rollout events with success/failure tracking
  Labels: `namespace`, `name`, `result` (success/failed)

- **`unleasherator_releasechannel_rollout_duration_seconds`** (Histogram)
  Duration of complete rollouts from start to finish
  Labels: `namespace`, `name`

- **`unleasherator_releasechannel_instance_updates_total`** (Counter)
  Individual instance update attempts and their outcomes
  Labels: `namespace`, `name`, `result` (success/failed)

- **`unleasherator_releasechannel_conflicts_total`** (Counter)
  Resource conflicts encountered during updates (helps identify contention)
  Labels: `namespace`, `name`

### Quick Monitoring Queries

Here are some useful Prometheus queries for monitoring ReleaseChannels:

```promql
# Instances out of sync
unleasherator_releasechannel_instances_total - unleasherator_releasechannel_instances_up_to_date

# Rollout success rate (last 24h)
rate(unleasherator_releasechannel_rollouts_total{result="success"}[24h]) /
rate(unleasherator_releasechannel_rollouts_total[24h]) * 100

# Average rollout duration
rate(unleasherator_releasechannel_rollout_duration_seconds_sum[5m]) /
rate(unleasherator_releasechannel_rollout_duration_seconds_count[5m])

# High conflict rate (may indicate resource contention)
rate(unleasherator_releasechannel_conflicts_total[5m]) > 0.1
```

### Basic Alerting Rules

Consider setting up alerts for these conditions:

```yaml
# High failure rate
- alert: ReleaseChannelHighFailureRate
  expr: |
    rate(unleasherator_releasechannel_rollouts_total{result="failed"}[1h]) /
    rate(unleasherator_releasechannel_rollouts_total[1h]) > 0.1
  for: 5m

# Instances stuck out of sync
- alert: ReleaseChannelInstancesOutOfSync
  expr: |
    (unleasherator_releasechannel_instances_total -
     unleasherator_releasechannel_instances_up_to_date) > 0
  for: 30m

# Slow rollouts
- alert: ReleaseChannelSlowRollout
  expr: |
    histogram_quantile(0.95,
      rate(unleasherator_releasechannel_rollout_duration_seconds_bucket[5m])
    ) > 300
  for: 10m
```

For comprehensive observability guidance, monitoring setup, and advanced troubleshooting techniques for the entire Unleasherator controller, see the [Observability Guide](observability.md).

## Best Practices

1. **Use descriptive names** for ReleaseChannels (e.g., `unleash-stable`, `unleash-canary`)

2. **Start with canary deployments** for production environments

3. **Monitor rollout progress** using status fields and events

4. **Use appropriate `maxParallel`** settings based on cluster capacity

5. **Test ReleaseChannels** in development environments first

6. **Use GitOps workflows** to manage ReleaseChannel definitions

7. **Set up monitoring and alerts** using the provided Prometheus metrics

8. **Monitor resource conflicts** to identify optimization opportunities

## Future Enhancements

Potential improvements could include:

- **Health checks**: Verify Unleash instances are healthy before proceeding
- **Rollback capabilities**: Automatic rollback on deployment failures
- **Advanced strategies**: Blue/green deployments, percentage-based rollouts
- **Cross-namespace support**: Manage instances across multiple namespaces
- **Integration hooks**: Webhooks for external notifications
- **Metrics integration**: Prometheus metrics for rollout monitoring

## Security Considerations

- ReleaseChannels operate within namespace boundaries
- RBAC controls access to ReleaseChannel operations
- Image validation should be handled by admission controllers
- Consider using signed images for production deployments
