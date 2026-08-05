#!/usr/bin/env bash

set -euo pipefail

UNLEASH_NAMESPACE="${UNLEASH_NAMESPACE:-bifrost-unleash}"
OPERATOR_NAMESPACE="${OPERATOR_NAMESPACE:-nais-system}"
CANARY_NAMESPACE="${CANARY_NAMESPACE:-unleasherator-federation-canary}"
SUBSCRIBER_CONTEXT="${SUBSCRIBER_CONTEXT:-}"
FEDERATION_CLUSTER="${FEDERATION_CLUSTER:-}"
BURST_ITERATIONS="${BURST_ITERATIONS:-25}"
METRICS_SERVICE="${METRICS_SERVICE:-unleasherator-controller-manager-metrics-service}"
SMOKE_TEST_LABEL="${SMOKE_TEST_LABEL:-unleasherator.nais.io/federation-smoke-test}"
ALLOW_PRODUCTION_BURST="${ALLOW_PRODUCTION_BURST:-false}"
TARGET_ENVIRONMENT="${TARGET_ENVIRONMENT:-}"
API_TOKEN_ENVIRONMENT="${API_TOKEN_ENVIRONMENT:-development}"
API_TOKEN_PROJECT="${API_TOKEN_PROJECT:-default}"

mode="run"
case "${1:-}" in
"")
  ;;
--preflight)
  mode="preflight"
  ;;
*)
  echo "Usage: $0 [--preflight]" >&2
  exit 1
  ;;
esac

for command in kubectl jq awk; do
  command -v "$command" >/dev/null || {
    echo "Missing required command: $command" >&2
    exit 1
  }
done

confirm() {
  local answer
  read -r -p "$1 [y/N] " answer
  [[ "$answer" =~ ^[Yy]([Ee][Ss])?$ ]]
}

token_cleanup_required=false
cleanup_temporary_token() {
  if [[ "$token_cleanup_required" == "true" ]]; then
    if ! kubectl --context "$subscriber_context" -n "$tenant_namespace" \
      delete apitoken "$token_name" --wait=true --timeout=120s >/dev/null; then
      echo "Failed to clean up ApiToken $tenant_namespace/$token_name" >&2
    fi
    if ! kubectl --context "$subscriber_context" -n "$tenant_namespace" \
      delete secret "$token_name" --ignore-not-found >/dev/null; then
      echo "Failed to clean up Secret $tenant_namespace/$token_name" >&2
    fi
  fi
}
trap cleanup_temporary_token EXIT

context_environment() {
  case "$1" in
  *non-prod* | *dev* | *test* | *ci* | *sandbox*)
    echo "nonproduction"
    ;;
  *prod*)
    echo "production"
    ;;
  *)
    echo "unknown"
    ;;
  esac
}

metric_value() {
  local context="$1"
  local metric="$2"
  local state="$3"
  local status="$4"
  local metrics_path="/api/v1/namespaces/$OPERATOR_NAMESPACE/services/http:$METRICS_SERVICE:8080/proxy/metrics"

  kubectl --context "$context" get --raw "$metrics_path" |
    awk -v series="$metric{state=\"$state\",status=\"$status\"}" '
      index($0, series " ") == 1 {
        print $2
        found = 1
        exit
      }
      END {
        if (!found) {
          print 0
        }
      }
    '
}

wait_for_metric() {
  local context="$1"
  local metric="$2"
  local state="$3"
  local status="$4"
  local target="$5"
  local current

  for _ in {1..100}; do
    current="$(metric_value "$context" "$metric" "$state" "$status")"
    if awk -v current="$current" -v target="$target" 'BEGIN { exit !(current >= target) }'; then
      return 0
    fi
    sleep 0.2
  done
  return 1
}

wait_for_published_hash() {
  local unleash_name="$1"
  local published_hash

  for _ in {1..100}; do
    published_hash="$(kubectl -n "$UNLEASH_NAMESPACE" get unleash "$unleash_name" \
      -o jsonpath='{.status.lastPublishedHash}')"
    if [[ -n "$published_hash" && "$published_hash" != "0" ]]; then
      return 0
    fi
    sleep 0.2
  done
  return 1
}

