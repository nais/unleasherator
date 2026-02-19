package controller

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel/trace"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/nais/unleasherator/internal/o11y"
	"github.com/nais/unleasherator/internal/resources"
	"github.com/nais/unleasherator/internal/unleashclient"
	"github.com/prometheus/client_golang/prometheus"
)

const tokenFinalizer = "unleash.nais.io/finalizer"

var (
	// API Token controller timeouts - prefixed to avoid conflicts with other controllers
	apiTokenRequeueAfter = 1 * time.Hour

	// apiTokenStatus is a Prometheus metric which will be used to expose the status of the Unleash instances
	apiTokenStatus = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "unleasherator_apitoken_status",
			Help: "Status of ApiToken instances",
		},
		[]string{"namespace", "name", "status"},
	)

	apiTokenExistingTokens = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "unleasherator_apitoken_existing_tokens",
			Help: "Number of existing tokens in Unleash for ApiToken",
		},
		[]string{"namespace", "name", "environment"},
	)

	apiTokenDeletedCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "unleasherator_apitoken_deleted_total",
			Help: "Number of ApiTokens deleted from Unleash",
		},
		[]string{"namespace", "name"},
	)

	apiTokenCreatedCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "unleasherator_apitoken_created_total",
			Help: "Number of ApiTokens created in Unleash",
		},
		[]string{"namespace", "name"},
	)
)

func init() {
	metrics.Registry.MustRegister(apiTokenStatus, apiTokenExistingTokens, apiTokenDeletedCounter, apiTokenCreatedCounter)
}

// ApiTokenReconciler reconciles a ApiToken object
type ApiTokenReconciler struct {
	client.Client
	Scheme            *runtime.Scheme
	Recorder          record.EventRecorder
	OperatorNamespace string

	// ApiTokenNameSuffix is the suffix used for the ApiToken names
	// in order to avoid name collisions when multiple clusters are
	// using the same Unleash instance.
	ApiTokenNameSuffix string

	// ApiTokenUpdateEnabled enables updating tokens in Unleash since
	// tokens in Unleash are immutable. This is a feature flag that
	// can be enabled in the operator config.
	ApiTokenUpdateEnabled bool

	Tracer trace.Tracer
}

//+kubebuilder:rbac:groups=unleash.nais.io,resources=apitokens,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=unleash.nais.io,resources=apitokens/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=unleash.nais.io,resources=apitokens/finalizers,verbs=update
//+kubebuilder:rbac:groups=core,resources=secrets,verbs=get;create;update;delete

