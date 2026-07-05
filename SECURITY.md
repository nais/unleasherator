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
| **Tampering** | A tenant alters the `adminSecret` reference in their `RemoteUnleash` to point to a secret they do not own. | **Authoritative Authorization Annotation:** Managed cross-namespace secrets carry an operator-stamped `unleash.nais.io/authorized-namespace` annotation that the controller requires to equal the requesting tenant's namespace. This is not derivable from the (attacker-controlled) secret name. |
| **Repudiation** | A tenant denies creating a malicious `RemoteUnleash`. | Kubernetes Audit Logs provide non-repudiation for all API server interactions. |
| **Information Disclosure** | A tenant reads the raw Unleash Admin API token intended for another tenant (or even their own token, if they shouldn't have raw admin access). | **Operator Namespace Isolation:** Tokens are stored exclusively in the `nais-system` (operator) namespace. Tenants cannot read these secrets. The controller reads them on their behalf. |
| **Denial of Service** | A tenant provisions thousands of `RemoteUnleash` instances to overwhelm the operator or the Unleash API. | Kubernetes ResourceQuotas and rate-limiting on the Unleash API protect against DoS. |
| **Elevation of Privilege (Confused Deputy)** | A tenant tricks the operator into using its elevated privileges to read an arbitrary secret (or another tenant's token) from `nais-system` and applying it to their own `RemoteUnleash`. | **Authoritative Authorization Annotation:** The operator reads the `unleash.nais.io/authorized-namespace` annotation off the fetched secret and requires it to equal the requesting `RemoteUnleash.Namespace`. Because the annotation is stamped by the operator (not encoded in the attacker-controlled name), Tenant A cannot craft a name that grants access to Tenant B's secret. |

## The "Confused Deputy" Attack Vector

A Confused Deputy attack occurs when a lower-privileged entity (a tenant) tricks a higher-privileged entity (the Unleasherator controller) into performing an action on its behalf (fetching a secret from `nais-system`).

### The Vulnerability
If the controller blindly accepts any secret name in the `nais-system` namespace:
1. Tenant A (malicious) wants to steal access to Tenant B's Unleash instance.
2. Tenant A knows that Tenant B's secret is stored as `unleasherator-tenant-b-secret` in `nais-system`.
3. Tenant A creates a `RemoteUnleash` in their own namespace, setting `spec.adminSecret.name = unleasherator-tenant-b-secret` and `spec.adminSecret.namespace = nais-system`.
4. The controller (running with cluster-admin privileges) fetches the secret and provisions an API token for Tenant A using Tenant B's admin credentials.

### The Solution: An Authoritative Authorization Annotation

The historical mitigation embedded the tenant namespace in the secret *name* and parsed it back out. This is fundamentally fragile: `RemoteUnleash.Name` is attacker-controlled, and because `-` is both a separator and a legal character in names/namespaces, a name like `my-unleash-tenant-b` cannot be securely separated from its namespace by string parsing. A tenant could craft a name that makes the parser "see" a victim namespace.

The primary control is therefore **not** the name. When the federation subscriber provisions a managed admin secret in the operator namespace, it stamps an authoritative annotation recording the single tenant namespace the secret is authorized for:

```
metadata:
  annotations:
    unleash.nais.io/authorized-namespace: <tenant-namespace>
```

This annotation is set by the operator from the trusted Pub/Sub payload. Tenants cannot read these operator-namespace secrets and cannot influence the annotation. It is the source of truth for *who may use this secret*.

### Validation Logic
When a `RemoteUnleash` (created in namespace `Tenant-A`) references a cross-namespace secret in the operator namespace, the controller fetches the secret **once** and then authorizes as follows:

1. **Authoritative annotation (primary control).** If the fetched secret carries `unleash.nais.io/authorized-namespace`, the controller requires it to equal `remoteUnleash.Namespace`. If it does not match, the request is rejected with a terminal validation error. This holds even while legacy compatibility is enabled, so it fully closes the confused-deputy vector for all managed secrets.

```go
authorizedNS, ok := adminSecret.Annotations["unleash.nais.io/authorized-namespace"]
if ok && authorizedNS != remoteUnleash.Namespace {
    return Error("Validation failed: secret is authorized for a different namespace")
}
```

2. **Legacy fallback (defense-in-depth only).** Secrets *without* the annotation (created by pre-existing bash scripts or the old federation subscriber) are only accepted when `FEATURE_ALLOW_LEGACY_NAME_BOUND_SECRETS=true`. For these, the controller performs a relaxed name-binding check (the name must be bound to the `RemoteUnleash` instance name). This is no longer the primary security control — see the Legacy Formats section below. When `FEATURE_ALLOW_LEGACY_NAME_BOUND_SECRETS=false`, only annotation-bearing secrets whose annotation matches the tenant namespace are accepted, and every managed secret must additionally carry a matching `url` key (fail-closed URL validation).

A same-namespace reference (the secret lives in the tenant's own namespace) never crosses a privilege boundary and does not go through the cross-namespace authorization gate.

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
4. **The Attack Fails (annotation mismatch and/or 404 Not Found):**
   If malicious Tenant A points at a real managed secret (e.g. Tenant B's), the controller reads the secret's `unleash.nais.io/authorized-namespace` annotation, sees `Tenant-B`, and rejects the request because it does not equal `Tenant-A`. If instead Tenant A predicts a namespace-bound name for their *own* namespace, no such secret was ever created (only authorized namespaces get one), so the fetch returns `404 Not Found`.

**Conclusion:** A tenant can only successfully provision API tokens if a Cluster Admin has explicitly authorized their namespace on the central Management Cluster. Authorization is enforced by the operator-stamped annotation, not by trusting the requested secret name.

## Legacy Formats, The "Nonce", and Migration

### Where Legacy Federation Secrets Live
Legacy (non-namespace-bound) federation secrets are created in the **tenant's own namespace**, one per authorized namespace, and the generated `RemoteUnleash` references them with an empty `spec.adminSecret.namespace` (i.e. same namespace). This matches the original pre-migration layout and has two important properties:

1. **N secrets align with N RemoteUnleashes.** The subscriber emits exactly one admin secret per RemoteUnleash, so downstream provisioning/removal never indexes past the end of the slice (previously a panic when more than one namespace was authorized — the default configuration).
2. **No relocation or orphaning.** Tenant secrets are not moved into the operator namespace, so nothing is left behind.

Because these references are same-namespace, they do not cross a privilege boundary and never reach the cross-namespace authorization gate.

### The Nonce Is Now Cryptographically Random
The subscriber derives a per-message secret nonce. When the publisher supplies one it is used verbatim; when it is **absent**, the subscriber generates a fresh **cryptographically-random** nonce (`crypto/rand`) once per message and uses it for both the secret name and the matching `RemoteUnleash` reference in the same pass, so they stay consistent. The previous behavior fell back to the literal string `default`, which made namespace-bound secret names fully guessable — that fallback has been removed.

For namespace-bound managed secrets the nonce is defense-in-depth only; the authoritative control is the `unleash.nais.io/authorized-namespace` annotation. For annotation-less legacy secrets, the random nonce (plus RBAC on who may create secrets in the operator namespace) is what keeps a federation-generated name from being guessable.

### Migration
`FEATURE_FEDERATION_NAMESPACE_BOUND_SECRETS` makes the subscriber write managed secrets into the operator namespace carrying the authorized-namespace annotation. `FEATURE_ALLOW_LEGACY_NAME_BOUND_SECRETS` (default `true` during migration) keeps accepting annotation-less cross-namespace secrets via the relaxed name-binding fallback so existing tenants are not hard-broken — including the previously-rejected exact `unleasherator-<name>-admin-key` form.

Once migration is complete, set `FEATURE_ALLOW_LEGACY_NAME_BOUND_SECRETS=false`. From then on, only annotation-bearing secrets whose annotation matches the requesting namespace are accepted for cross-namespace references, and every such secret must carry a matching `url` key (fail-closed URL validation). Name parsing plays no part in the security decision at that point.
