package controllers

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"github.com/nais/unleasherator/pkg/unleash"
	corev1 "k8s.io/api/core/v1"
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

	unleashv1 "github.com/nais/unleasherator/api/v1"
	unleashclient "github.com/nais/unleasherator/pkg/unleash"
	"github.com/prometheus/client_golang/prometheus"
)

const tokenFinalizer = "unleash.nais.io/finalizer"

var (
	// apiTokenStatus is a Prometheus metric which will be used to expose the status of the Unleash instances
	apiTokenStatus = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "unleasherator_unleash_status",
			Help: "Status of Unleash instances",
		},
		[]string{"namespace", "name", "status"},
	)
)

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
}

//+kubebuilder:rbac:groups=unleash.nais.io,resources=apitokens,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=unleash.nais.io,resources=apitokens/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=unleash.nais.io,resources=apitokens/finalizers,verbs=update
//+kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the ApiToken object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.13.0/pkg/reconcile
func (r *ApiTokenReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	token := &unleashv1.ApiToken{}
	err := r.Get(ctx, req.NamespacedName, token)

	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("ApiToken resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get ApiToken")
		return ctrl.Result{}, err
	}

	// Set status to unknown if not set
	if token.Status.Conditions == nil || len(token.Status.Conditions) == 0 {
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
	}

	// Refetch after status update
	if err := r.Get(ctx, req.NamespacedName, token); err != nil {
		log.Error(err, "Failed to get ApiToken")
		return ctrl.Result{}, err
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(token, tokenFinalizer) {
		log.Info("Adding finalizer to ApiToken")
		if ok := controllerutil.AddFinalizer(token, tokenFinalizer); !ok {
			log.Error(err, "Failed to add finalizer to ApiToken")
			return ctrl.Result{}, err
		}

		if err = r.Update(ctx, token); err != nil {
			log.Error(err, "Failed to update ApiToken to add finalizer")
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

		return ctrl.Result{Requeue: true}, nil
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
	if token.GetDeletionTimestamp() != nil {
		if controllerutil.ContainsFinalizer(token, tokenFinalizer) {
			log.Info("Performing Finalizer Operations for ApiToken before deletion")
			r.doFinalizerOperationsForToken(token, apiClient, log)

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

			if err := r.Get(ctx, req.NamespacedName, token); err != nil {
				log.Error(err, "Failed to get ApiToken")
				return ctrl.Result{}, err
			}

			meta.SetStatusCondition(&token.Status.Conditions, metav1.Condition{
				Type:    unleashv1.ApiTokenStatusConditionTypeDeleted,
				Status:  metav1.ConditionTrue,
				Reason:  "Finalizing",
				Message: "Finalizer operations completed",
			})

			if err := r.Status().Update(ctx, token); err != nil {
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

	// Check if token exists in Unleash
	apiToken, err := apiClient.GetAPIToken(token.UnleashClientName(r.ApiTokenNameSuffix))
	if err != nil {
		if err := r.updateStatusFailed(ctx, token, err, "TokenCheckFailed", "Failed to check if token exists"); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	}

	// Create token if it does not exist in Unleash
	if apiToken == nil {
		apiToken, err = apiClient.CreateAPIToken(unleashclient.ApiTokenRequest{
			Username:    token.UnleashClientName(r.ApiTokenNameSuffix),
			Type:        token.Spec.Type,
			Environment: token.Spec.Environment,
			Projects:    token.Spec.Projects,
		})
		if err != nil {
			if err := r.updateStatusFailed(ctx, token, err, "TokenCreationFailed", "Failed to created token"); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, err
		}
	}

	// Kubernetes secret with token
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      token.Spec.SecretName,
			Namespace: token.GetObjectMeta().GetNamespace(),
		},
		Data: map[string][]byte{
			unleashv1.ApiTokenSecretTokenEnv:  []byte(apiToken.Secret),
			unleashv1.ApiTokenSecretServerEnv: []byte(unleash.URL()),
		},
	}

	if err := controllerutil.SetControllerReference(token, secret, r.Scheme); err != nil {
		log.Error(err, "Failed to set controller reference on secret for ApiToken")
		return ctrl.Result{}, err
	}

	// Delete existing token secret if it exists
	if err := r.Delete(ctx, secret); err != nil && !apierrors.IsNotFound(err) {
		if err := r.updateStatusFailed(ctx, token, err, "TokenSecretFailed", "Failed to delete existing token secret"); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	}

	// Create new token secret in Kubernetes
	if err := r.Create(ctx, secret); err != nil {
		if err := r.updateStatusFailed(ctx, token, err, "TokenSecretFailed", "Failed to create token secret"); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	}

	// Set ApiToken status to success
	err = r.updateStatusSuccess(ctx, token)
	return ctrl.Result{}, err
}

// getUnleashInstance returns the Unleash instance that the ApiToken belongs to
func (r *ApiTokenReconciler) getUnleashInstance(ctx context.Context, token *unleashv1.ApiToken) (UnleashInstance, error) {
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
	log := log.FromContext(ctx)
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
	log := log.FromContext(ctx)

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
func (r *ApiTokenReconciler) doFinalizerOperationsForToken(token *unleashv1.ApiToken, unleashClient *unleash.Client, log logr.Logger) {
	tokenName := token.UnleashClientName(r.ApiTokenNameSuffix)
	err := unleashClient.DeleteApiToken(tokenName)
	if err != nil {
		log.Error(err, fmt.Sprintf("Failed to delete ApiToken %s from Unleash", tokenName))
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *ApiTokenReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&unleashv1.ApiToken{}).
		Complete(r)
}