management_context="$(kubectl config current-context)"
management_server="$(kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}')"

echo "Management context: $management_context"
echo "Management server:  $management_server"

subscriber_context="$SUBSCRIBER_CONTEXT"
if [[ -z "$subscriber_context" ]]; then
  subscriber_prefix=""
  if [[ "$management_context" == *-management* ]]; then
    subscriber_prefix="${management_context%%-management*}-"
  fi
  contexts=()
  while IFS= read -r context; do
    if [[ "$context" != "$management_context" && "$context" != *management* ]]; then
      if [[ -z "$subscriber_prefix" || "$context" == "$subscriber_prefix"* ]]; then
        contexts+=("$context")
      fi
    fi
  done < <(kubectl config get-contexts -o name)
  [[ "${#contexts[@]}" -gt 0 ]] || {
    echo "No subscriber contexts found" >&2
    exit 1
  }

  echo
  echo "Choose the subscriber context:"
  printf '%s\n' "${contexts[@]}" | nl -w2 -s'. '
  read -r -p "Subscriber context number: " context_number
  [[ "$context_number" =~ ^[0-9]+$ ]] || {
    echo "Invalid subscriber context number" >&2
    exit 1
  }
  ((context_number >= 1)) || {
    echo "Subscriber context number is out of range" >&2
    exit 1
  }
  subscriber_context="${contexts[$((context_number - 1))]:-}"
  [[ -n "$subscriber_context" ]] || {
    echo "Subscriber context number is out of range" >&2
    exit 1
  }
fi

subscriber_server="$(kubectl config view --context "$subscriber_context" --minify \
  -o jsonpath='{.clusters[0].cluster.server}')"
manager_environment="$(kubectl --context "$subscriber_context" -n "$OPERATOR_NAMESPACE" \
  get deployment unleasherator-controller-manager -o json |
  jq -c '.spec.template.spec.containers[]
    | select(.name == "manager")
    | .env
  ')"
if [[ -z "$FEDERATION_CLUSTER" ]]; then
  FEDERATION_CLUSTER="$(jq -r '.[] | select(.name == "CLUSTER_NAME") | .value // empty' \
    <<<"$manager_environment")"
fi
[[ -n "$FEDERATION_CLUSTER" ]] || {
  echo "Could not determine subscriber federation cluster name" >&2
  exit 1
}

namespace_bound="$(jq -r \
  '.[] | select(.name == "FEATURE_FEDERATION_NAMESPACE_BOUND_SECRETS") | .value // empty' \
  <<<"$manager_environment")"
legacy_allowed="$(jq -r \
  '.[] | select(.name == "FEATURE_ALLOW_LEGACY_NAME_BOUND_SECRETS") | .value // empty' \
  <<<"$manager_environment")"
[[ "$namespace_bound" == "true" || "$namespace_bound" == "false" ]] || {
  echo "Could not determine namespace-bound federation feature state" >&2
  exit 1
}
[[ "$legacy_allowed" == "true" || "$legacy_allowed" == "false" ]] || {
  echo "Could not determine legacy federation feature state" >&2
  exit 1
}
[[ "$legacy_allowed" == "true" ]] || {
  echo "Legacy validation must remain enabled during smoke testing" >&2
  exit 1
}
[[ "$API_TOKEN_ENVIRONMENT" =~ ^[A-Za-z0-9._-]+$ ]] || {
  echo "API_TOKEN_ENVIRONMENT contains unsupported characters" >&2
  exit 1
}
[[ "$API_TOKEN_PROJECT" =~ ^[A-Za-z0-9._-]+$ ]] || {
  echo "API_TOKEN_PROJECT contains unsupported characters" >&2
  exit 1
}