func (r *ApiTokenReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	spanOpts := o11y.ReconcilerAttributes(ctx, req)
	ctx, span := r.Tracer.Start(ctx, "Reconcile ApiToken", spanOpts...)
	defer span.End()

	log := log.FromContext(ctx).WithName("apitoken").WithValues("TraceID", span.SpanContext().TraceID())
	log.Info("Starting reconciliation of ApiToken")

	token := &unleashv1.ApiToken{}
	err := r.Get(ctx, req.NamespacedName, token)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("ApiToken resource not found. Ignoring since object must be deleted")
			apiTokenStatus.DeleteLabelValues(req.Namespace, req.Name, unleashv1.ApiTokenStatusConditionTypeCreated)
			apiTokenStatus.DeleteLabelValues(req.Namespace, req.Name, unleashv1.ApiTokenStatusConditionTypeFailed)
			return ctrl.Result{Requeue: false}, nil
		}
		log.Error(err, "Failed to get ApiToken")
		return ctrl.Result{}, err
	}

	// Set status to unknown if not set
	if len(token.Status.Conditions) == 0 {
		log.Info("Setting status to unknown for ApiToken")

		err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
			if err := r.Get(ctx, req.NamespacedName, token); err != nil {
				return err
			}
			if len(token.Status.Conditions) > 0 {
				return nil // Already has conditions
			}
			meta.SetStatusCondition(&token.Status.Conditions, metav1.Condition{
				Type:    unleashv1.ApiTokenStatusConditionTypeCreated,
				Status:  metav1.ConditionUnknown,
				Reason:  "Reconciling",
				Message: "Starting reconciliation",
			})
			return r.Status().Update(ctx, token)
		})
		if err != nil {
			log.Error(err, "Failed to update ApiToken status")
			return ctrl.Result{}, err
		}

		if err := r.Get(ctx, req.NamespacedName, token); err != nil {
			log.Error(err, "Failed to get ApiToken")
			return ctrl.Result{}, err
		}
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(token, tokenFinalizer) {
		span.AddEvent("Adding finalizer to ApiToken")
		log.Info("Adding finalizer to ApiToken")

		err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
			if err := r.Get(ctx, req.NamespacedName, token); err != nil {
				return err
			}
			if controllerutil.ContainsFinalizer(token, tokenFinalizer) {
				return nil // Already has finalizer
			}
			controllerutil.AddFinalizer(token, tokenFinalizer)
			return r.Update(ctx, token)
		})
		if err != nil {
			log.Error(err, "Failed to update ApiToken to add finalizer")
			return ctrl.Result{}, err
		}

		if err := r.Get(ctx, req.NamespacedName, token); err != nil {
			log.Error(err, "Failed to get ApiToken")
			return ctrl.Result{}, err
		}
	}

	// Handle deletion early - if the Unleash instance doesn't exist, we can't clean up
	// the token in Unleash, but we should still allow the ApiToken to be deleted
	if token.GetDeletionTimestamp() != nil {
		log.Info("ApiToken marked for deletion")
		if controllerutil.ContainsFinalizer(token, tokenFinalizer) {
			// Try to clean up the token in Unleash if the instance exists
			if err := r.cleanupTokenInUnleash(ctx, token, log); err != nil {
				// Log but don't block deletion - the Unleash instance may not exist
				log.Info("Could not clean up token in Unleash, proceeding with deletion", "error", err.Error())
			}

			// Update status to indicate finalizing
			_ = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
				if err := r.Get(ctx, req.NamespacedName, token); err != nil {
					return err
				}
				meta.SetStatusCondition(&token.Status.Conditions, metav1.Condition{
					Type:    unleashv1.ApiTokenStatusConditionTypeDeleted,
					Status:  metav1.ConditionUnknown,
					Reason:  "Finalizing",
					Message: "Performing finalizer operations",
				})
				return r.Status().Update(ctx, token)
			})

			// Remove finalizer
			log.Info("Removing finalizer from ApiToken")
			err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
				if err := r.Get(ctx, req.NamespacedName, token); err != nil {
					return err
				}
				if !controllerutil.ContainsFinalizer(token, tokenFinalizer) {
					return nil // Already removed
				}
				controllerutil.RemoveFinalizer(token, tokenFinalizer)
				return r.Update(ctx, token)
			})
			if err != nil {
				log.Error(err, "Failed to update ApiToken to remove finalizer")
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Get Unleash instance for ApiToken
	unleash, err := r.getUnleashInstance(ctx, token)
	if err != nil {
		if apierrors.IsNotFound(err) {
			message := fmt.Sprintf("%s resource with name %s not found in namespace %s", token.Spec.UnleashInstance.Kind, token.Spec.UnleashInstance.Name, token.Namespace)
			if statusErr := r.updateStatusFailed(ctx, token, err, "UnleashNotFound", message); statusErr != nil {
				return ctrl.Result{}, statusErr
			}

			return ctrl.Result{}, err
		}

		log.Error(err, "Failed to get Unleash resource")
		return ctrl.Result{}, err
	}

	// Check if Unleash instance is ready
	if !unleash.IsReady() {
		if err := r.updateStatusFailed(ctx, token, nil, "UnleashNotReady", "Unleash instance not ready"); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, fmt.Errorf("unleash instance not ready")
	}

	// Get Unleash API client
	apiClient, err := unleash.ApiClient(ctx, r.Client, r.OperatorNamespace)
	if err != nil {
		reason := "UnleashClientFailed"
		message := "Failed to create Unleash client"

		if apierrors.IsNotFound(err) {
			reason = "UnleashSecretNotFound"
			message = "Unleash secret not found"
		}

		if err := r.updateStatusFailed(ctx, token, err, reason, message); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, err
	}

	// Get token(s) from Unleash
	log.Info("Fetching token from Unleash for ApiToken")
	apiTokens, err := apiClient.GetAPITokensByName(ctx, token.ApiTokenName(r.ApiTokenNameSuffix))
	if err != nil {
		log.Error(err, "Failed to get token from Unleash")
		if err := r.updateStatusFailed(ctx, token, err, "TokenCheckFailed", "Failed to check if token exists in Unleash"); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	}

	var apiToken *unleashclient.ApiToken
	log.WithValues("tokens", len(apiTokens.Tokens)).Info("Fetched token from Unleash for ApiToken")
	// @TODO this is not entirely correct with regards to the environment
	apiTokenExistingTokens.WithLabelValues(token.Namespace, token.Name, token.Spec.Environment).Set(float64(len(apiTokens.Tokens)))

	// Sort tokens by created_at descending
	// This is needed since Unleash can return tokens in any order
	sort.Slice(apiTokens.Tokens, func(i, j int) bool {
		ti, _ := time.Parse(time.RFC3339, apiTokens.Tokens[i].CreatedAt)
		tj, _ := time.Parse(time.RFC3339, apiTokens.Tokens[j].CreatedAt)
		return ti.After(tj)
	})

	// Delete outdated tokens in Unleash
	for _, t := range apiTokens.Tokens {
		// Check if the token is up to date and that we have not already found an up to date token
		if token.IsEqual(t) && apiToken == nil {
			// This is the token we are looking for
			// Continue to the next token
			apiToken = &t
			continue
		}

		log.WithValues("token", t.TokenName, "created_at", t.CreatedAt).Info(fmt.Sprintf("Token is outdated in Unleash. Token diff: %s", token.Diff(t)))
		span.AddEvent(fmt.Sprintf("Deleting old token for %s created at %s in Unleash", t.TokenName, t.CreatedAt))

		// If ApiTokenUpdateEnabled is false, prevent deletion of outdated tokens and subsequent creation of new tokens
		// This also prevents deletion of duplicate tokens.
		if !r.ApiTokenUpdateEnabled {
			log.WithValues("token", t.TokenName, "created_at", t.CreatedAt).Info("Token update is disabled in operator config")

			// Prevent creation of new tokens further down
			if apiToken == nil {
				apiToken = &t
			}

			continue
		}

		// At this point we know that the token is outdated or duplicate and it is safe to delete it
		log.WithValues("token", t.TokenName, "created_at", t.CreatedAt).Info("Deleting token in Unleash for ApiToken")
		apiTokenDeletedCounter.WithLabelValues(token.Namespace, token.Name).Inc()
		err = apiClient.DeleteApiToken(ctx, t.Secret)
		if err != nil {
			if err := r.updateStatusFailed(ctx, token, err, "TokenUpdateFailed", "Failed to delete old token in Unleash"); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, err
		}
	}

	// Create token if it does not exist in Unleash
	if apiToken == nil {
		span.AddEvent("Creating token in Unleash")
		log.Info("Creating token in Unleash for ApiToken")
		apiTokenCreatedCounter.WithLabelValues(token.Namespace, token.Name).Inc()

		// Get version from Unleash instance for v7 compatibility
		var version string
		switch instance := unleash.(type) {
		case *unleashv1.Unleash:
			version = instance.Status.Version
		case *unleashv1.RemoteUnleash:
			version = instance.Status.Version
		}

		apiToken, err = apiClient.CreateAPIToken(ctx, token.ApiTokenRequest(r.ApiTokenNameSuffix, version))
		if err != nil {
			reason := "TokenCreationFailed"
			message := "Failed to create token in Unleash"

			// Check if it's a v7 compatibility issue and enhance error message
			if apiErr, ok := err.(*unleashclient.UnleashAPIError); ok {
				if apiErr.IsV7CompatibilityIssue() {
					// Get version from Unleash instance status for context
					var version string
					switch instance := unleash.(type) {
					case *unleashv1.Unleash:
						version = instance.Status.Version
					case *unleashv1.RemoteUnleash:
						version = instance.Status.Version
					}

					reason = "TokenCreationV7Incompatible"
					if version != "" {
						message = fmt.Sprintf("Failed to create token: Unleash %s API compatibility issue. %s", version, apiErr.Message)
					} else {
						message = fmt.Sprintf("Failed to create token: Unleash v7+ API compatibility issue. %s", apiErr.Message)
					}
				} else {
					message = fmt.Sprintf("Failed to create token in Unleash (HTTP %d): %s", apiErr.StatusCode, apiErr.Message)
				}
			}

			if err := r.updateStatusFailed(ctx, token, err, reason, message); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, err
		}
	}

	secret := resources.ApiTokenSecret(unleash, token, apiToken)
	if err := controllerutil.SetControllerReference(token, secret, r.Scheme); err != nil {
		log.Error(err, "Failed to set controller reference on token secret")
		return ctrl.Result{}, err
	}

	span.AddEvent("Updating token secret")
	log.Info("Updating existing token secret for ApiToken")
	if err := r.Update(ctx, secret); err != nil {
		if !apierrors.IsNotFound(err) {
			if err := r.updateStatusFailed(ctx, token, err, "TokenSecretFailed", "Failed to update existing token secret"); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, err
		}

		span.AddEvent("Creating token secret")
		log.Info("Creating token secret for ApiToken")
		if err := r.Create(ctx, secret); err != nil {
			if err := r.updateStatusFailed(ctx, token, err, "TokenSecretFailed", "Failed to create new token secret"); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, err
		}
	}

	// Set ApiToken status to success
	err = r.updateStatusSuccess(ctx, token)
	return ctrl.Result{RequeueAfter: apiTokenRequeueAfter}, err
}

