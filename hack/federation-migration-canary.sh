#!/usr/bin/env bash

set -euo pipefail

UNLEASH_NAMESPACE="${UNLEASH_NAMESPACE:-bifrost-unleash}"
OPERATOR_NAMESPACE="${OPERATOR_NAMESPACE:-nais-system}"

for command in kubectl jq; do
  command -v "$command" >/dev/null || {
    echo "Missing required command: $command" >&2
    exit 1
  }
done

management_context="$(kubectl config current-context)"
management_server="$(kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}')"

echo "Management context: $management_context"
echo "Management server:  $management_server"
echo
read -r -p "Type the management context name to continue: " confirmation
[[ "$confirmation" == "$management_context" ]] || {
  echo "Context confirmation failed" >&2
  exit 1
}

read -r -p "Subscriber context: " subscriber_context
read -r -p "Canary Unleash name: " unleash_name
read -r -p "Tenant namespace: " tenant_namespace

subscriber_server="$(kubectl config view --context "$subscriber_context" --minify \
  -o jsonpath='{.clusters[0].cluster.server}')"

echo
echo "Subscriber context: $subscriber_context"
echo "Subscriber server:  $subscriber_server"
echo "Canary:              $UNLEASH_NAMESPACE/$unleash_name"
echo "Tenant namespace:    $tenant_namespace"
echo
read -r -p "Type the Unleash name to replay this canary: " confirmation
[[ "$confirmation" == "$unleash_name" ]] || {
  echo "Canary confirmation failed" >&2
  exit 1
}

remote_unleash="$(kubectl --context "$subscriber_context" -n "$tenant_namespace" \
  get remoteunleash "$unleash_name" -o json)"
old_secret_name="$(jq -r '.spec.adminSecret.name' <<<"$remote_unleash")"
old_secret_namespace="$(jq -r '.spec.adminSecret.namespace // empty' <<<"$remote_unleash")"
[[ -n "$old_secret_namespace" ]] || old_secret_namespace="$tenant_namespace"

kubectl -n "$UNLEASH_NAMESPACE" patch unleash "$unleash_name" \
  --subresource=status --type=merge \
  -p '{"status":{"lastPublishedHash":0}}'

kubectl -n "$UNLEASH_NAMESPACE" label unleash "$unleash_name" \
  "unleasherator.nais.io/federation-replay=$(date +%s)" --overwrite

echo "Waiting for the RemoteUnleash to use an operator-namespace secret..."
for _ in {1..36}; do
  secret_namespace="$(kubectl --context "$subscriber_context" -n "$tenant_namespace" \
    get remoteunleash "$unleash_name" -o jsonpath='{.spec.adminSecret.namespace}')"
  [[ "$secret_namespace" == "$OPERATOR_NAMESPACE" ]] && break
  sleep 5
done

[[ "${secret_namespace:-}" == "$OPERATOR_NAMESPACE" ]] || {
  echo "Migration did not converge within 3 minutes" >&2
  exit 1
}

remote_unleash="$(kubectl --context "$subscriber_context" -n "$tenant_namespace" \
  get remoteunleash "$unleash_name" -o json)"
new_secret_name="$(jq -r '.spec.adminSecret.name' <<<"$remote_unleash")"
connected="$(jq -r '.status.connected // false' <<<"$remote_unleash")"
reconciled="$(jq -r '.status.reconciled // false' <<<"$remote_unleash")"
authorized_namespace="$(kubectl --context "$subscriber_context" -n "$OPERATOR_NAMESPACE" \
  get secret "$new_secret_name" \
  -o jsonpath='{.metadata.annotations.unleash\.nais\.io/authorized-namespace}')"

[[ "$connected" == "true" && "$reconciled" == "true" ]] || {
  echo "RemoteUnleash is not healthy after migration" >&2
  exit 1
}
[[ "$authorized_namespace" == "$tenant_namespace" ]] || {
  echo "Secret authorization annotation is incorrect" >&2
  exit 1
}

if kubectl --context "$subscriber_context" -n "$old_secret_namespace" \
  get secret "$old_secret_name" >/dev/null 2>&1; then
  echo "Legacy secret still exists: $old_secret_namespace/$old_secret_name" >&2
  exit 1
fi

echo
echo "Canary migration succeeded"
echo "RemoteUnleash:  connected=$connected reconciled=$reconciled"
echo "New secret:     $OPERATOR_NAMESPACE/$new_secret_name"
echo "Authorization:  $authorized_namespace"
echo "Legacy secret:  removed"