management_environment="$(context_environment "$management_context")"
subscriber_context_environment="$(context_environment "$subscriber_context")"
federation_environment="$(context_environment "$FEDERATION_CLUSTER")"
if [[ "$subscriber_context_environment" != "unknown" &&
  "$federation_environment" != "unknown" &&
  "$subscriber_context_environment" != "$federation_environment" ]]; then
  echo "Subscriber context and federation cluster belong to different environments" >&2
  exit 1
fi
subscriber_environment="$federation_environment"
if [[ "$subscriber_environment" == "unknown" ]]; then
  subscriber_environment="$subscriber_context_environment"
fi
if [[ -n "$TARGET_ENVIRONMENT" ]]; then
  [[ "$TARGET_ENVIRONMENT" == "production" ||
    "$TARGET_ENVIRONMENT" == "nonproduction" ]] || {
    echo "TARGET_ENVIRONMENT must be production or nonproduction" >&2
    exit 1
  }
  if [[ "$subscriber_environment" != "unknown" &&
    "$subscriber_environment" != "$TARGET_ENVIRONMENT" ]]; then
    echo "TARGET_ENVIRONMENT conflicts with the selected subscriber context" >&2
    exit 1
  fi
  subscriber_environment="$TARGET_ENVIRONMENT"
fi
[[ "$subscriber_environment" != "unknown" ]] || {
  echo "Could not classify the subscriber environment." >&2
  echo "Set TARGET_ENVIRONMENT=production or TARGET_ENVIRONMENT=nonproduction." >&2
  exit 1
}
if [[ "$management_environment" != "unknown" &&
  "$subscriber_environment" != "unknown" &&
  "$management_environment" != "$subscriber_environment" ]]; then
  echo "Management and subscriber contexts belong to different environments" >&2
  exit 1
fi
is_production=false
[[ "$subscriber_environment" == "production" ]] && is_production=true

echo "Subscriber context: $subscriber_context"
echo "Subscriber server:  $subscriber_server"
echo "Federation cluster:  $FEDERATION_CLUSTER"
echo "Target environment:  $subscriber_environment"
echo "Feature flags:       namespace-bound=$namespace_bound legacy=$legacy_allowed"
echo
confirmation="Continue with these contexts?"
if [[ "$is_production" == "true" ]]; then
  if [[ "$mode" == "preflight" ]]; then
    echo "This PRODUCTION preflight is read-only."
    confirmation="Continue with this PRODUCTION preflight?"
  else
    echo "WARNING: This will mutate resources in PRODUCTION."
    confirmation="Continue with this PRODUCTION smoke test?"
  fi
fi
confirm "$confirmation" || {
  echo "Cancelled" >&2
  exit 1
}

unleashes="$(kubectl -n "$UNLEASH_NAMESPACE" get unleashes.unleash.nais.io -o json)"
approved_sources="$(jq -r \
  --arg smokeTestLabel "$SMOKE_TEST_LABEL" '
    .items[]
    | select(.metadata.labels[$smokeTestLabel] == "true")
    | select(.status.connected == true and .status.reconciled == true)
    | .metadata.name
  ' <<<"$unleashes")"