// getUnleashInstance returns the Unleash instance that the ApiToken belongs to
func (r *ApiTokenReconciler) getUnleashInstance(ctx context.Context, token *unleashv1.ApiToken) (resources.UnleashInstance, error) {
	if token.Spec.UnleashInstance.ApiVersion != unleashv1.GroupVersion.String() {
		return nil, fmt.Errorf("unsupported api version: %s", token.Spec.UnleashInstance.ApiVersion)
	}

	switch token.Spec.UnleashInstance.Kind {
	case "Unleash":
		unleash := &unleashv1.Unleash{}
		if err := r.Get(ctx, types.NamespacedName{Name: token.Spec.UnleashInstance.Name, Namespace: token.Namespace}, unleash); err != nil {
			return nil, err
		}

		return unleash, nil
	case "RemoteUnleash":
		unleash := &unleashv1.RemoteUnleash{}
		if err := r.Get(ctx, types.NamespacedName{Name: token.Spec.UnleashInstance.Name, Namespace: token.Namespace}, unleash); err != nil {
			return nil, err
		}

		return unleash, nil
	default:
		return nil, fmt.Errorf("unsupported api kind: %s", token.Spec.UnleashInstance.Kind)
	}
}

// updateStatusSuccess will set the status condition as created and reset the failed status condition
func (r *ApiTokenReconciler) updateStatusSuccess(ctx context.Context, apiToken *unleashv1.ApiToken) error {
	log := log.FromContext(ctx).WithName("apitoken")
	log.Info("Successfully created ApiToken")

	apiTokenStatus.WithLabelValues(apiToken.Namespace, apiToken.Name, unleashv1.ApiTokenStatusConditionTypeCreated).Set(1.0)
	apiTokenStatus.WithLabelValues(apiToken.Namespace, apiToken.Name, unleashv1.ApiTokenStatusConditionTypeFailed).Set(0.0)

	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		if err := r.Get(ctx, apiToken.NamespacedName(), apiToken); err != nil {
			return err
		}

		meta.RemoveStatusCondition(&apiToken.Status.Conditions, unleashv1.ApiTokenStatusConditionTypeFailed)

		meta.SetStatusCondition(&apiToken.Status.Conditions, metav1.Condition{
			Type:    unleashv1.ApiTokenStatusConditionTypeCreated,
			Status:  metav1.ConditionTrue,
			Reason:  "Reconciling",
			Message: "API token successfully created",
		})

		apiToken.Status.Created = true
		apiToken.Status.Failed = false

		return r.Status().Update(ctx, apiToken)
	})

	if err != nil {
		log.Error(err, "Failed to update status for ApiToken")
		return err
	}

	return nil
}

