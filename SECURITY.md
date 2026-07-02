# Unleasherator Security Model

This document outlines the security architecture, threat model, and defense mechanisms for the Unleasherator Kubernetes operator.

## Architecture & Component Security Boundaries

Unleasherator operates across two primary security boundaries:

1.  **Management Cluster (Publisher):** Where `Unleash` instances are provisioned and managed. This environment is highly trusted and restricted to cluster administrators.
2.  **Tenant Clusters (Subscriber):** Where `RemoteUnleash` instances are created to consume Unleash API tokens. Tenants have restricted privileges (usually bound to their specific `Namespace`), but they can freely create resources within their namespaces.

### Component Breakdown

*   **`Unleash` (CRD):** Created exclusively by cluster admins on the management cluster. This resource tells Unleasherator to provision an actual Unleash server. The operator generates a highly privileged Admin API token for this instance.
*   **Federation (Pub/Sub):** The management cluster publishes the Unleash instance details, including the raw Admin API token, to a Google Cloud Pub/Sub topic. This channel must be secured via IAM to ensure only authorized tenant clusters can subscribe and receive the payload.
*   **`RemoteUnleash` (CRD):** Created by tenants on tenant clusters. This resource requests access to an Unleash instance. The operator intercepts this, validates the request, and provisions tenant-specific tokens.
*   **`ApiToken` (CRD):** Created by tenants to request a specific client/frontend token for their workloads. The operator uses the highly privileged Admin token (acquired via `RemoteUnleash`) to dynamically generate these scoped, less-privileged tokens directly in the Unleash server, and then writes them to a local Kubernetes `Secret` for the tenant's pods to mount.

### The Core Challenge: Secret Distribution
The management cluster must distribute highly sensitive Unleash API Admin tokens to the tenant clusters so that tenant workloads can evaluate feature flags.
Because tenants cannot cross namespaces to read secrets, the token must be provided in a way that is accessible to the `RemoteUnleash` controller, without exposing the raw token to malicious tenants.

## Threat Modeling (STRIDE)

We evaluate the system against the STRIDE threat model to identify potential vulnerabilities.

