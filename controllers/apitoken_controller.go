package controllers

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel/trace"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/nais/unleasherator/pkg/o11y"
	"github.com/nais/unleasherator/pkg/resources"
	"github.com/nais/unleasherator/pkg/unleashclient"
	"github.com/prometheus/client_golang/prometheus"
)

const tokenFinalizer = "unleash.nais.io/finalizer"

var (
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
	metrics.Registry.MustRegister(apiTokenStatus)
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
//+kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch;delete

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
			return ctrl.Result{Requeue: false}, nil
		}
		log.Error(err, "Failed to get ApiToken")
		return ctrl.Result{}, err
	}

	// Set status to unknown if not set
	if token.Status.Conditions == nil || len(token.Status.Conditions) == 0 {
		log.Info("Setting status to unknown for ApiToken")

		meta.SetStatusCondition(&token.Status.Conditions, metav1.Condition{
			Type:    unleashv1.ApiTokenStatusConditionTypeCreated,
			Status:  metav1.ConditionUnknown,
			Reason:  "Reconciling",
			Message: "Starting reconciliation",
		})

		if err = r.Status().Update(ctx, token); err != nil {
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

		if ok := controllerutil.AddFinalizer(token, tokenFinalizer); !ok {
			log.Error(err, "Failed to add finalizer to ApiToken")
			return ctrl.Result{}, err
		}

		if err = r.Update(ctx, token); err != nil {
			log.Error(err, "Failed to update ApiToken to add finalizer")
			return ctrl.Result{}, err
		}

		if err := r.Get(ctx, req.NamespacedName, token); err != nil {
			log.Error(err, "Failed to get ApiToken")
			return ctrl.Result{}, err
		}
	}

	// Get Unleash instance for ApiToken
	unleash, err := r.getUnleashInstance(ctx, token)
	if err != nil {
		if apierrors.IsNotFound(err) {
			message := fmt.Sprintf("%s resource with name %s not found in namespace %s", token.Spec.UnleashInstance.Kind, token.Spec.UnleashInstance.Name, token.Namespace)
			if err := r.updateStatusFailed(ctx, token, err, "UnleashNotFound", message); err != nil {
				return ctrl.Result{}, err
			}

			return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
		}

		log.Error(err, "Failed to get Unleash resource")
		return ctrl.Result{}, err
	}

	// Check if Unleash instance is ready
	if !unleash.IsReady() {
		if err := r.updateStatusFailed(ctx, token, nil, "UnleashNotReady", "Unleash instance not ready"); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{RequeueAfter: 1 * time.Minute}, nil
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

	// Check if marked for deletion
	// @TODO won't work if Unleash is unavailable
	if token.GetDeletionTimestamp() != nil {
		log.Info("ApiToken marked for deletion")
		if controllerutil.ContainsFinalizer(token, tokenFinalizer) {
			log.Info("Performing Finalizer Operations for ApiToken before deletion")
			r.doFinalizerOperationsForToken(ctx, token, apiClient, log)

			if err := r.Get(ctx, req.NamespacedName, token); err != nil {
				log.Error(err, "Failed to get ApiToken")
				return ctrl.Result{}, err
			}

			meta.SetStatusCondition(&token.Status.Conditions, metav1.Condition{
				Type:    unleashv1.ApiTokenStatusConditionTypeDeleted,
				Status:  metav1.ConditionUnknown,
				Reason:  "Finalizing",
				Message: "Performing finalizer operations",
			})

			if err = r.Status().Update(ctx, token); err != nil {
				log.Error(err, "Failed to update ApiToken status")
				return ctrl.Result{}, err
			}

			log.Info("Removing finalizer from ApiToken")
			if ok := controllerutil.RemoveFinalizer(token, tokenFinalizer); !ok {
				log.Error(err, "Failed to remove finalizer from ApiToken")
				return ctrl.Result{}, err
			}

			if err = r.Update(ctx, token); err != nil {
				log.Error(err, "Failed to update ApiToken to remove finalizer")
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
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
	log.WithValues("tokens", len(apiTokens.Tokens), "unleashApiTokenName", token.ApiTokenName(r.ApiTokenNameSuffix)).Info("Fetched tokens from Unleash for ApiToken")
	apiTokenExistingTokens.WithLabelValues(token.Namespace, token.Name, token.Spec.Environment).Set(float64(len(apiTokens.Tokens)))

	// Delete outdated tokens in Unleash
	log.Info("Deleting outdated tokens in Unleash for ApiToken")
	for _, t := range apiTokens.Tokens {
		if token.IsEqual(t) {
			apiToken = &t
			continue
		}

		log.WithValues("token", t.TokenName, "created_at", t.CreatedAt).Info(fmt.Sprintf("Token is outdated in Unleash. Token diff: %s", token.Diff(t)))

		apiTokenDeletedCounter.WithLabelValues(token.Namespace, token.Name).Inc()
		span.AddEvent(fmt.Sprintf("Deleting old token for %s created at %s in Unleash", t.TokenName, t.CreatedAt))

		if !r.ApiTokenUpdateEnabled {
			log.WithValues("token", t.TokenName, "created_at", t.CreatedAt).Info("Token update is disabled in operator config")

			// Set ApiToken so we don't create a new token if update is disabled
			if apiToken == nil {
				apiToken = &t
			}

			continue
		}

		log.WithValues("token", t.TokenName, "created_at", t.CreatedAt).Info("Deleting token in Unleash for ApiToken")
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
		apiToken, err = apiClient.CreateAPIToken(ctx, token.ApiTokenRequest(r.ApiTokenNameSuffix))
		if err != nil {
			if err := r.updateStatusFailed(ctx, token, err, "TokenCreationFailed", "Failed to create token in Unleash"); err != nil {
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
	return ctrl.Result{RequeueAfter: 1 * time.Hour}, err
}

// getUnleashInstance returns the Unleash instance that the ApiToken belongs to
func (r *ApiTokenReconciler) getUnleashInstance(ctx context.Context, token *unleashv1.ApiToken) (resources.UnleashInstance, error) {
	if token.Spec.UnleashInstance.ApiVersion != unleashv1.GroupVersion.String() {
		return nil, fmt.Errorf("unsupported api version: %s", token.Spec.UnleashInstance.ApiVersion)
	}

	if token.Spec.UnleashInstance.Kind == "Unleash" {
		unleash := &unleashv1.Unleash{}
		if err := r.Get(ctx, types.NamespacedName{Name: token.Spec.UnleashInstance.Name, Namespace: token.Namespace}, unleash); err != nil {
			return nil, err
		}

		return unleash, nil
	} else if token.Spec.UnleashInstance.Kind == "RemoteUnleash" {
		unleash := &unleashv1.RemoteUnleash{}
		if err := r.Get(ctx, types.NamespacedName{Name: token.Spec.UnleashInstance.Name, Namespace: token.Namespace}, unleash); err != nil {
			return nil, err
		}

		return unleash, nil
	} else {
		return nil, fmt.Errorf("unsupported api kind: %s", token.Spec.UnleashInstance.Kind)
	}
}

// updateStatusSuccess will set the status condition as created and reset the failed status condition
func (r *ApiTokenReconciler) updateStatusSuccess(ctx context.Context, apiToken *unleashv1.ApiToken) error {
	log := log.FromContext(ctx).WithName("apitoken")
	log.Info("Successfully created ApiToken")

	apiTokenStatus.WithLabelValues(apiToken.Namespace, apiToken.Name, unleashv1.ApiTokenStatusConditionTypeCreated).Set(1.0)
	apiTokenStatus.WithLabelValues(apiToken.Namespace, apiToken.Name, unleashv1.ApiTokenStatusConditionTypeFailed).Set(0.0)

	if err := r.Get(ctx, apiToken.NamespacedName(), apiToken); err != nil {
		log.Error(err, "Failed to get ApiToken")
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

	if err := r.Status().Update(ctx, apiToken); err != nil {
		log.Error(err, "Failed to update status for ApiToken")
		return err
	}

	return nil
}

// updateStatusFailed will set the status condition as failed, but not reset the created status condition
func (r *ApiTokenReconciler) updateStatusFailed(ctx context.Context, apiToken *unleashv1.ApiToken, err error, reason, message string) error {
	log := log.FromContext(ctx).WithName("apitoken")

	if err != nil {
		log.Error(err, fmt.Sprintf("%s for ApiToken", message))
	} else {
		log.Info(fmt.Sprintf("%s for ApiToken", message))
	}

	apiTokenStatus.WithLabelValues(apiToken.Namespace, apiToken.Name, unleashv1.ApiTokenStatusConditionTypeFailed).Set(1.0)

	if err := r.Get(ctx, apiToken.NamespacedName(), apiToken); err != nil {
		log.Error(err, "Failed to get ApiToken")
		return err
	}

	meta.SetStatusCondition(&apiToken.Status.Conditions, metav1.Condition{
		Type:    unleashv1.ApiTokenStatusConditionTypeFailed,
		Status:  metav1.ConditionTrue,
		Reason:  reason,
		Message: message,
	})

	apiToken.Status.Failed = true

	if err := r.Status().Update(ctx, apiToken); err != nil {
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

// SetupWithManager sets up the controller with the Manager.
func (r *ApiTokenReconciler) SetupWithManager(mgr ctrl.Manager) error {
	pred := predicate.GenerationChangedPredicate{}
	return ctrl.NewControllerManagedBy(mgr).
		For(&unleashv1.ApiToken{}).
		WithEventFilter(pred).
		Complete(r)
}