// updateStatusFailed will set the status condition as failed, but not reset the created status condition
func (r *ApiTokenReconciler) updateStatusFailed(ctx context.Context, apiToken *unleashv1.ApiToken, origErr error, reason, message string) error {
	log := log.FromContext(ctx).WithName("apitoken")

	if origErr != nil {
		log.Error(origErr, fmt.Sprintf("%s for ApiToken", message))
	} else {
		log.Info(fmt.Sprintf("%s for ApiToken", message))
	}

	apiTokenStatus.WithLabelValues(apiToken.Namespace, apiToken.Name, unleashv1.ApiTokenStatusConditionTypeFailed).Set(1.0)

	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		if err := r.Get(ctx, apiToken.NamespacedName(), apiToken); err != nil {
			return err
		}

		meta.SetStatusCondition(&apiToken.Status.Conditions, metav1.Condition{
			Type:    unleashv1.ApiTokenStatusConditionTypeFailed,
			Status:  metav1.ConditionTrue,
			Reason:  reason,
			Message: message,
		})

		apiToken.Status.Failed = true

		return r.Status().Update(ctx, apiToken)
	})

	if err != nil {
		log.Error(err, "Failed to update status for ApiToken")
		return err
	}

	return nil
}

// doFinalizerOperationsForToken will delete the ApiToken from Unleash
func (r *ApiTokenReconciler) doFinalizerOperationsForToken(ctx context.Context, token *unleashv1.ApiToken, unleashClient *unleashclient.Client, log logr.Logger) {
	tokenName := token.ApiTokenName(r.ApiTokenNameSuffix)
	tokens, err := unleashClient.GetAPITokensByName(ctx, tokenName)
	if err != nil {
		log.Error(err, fmt.Sprintf("Failed to get ApiToken %s from Unleash", tokenName))
		return
	}

	if tokens == nil || len(tokens.Tokens) == 0 {
		log.Info(fmt.Sprintf("ApiToken %s not found in Unleash", tokenName))
		return
	}

	for _, t := range tokens.Tokens {
		log.Info(fmt.Sprintf("Deleting ApiToken %s from Unleash", t.TokenName))
		err = unleashClient.DeleteApiToken(ctx, t.Secret)
		if err != nil {
			log.Error(err, fmt.Sprintf("Failed to delete ApiToken %s from Unleash", t.TokenName))
		}
		log.Info(fmt.Sprintf("Successfully deleted ApiToken %s from Unleash", t.TokenName))
	}
}