| Threat | Description in Unleasherator Context | Mitigation |
| :--- | :--- | :--- |
| **Spoofing** | A tenant spoofs a `RemoteUnleash` resource to impersonate another tenant or claim ownership of a shared Unleash instance. | The system relies on Kubernetes RBAC for identity. The operator only trusts the explicit `Namespace` of the requesting `RemoteUnleash` object. |
| **Tampering** | A tenant alters the `adminSecret` reference in their `RemoteUnleash` to point to a secret they do not own. | **Strict Secret Naming Validation:** The operator strictly enforces that the referenced secret name cryptographically binds to the tenant's namespace. |
| **Repudiation** | A tenant denies creating a malicious `RemoteUnleash`. | Kubernetes Audit Logs provide non-repudiation for all API server interactions. |
| **Information Disclosure** | A tenant reads the raw Unleash Admin API token intended for another tenant (or even their own token, if they shouldn't have raw admin access). | **Operator Namespace Isolation:** Tokens are stored exclusively in the `nais-system` (operator) namespace. Tenants cannot read these secrets. The controller reads them on their behalf. |
| **Denial of Service** | A tenant provisions thousands of `RemoteUnleash` instances to overwhelm the operator or the Unleash API. | Kubernetes ResourceQuotas and rate-limiting on the Unleash API protect against DoS. |
| **Elevation of Privilege (Confused Deputy)** | A tenant tricks the operator into using its elevated privileges to read an arbitrary secret (or another tenant's token) from `nais-system` and applying it to their own `RemoteUnleash`. | **Cryptographic/Namespace Binding:** The operator validates that the secret name explicitly contains the tenant's namespace and the Unleash instance name, making it impossible for Tenant A to guess or legally request Tenant B's secret. |

## The "Confused Deputy" Attack Vector

A Confused Deputy attack occurs when a lower-privileged entity (a tenant) tricks a higher-privileged entity (the Unleasherator controller) into performing an action on its behalf (fetching a secret from `nais-system`).

### The Vulnerability
If the controller blindly accepts any secret name in the `nais-system` namespace:
1. Tenant A (malicious) wants to steal access to Tenant B's Unleash instance.
2. Tenant A knows that Tenant B's secret is stored as `unleasherator-tenant-b-secret` in `nais-system`.
3. Tenant A creates a `RemoteUnleash` in their own namespace, setting `spec.adminSecret.name = unleasherator-tenant-b-secret` and `spec.adminSecret.namespace = nais-system`.
4. The controller (running with cluster-admin privileges) fetches the secret and provisions an API token for Tenant A using Tenant B's admin credentials.

### The Solution: Namespace-Bound Secret Names

To eliminate this vulnerability, the system enforces a strict naming convention for all cross-namespace secrets. 

**The Ideal Secret Name Format:**
`unleasherator-<unleash-name>-<tenant-namespace>-admin-key`

**Why this format?**
1. **`unleasherator-`**: Standard prefix for easy identification.
2. **`<unleash-name>`**: Prevents collisions if a single tenant consumes multiple different Unleash instances.
3. **`<tenant-namespace>`**: **The Security Boundary.** By embedding the requesting tenant's namespace into the expected secret name, the controller forces a cryptographic lock. 
4. **`-admin-key`**: A descriptive suffix.

### Validation Logic
When a `RemoteUnleash` (created in namespace `Tenant-A`) requests a cross-namespace secret from `nais-system`, the controller enforces:

```go
expectedName := fmt.Sprintf("unleasherator-%s-%s-admin-key", remoteUnleash.Name, remoteUnleash.Namespace)
if requestedSecretName != expectedName {
    return Error("Validation failed: You can only request secrets bound to your namespace")
}
```

If Tenant A attempts to request `unleasherator-my-unleash-Tenant-B-admin-key`, the controller calculates `expectedName` as `unleasherator-my-unleash-Tenant-A-admin-key`. Since they do not match, the request is violently rejected. Tenant A is mathematically restricted to requesting secrets that contain their own namespace.

## Authorization Flow & Preventing Unauthorized Access

A common question is: *"What stops Tenant A from simply guessing the Unleash instance name, deploying a `RemoteUnleash` in their namespace, and successfully requesting `unleasherator-my-unleash-Tenant-A-admin-key-abc123`?"*

The answer lies in the **Separation of Concerns** between the Management Cluster and the Tenant Cluster:

1. **The Root of Trust (Management Cluster):** 
   When a Cluster Admin provisions an `Unleash` instance (`my-unleash`), they explicitly configure `spec.federation.namespaces = ["Tenant-B"]`. Tenants have zero access to modify this.
2. **The Pub/Sub Payload:**
   The Publisher broadcasts this authorization state. The payload explicitly states that `my-unleash` is only authorized for `Tenant-B`.
3. **The Subscriber (Tenant Cluster):**
   The Subscriber receives the payload and physically provisions secrets in `nais-system`. 
   Because only `Tenant-B` was authorized, it creates **ONLY ONE** secret: `unleasherator-my-unleash-Tenant-B-admin-key-abc123`.
   **Crucially, it DOES NOT create `unleasherator-my-unleash-Tenant-A-admin-key-abc123`.**
4. **The Attack Fails (404 Not Found):**
   When malicious Tenant A creates their `RemoteUnleash` and correctly predicts the namespace-bound secret name for their own namespace, the request passes the initial naming validation. However, when the controller attempts to fetch that secret from `nais-system`, the Kubernetes API returns a `404 Not Found`.

**Conclusion:** A tenant can only successfully provision API tokens if a Cluster Admin has explicitly authorized their namespace on the central Management Cluster.

## Legacy Formats, The "Nonce", and Migration

### The Purpose of the Legacy Nonce
In the old federation model, the subscriber generated cross-namespace secrets named `unleasherator-<name>-<nonce>` (e.g., `unleasherator-my-unleash-a3f9b2`).
The **nonce** acted as a shared password/secret distributed out-of-band to authorized tenants. Because a malicious tenant could not guess the random nonce, they could not correctly point their `RemoteUnleash` at the victim's secret in the operator namespace. This provided security through obscurity.

However, scripts like `unleash-sync-to-tenant.sh` bypassed the nonce system entirely, generating predictable names (`unleasherator-<namespace>-admin-key`), which left those instances highly vulnerable to Confused Deputy attacks because the name was easily guessable by malicious tenants.

### Migration to Namespace-Bound Secrets
With the introduction of **Namespace-Bound Secret Names**, the nonce is completely obsolete. The controller no longer relies on an unguessable string; it mathematically validates that the requesting tenant's namespace is explicitly embedded in the secret name.

During the migration to strict namespace-bound secrets, temporary backward-compatibility logic (`FEATURE_ALLOW_LEGACY_NAME_BOUND_SECRETS`) is enabled.

This logic allows old, strictly name-bound formats (e.g., `unleasherator-<name>-<nonce>`). To mitigate vulnerabilities during the transition, the controller strictly enforces the presence of a randomly generated `<nonce>` for these legacy formats, making it impossible for an attacker to guess the target secret name.

Once the migration is complete, this feature gate must be disabled, permanently locking the operator into the namespace-bound security model and rendering the nonce entirely defunct.
