package controllers

import (
	"context"
	"fmt"

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
)

const tokenFinalizer = "unleash.nais.io/finalizer"

const (
	// typeCreatedToken is the event reason for a created token
	typeCreatedToken = "Created"

	// typeDeletedToken is the event reason for a deleted token
	typeDeletedToken = "Deleted"

	// typeFailedToken is the event reason for a failed token
	typeFailedToken = "Failed"
)

// ApiTokenReconciler reconciles a ApiToken object
type ApiTokenReconciler struct {
	client.Client
	Scheme            *runtime.Scheme
	Recorder          record.EventRecorder
	OperatorNamespace string
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
	unleash := &unleashv1.Unleash{}
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
			Type:    typeCreatedToken,
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
			return ctrl.Result{Requeue: true}, err
		}

		if err = r.Update(ctx, token); err != nil {
			log.Error(err, "Failed to update ApiToken to add finalizer")
			return ctrl.Result{}, err
		}
	}

	// Check if marked for deletion
	if token.GetDeletionTimestamp() != nil {
		if controllerutil.ContainsFinalizer(token, tokenFinalizer) {
			log.Info("Performing Finalizer Operations for ApiToken before deletion")

			meta.SetStatusCondition(&token.Status.Conditions, metav1.Condition{
				Type:    typeDeletedToken,
				Status:  metav1.ConditionUnknown,
				Reason:  "Finalizing",
				Message: "Performing finalizer operations",
			})

			if err = r.Status().Update(ctx, token); err != nil {
				log.Error(err, "Failed to update ApiToken status")
				return ctrl.Result{}, err
			}

			r.doFinalizerOperationsForToken(token)

			if err := r.Get(ctx, req.NamespacedName, token); err != nil {
				log.Error(err, "Failed to get ApiToken")
				return ctrl.Result{}, err
			}

			meta.SetStatusCondition(&token.Status.Conditions, metav1.Condition{
				Type:    typeDeletedToken,
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
				return ctrl.Result{Requeue: true}, err
			}

			if err = r.Update(ctx, token); err != nil {
				log.Error(err, "Failed to update ApiToken to remove finalizer")
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Get Unleash instance
	if token.Spec.UnleashInstance.ApiVersion != unleashv1.GroupVersion.String() {
		return ctrl.Result{}, fmt.Errorf("unsupported api version: %s", token.Spec.UnleashInstance.ApiVersion)
	}
	if token.Spec.UnleashInstance.Kind == "Unleash" {
		adminToken, unleashUrl, err := r.getAdminTokenForUnleash(ctx, token, unleash)
	} else if token.Spec.UnleashInstance.Kind == "RemoteUnleash" {
		adminToken, unleashUrl, err := r.getAdminTokenForRemoteUnleash(ctx, token)
	} else {
		return ctrl.Result{}, fmt.Errorf("unsupported api kind: %s", token.Spec.UnleashInstance.Kind)
	}

	if err := r.Get(ctx, types.NamespacedName{Name: token.Spec.UnleashInstance.Name, Namespace: token.Namespace}, &unleashv1.Unleash{}); err != nil {
		if apierrors.IsNotFound(err) {
			meta.SetStatusCondition(&token.Status.Conditions, metav1.Condition{
				Type:    typeCreatedToken,
				Status:  metav1.ConditionFalse,
				Reason:  "UnleashNotFound",
				Message: "Unleash resource not found",
			})
			meta.SetStatusCondition(&token.Status.Conditions, metav1.Condition{
				Type:    typeFailedToken,
				Status:  metav1.ConditionTrue,
				Reason:  "UnleashNotFound",
				Message: "Unleash resource not found",
			})
			if err = r.Status().Update(ctx, token); err != nil {
				log.Error(err, "Failed to update ApiToken status")
				return ctrl.Result{Requeue: true}, err
			}

			return ctrl.Result{}, nil
		}

		log.Error(err, "Failed to get Unleash resource")
		return ctrl.Result{Requeue: true}, err
	}

	if !unleash.IsReady() {
		if err := r.Get(ctx, req.NamespacedName, token); err != nil {
			log.Error(err, "Failed to get ApiToken")
			return ctrl.Result{}, err
		}

		meta.SetStatusCondition(&token.Status.Conditions, metav1.Condition{
			Type:    typeCreatedToken,
			Status:  metav1.ConditionFalse,
			Reason:  "UnleashNotReady",
			Message: "Unleash resource not ready",
		})
		if err = r.Status().Update(ctx, token); err != nil {
			log.Error(err, "Failed to update ApiToken status")
			return ctrl.Result{Requeue: true}, err
		}

		return ctrl.Result{Requeue: true}, nil
	}

	// Get Unleash admin API token
	adminToken, err := unleash.GetAdminToken(ctx, r.Client, r.OperatorNamespace)
	if err != nil {
		if apierrors.IsNotFound(err) {
			meta.SetStatusCondition(&token.Status.Conditions, metav1.Condition{
				Type:    typeCreatedToken,
				Status:  metav1.ConditionFalse,
				Reason:  "UnleashSecretNotFound",
				Message: "Unleash admin secret not found",
			})
			if err = r.Status().Update(ctx, token); err != nil {
				log.Error(err, "Failed to update ApiToken status")
				return ctrl.Result{Requeue: true}, err
			}

			return ctrl.Result{Requeue: true}, nil
		}

		log.Error(err, "Failed to get Unleash admin secret")
		return ctrl.Result{Requeue: true}, err
	}

	if adminToken == nil || len(adminToken) == 0 {
		log.Error(err, "Unleash admin secret does not contain an api key")
		meta.SetStatusCondition(&token.Status.Conditions, metav1.Condition{
			Type:    typeCreatedToken,
			Status:  metav1.ConditionFalse,
			Reason:  "UnleashSecretMissingApiKey",
			Message: "Unleash admin secret is empty",
		})
		if err = r.Status().Update(ctx, token); err != nil {
			log.Error(err, "Failed to update ApiToken status")
			return ctrl.Result{Requeue: true}, err
		}

		return ctrl.Result{Requeue: true}, nil
	}

	client, err := unleashclient.NewClient(unleash.GetURL(), string(adminToken))
	if err != nil {
		log.Error(err, "Failed to create Unleash client")
		meta.SetStatusCondition(&token.Status.Conditions, metav1.Condition{
			Type:    typeCreatedToken,
			Status:  metav1.ConditionFalse,
			Reason:  "FailedToCreateUnleashClient",
			Message: "Failed to create Unleash client",
		})
		if err = r.Status().Update(ctx, token); err != nil {
			log.Error(err, "Failed to update ApiToken status")
			return ctrl.Result{Requeue: true}, err
		}
	}

	exists, err := client.CheckAPITokenExists(token.GetObjectMeta().GetName())
	if err != nil {
		log.Error(err, "Failed to check if token exists")
		meta.SetStatusCondition(&token.Status.Conditions, metav1.Condition{
			Type:    typeCreatedToken,
			Status:  metav1.ConditionFalse,
			Reason:  "FailedToCheckIfTokenExists",
			Message: "Failed to check if token exists",
		})
		if err = r.Status().Update(ctx, token); err != nil {
			log.Error(err, "Failed to update ApiToken status")
			return ctrl.Result{Requeue: true}, err
		}

		return ctrl.Result{Requeue: true}, err
	}

	if !exists {
		apiToken, err := client.CreateAPIToken(unleashclient.APITokenRequest{
			Username:    token.GetObjectMeta().GetName(),
			Type:        token.Spec.Type,
			Environment: token.Spec.Environment,
			Projects:    token.Spec.Projects,
		})
		if err != nil {
			log.Error(err, "Failed to create token")
			meta.SetStatusCondition(&token.Status.Conditions, metav1.Condition{
				Type:    typeCreatedToken,
				Status:  metav1.ConditionFalse,
				Reason:  "FailedToCreateToken",
				Message: "Failed to create token",
			})
			if err = r.Status().Update(ctx, token); err != nil {
				log.Error(err, "Failed to update ApiToken status")
				return ctrl.Result{Requeue: true}, err
			}
		}

		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      token.Spec.SecretName,
				Namespace: token.GetObjectMeta().GetNamespace(),
			},
			Data: map[string][]byte{
				"token": []byte(apiToken.Secret),
			},
		}

		if err := r.Delete(ctx, secret); err != nil && !apierrors.IsNotFound(err) {
			log.Error(err, "Failed to delete secret")
			meta.SetStatusCondition(&token.Status.Conditions, metav1.Condition{
				Type:    typeCreatedToken,
				Status:  metav1.ConditionFalse,
				Reason:  "FailedToDeleteExistingSecret",
				Message: "Failed to delete existing secret",
			})
			if err = r.Status().Update(ctx, token); err != nil {
				log.Error(err, "Failed to update ApiToken status")
				return ctrl.Result{Requeue: true}, err
			}
		}

		if err := r.Create(ctx, secret); err != nil {
			log.Error(err, "Failed to create secret")
			meta.SetStatusCondition(&token.Status.Conditions, metav1.Condition{
				Type:    typeCreatedToken,
				Status:  metav1.ConditionFalse,
				Reason:  "FailedToCreateSecret",
				Message: "Failed to create secret",
			})
			if err = r.Status().Update(ctx, token); err != nil {
				log.Error(err, "Failed to update ApiToken status")
				return ctrl.Result{Requeue: true}, err
			}
		}

		meta.SetStatusCondition(&token.Status.Conditions, metav1.Condition{
			Type:    typeCreatedToken,
			Status:  metav1.ConditionTrue,
			Reason:  "CreatedToken",
			Message: "Created token",
		})
		if err = r.Status().Update(ctx, token); err != nil {
			log.Error(err, "Failed to update ApiToken status")
			return ctrl.Result{Requeue: true}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *ApiTokenReconciler) getAdminTokenForUnleash(ctx context.Context, token *unleashv1.ApiToken, unleash *unleashv1.Unleash) (adminToken []byte, url string, err error) {
	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: token.Spec.UnleashInstance.Name, Namespace: unleash.Namespace}, secret); err != nil {
		return nil, "", nil
	}

	return secret.Data["token"], unleash.GetURL(), nil
}

func (r *ApiTokenReconciler) getAdminTokenForRemoteUnleash(ctx context.Context, token *unleashv1.ApiToken) (adminToken []byte, url string, err error) {
	unleash := &unleashv1.RemoteUnleash{}
	if err = r.Get(ctx, types.NamespacedName{Name: token.Spec.UnleashInstance.Name, Namespace: token.ObjectMeta.Namespace}, unleash); err != nil {
		return
	}

	adminToken, err = unleash.GetAdminToken(ctx, r.Client, r.OperatorNamespace)
	if err != nil {
		return
	}

	return adminToken, unleash.GetURL(), nil
}

func (r *ApiTokenReconciler) doFinalizerOperationsForToken(token *unleashv1.ApiToken) {

}

// SetupWithManager sets up the controller with the Manager.
func (r *ApiTokenReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&unleashv1.ApiToken{}).
		Complete(r)
}