// cleanupTokenInUnleash attempts to delete the token from Unleash during finalization.
// Returns an error if cleanup fails, but callers may choose to ignore this to allow deletion to proceed.
func (r *ApiTokenReconciler) cleanupTokenInUnleash(ctx context.Context, token *unleashv1.ApiToken, log logr.Logger) error {
	unleash, err := r.getUnleashInstance(ctx, token)
	if err != nil {
		return fmt.Errorf("failed to get Unleash instance: %w", err)
	}

	if !unleash.IsReady() {
		return fmt.Errorf("unleash instance not ready")
	}

	apiClient, err := unleash.ApiClient(ctx, r.Client, r.OperatorNamespace)
	if err != nil {
		return fmt.Errorf("failed to create Unleash client: %w", err)
	}

	log.Info("Performing Finalizer Operations for ApiToken before deletion")
	r.doFinalizerOperationsForToken(ctx, token, apiClient, log)
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ApiTokenReconciler) SetupWithManager(mgr ctrl.Manager) error {
	pred := predicate.GenerationChangedPredicate{}
	return ctrl.NewControllerManagedBy(mgr).
		For(&unleashv1.ApiToken{}).
		WithEventFilter(pred).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 1, // Prevent race conditions with rapid simultaneous changes
		}).
		Complete(r)
}