approved_source_details="$(jq -r \
  --arg smokeTestLabel "$SMOKE_TEST_LABEL" '
    .items[]
    | select(.metadata.labels[$smokeTestLabel] == "true")
    | [
        .metadata.name,
        (.status.connected // false | tostring),
        (.status.reconciled // false | tostring),
        (.spec.federation.enabled // false | tostring),
        ((.spec.federation.clusters // []) | join(",")),
        ((.spec.federation.namespaces // []) | join(","))
      ]
    | @tsv
  ' <<<"$unleashes")"
eligible_unlabelled_sources="$(jq -r \
  --arg smokeTestLabel "$SMOKE_TEST_LABEL" '
    .items[]
    | select(.metadata.labels[$smokeTestLabel] != "true")
    | select(.status.connected == true and .status.reconciled == true)
    | select(.spec.federation.enabled != true)
    | .metadata.name
  ' <<<"$unleashes")"
eligible_unlabelled_canaries="$(jq -r \
  --arg cluster "$FEDERATION_CLUSTER" \
  --arg smokeTestLabel "$SMOKE_TEST_LABEL" '
    .items[]
    | select(.metadata.labels[$smokeTestLabel] != "true")
    | select(.status.connected == true and .status.reconciled == true)
    | select(.spec.federation.enabled == true)
    | select((.spec.federation.clusters // []) | index($cluster))
    | select((.spec.federation.namespaces | length) == 1)
    | [.metadata.name, .spec.federation.namespaces[0]]
    | @tsv
  ' <<<"$unleashes")"
canaries="$(jq -r \
    --arg cluster "$FEDERATION_CLUSTER" \
    --arg smokeTestLabel "$SMOKE_TEST_LABEL" '
    .items[]
    | select(.metadata.labels[$smokeTestLabel] == "true")
    | select(.spec.federation.enabled == true)
    | select((.spec.federation.clusters // []) | index($cluster))
    | select((.spec.federation.namespaces | length) == 1)
    | [.metadata.name, .spec.federation.namespaces[0]]
    | @tsv
  ' <<<"$unleashes")"
bootstrap_candidates="$(jq -r \
  --arg smokeTestLabel "$SMOKE_TEST_LABEL" '
    .items[]
    | select(.status.connected == true and .status.reconciled == true)
    | select(.metadata.labels[$smokeTestLabel] == "true")
    | select(.spec.federation.enabled != true)
    | .metadata.name
  ' <<<"$unleashes")"
if [[ -z "$canaries" ]]; then
  if [[ "$namespace_bound" != "false" ]]; then
    if [[ -n "$bootstrap_candidates" ]]; then
      echo "Approved source has not been bootstrapped for federation cluster $FEDERATION_CLUSTER:" >&2
      printf '%s\n' "$bootstrap_candidates" | sed 's/^/  /' >&2
      echo "Set 'Generate namespace-bound federation secrets' to false on $subscriber_context," >&2
      echo "wait for the subscriber pod to restart, then run this script again to bootstrap it." >&2
    elif [[ -n "$approved_sources" ]]; then
      echo "Approved source already has an incompatible federation configuration:" >&2
      printf '%s\n' "$approved_source_details" |
        awk -F '\t' '{
          printf "  %s: connected=%s reconciled=%s federation=%s clusters=[%s] namespaces=[%s]\n",
            $1, $2, $3, $4, $5, $6
        }' >&2
      echo "The script will not overwrite it." >&2
      if [[ -n "$eligible_unlabelled_sources" ]]; then
        echo "Healthy non-federated alternatives that can be approved:" >&2
        printf '%s\n' "$eligible_unlabelled_sources" | sed 's/^/  /' >&2
      fi
      if [[ -n "$eligible_unlabelled_canaries" ]]; then
        echo "Healthy existing one-namespace canaries that can be approved:" >&2
        printf '%s\n' "$eligible_unlabelled_canaries" |
          awk -F '\t' '{ printf "  %s -> %s\n", $1, $2 }' >&2
      fi
    else
      echo "No approved smoke-test source found." >&2
      echo "Label one healthy source instance with $SMOKE_TEST_LABEL=true" >&2
      if [[ -n "$eligible_unlabelled_sources" ]]; then
        echo "Healthy non-federated alternatives:" >&2
        printf '%s\n' "$eligible_unlabelled_sources" | sed 's/^/  /' >&2
      fi
      if [[ -n "$eligible_unlabelled_canaries" ]]; then
        echo "Healthy existing one-namespace canaries:" >&2
        printf '%s\n' "$eligible_unlabelled_canaries" |
          awk -F '\t' '{ printf "  %s -> %s\n", $1, $2 }' >&2
      fi
    fi
    exit 1
  fi

  [[ -n "$bootstrap_candidates" ]] || {
    echo "No approved, healthy, non-federated Unleash resource is available for bootstrapping" >&2
    echo "The script will not overwrite an existing federation configuration." >&2
    if [[ -n "$approved_source_details" ]]; then
      echo "Approved source configuration:" >&2
      printf '%s\n' "$approved_source_details" |
        awk -F '\t' '{
          printf "  %s: connected=%s reconciled=%s federation=%s clusters=[%s] namespaces=[%s]\n",
            $1, $2, $3, $4, $5, $6
        }' >&2
    fi
    if [[ -n "$eligible_unlabelled_sources" ]]; then
      echo "Healthy non-federated alternatives that can be approved:" >&2
      printf '%s\n' "$eligible_unlabelled_sources" | sed 's/^/  /' >&2
    fi
    if [[ -n "$eligible_unlabelled_canaries" ]]; then
      echo "Healthy existing one-namespace canaries that can be approved:" >&2
      printf '%s\n' "$eligible_unlabelled_canaries" |
        awk -F '\t' '{ printf "  %s -> %s\n", $1, $2 }' >&2
    fi
    exit 1
  }

  if ! kubectl --context "$subscriber_context" get namespace "$CANARY_NAMESPACE" >/dev/null 2>&1; then
    if [[ "$is_production" == "true" ]]; then
      echo "Production canary namespace $CANARY_NAMESPACE does not exist." >&2
      echo "Create and approve it through the normal namespace workflow before continuing." >&2
      exit 1
    fi
    if [[ "$mode" == "run" ]]; then
      echo "Creating dedicated subscriber namespace: $CANARY_NAMESPACE"
      kubectl --context "$subscriber_context" create namespace "$CANARY_NAMESPACE"
    else
      echo "Preflight: subscriber namespace $CANARY_NAMESPACE will be created during bootstrap."
    fi
  fi

  if [[ "$mode" == "preflight" ]]; then
    echo
    echo "Federation smoke-test preflight succeeded"
    echo "Mode:                 legacy bootstrap"
    echo "Approved candidates:"
    printf '%s\n' "$bootstrap_candidates" | sed 's/^/  /'
    exit 0
  fi

  echo
  echo "Choose one healthy Unleash to bootstrap as a legacy canary:"
  printf '%s\n' "$bootstrap_candidates" | nl -w2 -s'. '
  read -r -p "Canary number: " canary_number
  [[ "$canary_number" =~ ^[0-9]+$ ]] || {
    echo "Invalid canary number" >&2
    exit 1
  }
  unleash_name="$(printf '%s\n' "$bootstrap_candidates" | sed -n "${canary_number}p")"
  [[ -n "$unleash_name" ]] || {
    echo "Canary number is out of range" >&2
    exit 1
  }

  echo
  echo "This will federate $UNLEASH_NAMESPACE/$unleash_name to"
  echo "$subscriber_context/$CANARY_NAMESPACE using the legacy secret format."
  confirm "Bootstrap this canary?" || {
    echo "Cancelled" >&2
    exit 1
  }

  kubectl -n "$UNLEASH_NAMESPACE" patch unleash "$unleash_name" --type=merge -p \
    "{\"spec\":{\"federation\":{\"enabled\":true,\"clusters\":[\"$FEDERATION_CLUSTER\"],\"namespaces\":[\"$CANARY_NAMESPACE\"]}}}"

  echo "Waiting for $subscriber_context/$CANARY_NAMESPACE RemoteUnleash $unleash_name..."
  for _ in {1..36}; do
    if kubectl --context "$subscriber_context" -n "$CANARY_NAMESPACE" \
      get remoteunleash "$unleash_name" >/dev/null 2>&1; then
      break
    fi
    printf '.'
    sleep 5
  done
  echo

  legacy_secret_namespace="$(kubectl --context "$subscriber_context" -n "$CANARY_NAMESPACE" \
    get remoteunleash "$unleash_name" -o jsonpath='{.spec.adminSecret.namespace}')"
  [[ -z "$legacy_secret_namespace" ]] || {
    echo "Bootstrap did not create a legacy same-namespace secret" >&2
    exit 1
  }

  echo
  echo "Legacy canary created successfully."
  echo "Set 'Generate namespace-bound federation secrets' back to true on $subscriber_context,"
  echo "wait for the subscriber pod to restart, then run this script again to migrate it."
  exit 0
fi

[[ "$namespace_bound" == "true" ]] || {
  if [[ "$mode" == "preflight" ]]; then
    echo
    echo "Verifying approved legacy canaries..."
    while IFS=$'\t' read -r canary_name canary_namespace; do
      remote_unleash="$(kubectl --context "$subscriber_context" -n "$canary_namespace" \
        get remoteunleash "$canary_name" -o json)"
      secret_namespace="$(jq -r '.spec.adminSecret.namespace // empty' <<<"$remote_unleash")"
      [[ -z "$secret_namespace" ]] || {
        echo "$canary_namespace/$canary_name already references a cross-namespace secret" >&2
        exit 1
      }
      echo "  $canary_name -> $canary_namespace: legacy secret confirmed"
    done <<<"$canaries"

    echo
    echo "Federation smoke-test preflight succeeded"
    echo "Mode:                 legacy canary ready for migration"
    echo "Next feature flags:   namespace-bound=true legacy=true"
    exit 0
  fi
  echo "Set 'Generate namespace-bound federation secrets' to true on $subscriber_context," >&2
  echo "wait for the subscriber pod to restart, then run this script again." >&2
  exit 1
}

if [[ "$mode" == "preflight" ]]; then
  echo
  echo "Federation smoke-test preflight succeeded"
  echo "Mode:                 namespace-bound migration"
  echo "Approved canaries:"
  printf '%s\n' "$canaries" | awk -F '\t' '{ printf "  %s -> %s\n", $1, $2 }'
  exit 0
fi

echo
echo "Choose one canary (Unleash, tenant namespace):"
printf '%s\n' "$canaries" | nl -w2 -s'. '
read -r -p "Canary number: " canary_number
[[ "$canary_number" =~ ^[0-9]+$ ]] || {
  echo "Invalid canary number" >&2
  exit 1
}
canary="$(printf '%s\n' "$canaries" | sed -n "${canary_number}p")"
[[ -n "$canary" ]] || {
  echo "Canary number is out of range" >&2
  exit 1
}
IFS=$'\t' read -r unleash_name tenant_namespace <<<"$canary"

echo
echo "Subscriber context: $subscriber_context"
echo "Subscriber server:  $subscriber_server"
echo "Canary:              $UNLEASH_NAMESPACE/$unleash_name"
echo "Tenant namespace:    $tenant_namespace"
echo
confirm "Replay this canary?" || {
  echo "Cancelled" >&2
  exit 1
}

remote_unleash="$(kubectl --context "$subscriber_context" -n "$tenant_namespace" \
  get remoteunleash "$unleash_name" -o json)"
old_secret_name="$(jq -r '.spec.adminSecret.name' <<<"$remote_unleash")"
old_secret_namespace="$(jq -r '.spec.adminSecret.namespace // empty' <<<"$remote_unleash")"
[[ -n "$old_secret_namespace" ]] || old_secret_namespace="$tenant_namespace"
shared_secret_references="$(kubectl --context "$subscriber_context" \
  get remoteunleashes.unleash.nais.io --all-namespaces -o json |
  jq -r \
    --arg secretName "$old_secret_name" \
    --arg secretNamespace "$old_secret_namespace" \
    --arg currentName "$unleash_name" \
    --arg currentNamespace "$tenant_namespace" '
      .items[]
      | select(
          .metadata.name != $currentName or
          .metadata.namespace != $currentNamespace
        )
      | select(.spec.adminSecret.name == $secretName)
      | select(
          (
            if (.spec.adminSecret.namespace // "") == ""
            then .metadata.namespace
            else .spec.adminSecret.namespace
            end
          ) == $secretNamespace
        )
      | [.metadata.namespace, .metadata.name]
      | @tsv
    ')"
[[ -z "$shared_secret_references" ]] || {
  echo "Cannot migrate: legacy secret $old_secret_namespace/$old_secret_name is shared by:" >&2
  printf '%s\n' "$shared_secret_references" |
    awk -F '\t' '{ printf "  %s/%s\n", $1, $2 }' >&2
  echo "Deploy the shared-secret cleanup fix or retire the stale references first." >&2
  exit 1
}

kubectl -n "$UNLEASH_NAMESPACE" patch unleash "$unleash_name" \
  --subresource=status --type=merge \
  -p '{"status":{"lastPublishedHash":0}}'

kubectl -n "$UNLEASH_NAMESPACE" label unleash "$unleash_name" \
  "unleasherator.nais.io/federation-replay=$(date +%s)" --overwrite

echo "Waiting for RemoteUnleash $tenant_namespace/$unleash_name to use an operator-namespace secret..."
for _ in {1..36}; do
  secret_namespace="$(kubectl --context "$subscriber_context" -n "$tenant_namespace" \
    get remoteunleash "$unleash_name" -o jsonpath='{.spec.adminSecret.namespace}')"
  [[ "$secret_namespace" == "$OPERATOR_NAMESPACE" ]] && break
  printf '.'
  sleep 5
done
echo

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

if [[ "$old_secret_namespace/$old_secret_name" != "$OPERATOR_NAMESPACE/$new_secret_name" ]]; then
  if kubectl --context "$subscriber_context" -n "$old_secret_namespace" \
    get secret "$old_secret_name" >/dev/null 2>&1; then
    echo "Legacy secret still exists: $old_secret_namespace/$old_secret_name" >&2
    exit 1
  fi
fi

token_name="federation-canary-$(date +%s)-$$"
kubectl --context "$subscriber_context" -n "$tenant_namespace" apply -f - <<EOF
apiVersion: unleash.nais.io/v1
kind: ApiToken
metadata:
  name: $token_name
spec:
  unleashInstance:
    apiVersion: unleash.nais.io/v1
    kind: RemoteUnleash
    name: $unleash_name
  secretName: $token_name
  type: CLIENT
  environment: $API_TOKEN_ENVIRONMENT
  projects:
    - $API_TOKEN_PROJECT
EOF
token_cleanup_required=true

echo "Waiting for temporary ApiToken provisioning..."
for _ in {1..36}; do
  token_status="$(kubectl --context "$subscriber_context" -n "$tenant_namespace" \
    get apitoken "$token_name" -o json)"
  token_created="$(jq -r '.status.created // false' <<<"$token_status")"
  token_failed="$(jq -r '.status.failed // false' <<<"$token_status")"
  [[ "$token_created" == "true" || "$token_failed" == "true" ]] && break
  printf '.'
  sleep 5
done
echo

[[ "${token_created:-false}" == "true" && "${token_failed:-false}" == "false" ]] || {
  echo "Temporary ApiToken provisioning failed; cleaning it up" >&2
  exit 1
}
kubectl --context "$subscriber_context" -n "$tenant_namespace" \
  get secret "$token_name" >/dev/null
kubectl --context "$subscriber_context" -n "$tenant_namespace" \
  delete apitoken "$token_name" --wait=true --timeout=120s
kubectl --context "$subscriber_context" -n "$tenant_namespace" \
  delete secret "$token_name" --ignore-not-found
token_cleanup_required=false

echo
echo "Canary migration succeeded"
echo "RemoteUnleash:  connected=$connected reconciled=$reconciled"
echo "New secret:     $OPERATOR_NAMESPACE/$new_secret_name"
echo "Authorization:  $authorized_namespace"
echo "Legacy secret:  removed"
echo "ApiToken:       provisioned and cleaned up"

run_burst=true
if [[ "$is_production" == "true" && "$ALLOW_PRODUCTION_BURST" != "true" ]]; then
  run_burst=false
  echo
  echo "Burst test skipped in production."
fi
if [[ "$run_burst" == "true" ]] &&
  confirm "Run a bounded $BURST_ITERATIONS-event federation burst test?"; then
  if [[ ! "$BURST_ITERATIONS" =~ ^[0-9]+$ ]] ||
    ((BURST_ITERATIONS < 1 || BURST_ITERATIONS > 100)); then
    echo "BURST_ITERATIONS must be an integer from 1 to 100" >&2
    exit 1
  fi

  if ! published_success_before="$(metric_value "$management_context" \
    unleasherator_federation_published_total provisioned success)" ||
    ! published_failed_before="$(metric_value "$management_context" \
      unleasherator_federation_published_total provisioned failed)" ||
    ! received_success_before="$(metric_value "$subscriber_context" \
      unleasherator_federation_received_total provisioned success)" ||
    ! received_failed_before="$(metric_value "$subscriber_context" \
      unleasherator_federation_received_total provisioned failed)" ||
    ! received_rejected_before="$(metric_value "$subscriber_context" \
      unleasherator_federation_received_total provisioned rejected)"; then
    echo "Could not read federation metrics through the Kubernetes service proxy" >&2
    exit 1
  fi

  echo "Publishing $BURST_ITERATIONS federation events..."
  for ((iteration = 1; iteration <= BURST_ITERATIONS; iteration++)); do
    kubectl -n "$UNLEASH_NAMESPACE" patch unleash "$unleash_name" \
      --subresource=status --type=merge \
      -p '{"status":{"lastPublishedHash":0}}' >/dev/null
    kubectl -n "$UNLEASH_NAMESPACE" label unleash "$unleash_name" \
      "unleasherator.nais.io/federation-replay=$(date +%s)-$$-$iteration" \
      --overwrite >/dev/null

    wait_for_published_hash "$unleash_name" || {
      echo "Federation event $iteration did not update lastPublishedHash within 20 seconds" >&2
      exit 1
    }
    published_target="$(awk -v baseline="$published_success_before" -v count="$iteration" \
      'BEGIN { print baseline + count }')"
    wait_for_metric "$management_context" \
      unleasherator_federation_published_total provisioned success "$published_target" || {
      echo "Federation event $iteration was not published within 20 seconds" >&2
      exit 1
    }
    printf '.'
  done
  echo

  received_target="$(awk -v baseline="$received_success_before" -v count="$BURST_ITERATIONS" \
    'BEGIN { print baseline + count }')"
  echo "Waiting for all events to be processed by $subscriber_context..."
  wait_for_metric "$subscriber_context" \
    unleasherator_federation_received_total provisioned success "$received_target" || {
    echo "Subscriber did not process all $BURST_ITERATIONS events within 20 seconds" >&2
    exit 1
  }

  published_failed_after="$(metric_value "$management_context" \
    unleasherator_federation_published_total provisioned failed)"
  received_failed_after="$(metric_value "$subscriber_context" \
    unleasherator_federation_received_total provisioned failed)"
  received_rejected_after="$(metric_value "$subscriber_context" \
    unleasherator_federation_received_total provisioned rejected)"

  [[ "$published_failed_after" == "$published_failed_before" ]] || {
    echo "Publisher failure counter increased during burst test" >&2
    exit 1
  }
  [[ "$received_failed_after" == "$received_failed_before" ]] || {
    echo "Subscriber failure counter increased during burst test" >&2
    exit 1
  }
  [[ "$received_rejected_after" == "$received_rejected_before" ]] || {
    echo "Subscriber rejection counter increased during burst test" >&2
    exit 1
  }

  remote_unleash="$(kubectl --context "$subscriber_context" -n "$tenant_namespace" \
    get remoteunleash "$unleash_name" -o json)"
  connected="$(jq -r '.status.connected // false' <<<"$remote_unleash")"
  reconciled="$(jq -r '.status.reconciled // false' <<<"$remote_unleash")"
  [[ "$connected" == "true" && "$reconciled" == "true" ]] || {
    echo "RemoteUnleash is not healthy after burst test" >&2
    exit 1
  }

  echo
  echo "Federation burst test succeeded"
  echo "Published:      $BURST_ITERATIONS/$BURST_ITERATIONS"
  echo "Received:       $BURST_ITERATIONS/$BURST_ITERATIONS"
  echo "Failed/rejected: 0"
  echo "RemoteUnleash:  connected=$connected reconciled=$reconciled"
fi
