package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/nais/unleasherator/internal/config"
	"github.com/nais/unleasherator/internal/federation"
	"github.com/nais/unleasherator/internal/o11y"
	"github.com/nais/unleasherator/internal/pb"
	"github.com/nais/unleasherator/internal/unleashclient"
	"github.com/nais/unleasherator/internal/utils"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

var (
	// RemoteUnleash controller timeouts - prefixed to avoid conflicts with other controllers
	remoteUnleashErrorRetryDelay = 1 * time.Minute
	remoteUnleashRequeueAfter    = 1 * time.Hour
	remoteUnleashRequeueJitter   = 10 * time.Minute // Jitter to spread reconciliations

	// remoteUnleashStatus is a Prometheus metric which will be used to expose the status of the RemoteUnleash instances
	remoteUnleashStatus = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "unleasherator_remoteunleash_status",
			Help: "Status of RemoteUnleash instances",
		},
		[]string{"namespace", "name", "status"},
	)

	remoteUnleashReceived = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "unleasherator_federation_received_total",
			Help: "Number of Unleash federation messages received with status",
		},
		[]string{"state", "status"},
	)
)

func init() {
	metrics.Registry.MustRegister(remoteUnleashStatus, remoteUnleashReceived)
}

// RemoteUnleashReconciler reconciles a RemoteUnleash object
type RemoteUnleashReconciler struct {
	client.Client
	Scheme                      *runtime.Scheme
	Recorder                    record.EventRecorder
	OperatorNamespace           string
	Timeout                     config.TimeoutConfig
	Federation                  RemoteUnleashFederation
	AllowLegacyNameBoundSecrets bool
	Tracer                      trace.Tracer
}

type RemoteUnleashFederation struct {
	Enabled     bool
	ClusterName string
	Subscriber  federation.Subscriber
}

//+kubebuilder:rbac:groups=unleash.nais.io,resources=remoteunleashes,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=unleash.nais.io,resources=remoteunleashes/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=unleash.nais.io,resources=remoteunleashes/finalizers,verbs=update
//+kubebuilder:rbac:groups=core,resources=secrets,verbs=get;create;update;delete

func (r *RemoteUnleashReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	spanOpts := o11y.ReconcilerAttributes(ctx, req)
	ctx, span := r.Tracer.Start(ctx, "Reconcile RemoteUnleash", spanOpts...)
	defer span.End()

	log := log.FromContext(ctx).WithName("remoteunleash").WithValues("TraceID", span.SpanContext().TraceID())
	log.Info("Starting reconciliation of RemoteUnleash")

	remoteUnleash := &unleashv1.RemoteUnleash{}
	err := r.Get(ctx, req.NamespacedName, remoteUnleash)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("RemoteUnleash resource not found. Ignoring since object must be deleted")
			remoteUnleashStatus.DeleteLabelValues(req.Namespace, req.Name, unleashv1.UnleashStatusConditionTypeReconciled)
			remoteUnleashStatus.DeleteLabelValues(req.Namespace, req.Name, unleashv1.UnleashStatusConditionTypeConnected)
			return ctrl.Result{Requeue: false}, nil
		}
		log.Error(err, "Failed to get RemoteUnleash")
		return ctrl.Result{}, err
	}

	// Check if marked for deletion - handle this early to allow deletion even if Unleash server is down
	if remoteUnleash.GetDeletionTimestamp() != nil {
		log.Info("RemoteUnleash marked for deletion")
		if controllerutil.ContainsFinalizer(remoteUnleash, tokenFinalizer) {
			log.Info("Performing Finalizer Operations for RemoteUnleash before deletion")

			// Try to update status, but don't block deletion if it fails
			_ = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
				if err := r.Get(ctx, req.NamespacedName, remoteUnleash); err != nil {
					return err
				}
				meta.SetStatusCondition(&remoteUnleash.Status.Conditions, metav1.Condition{
					Type:    unleashv1.UnleashStatusConditionTypeDegraded,
					Status:  metav1.ConditionUnknown,
					Reason:  "Finalizing",
					Message: "Performing finalizer operations",
				})
				return r.Status().Update(ctx, remoteUnleash)
			})

			// Perform finalizer operations - currently a no-op but allows for future cleanup
			r.doFinalizerOperationsForToken(remoteUnleash)

			// Remove the finalizer to allow deletion to proceed
			log.Info("Removing finalizer from RemoteUnleash")
			err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
				if err := r.Get(ctx, req.NamespacedName, remoteUnleash); err != nil {
					return err
				}
				if !controllerutil.ContainsFinalizer(remoteUnleash, tokenFinalizer) {
					return nil // Already removed
				}
				controllerutil.RemoveFinalizer(remoteUnleash, tokenFinalizer)
				return r.Update(ctx, remoteUnleash)
			})
			if err != nil {
				log.Error(err, "Failed to update RemoteUnleash to remove finalizer")
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{Requeue: false}, nil
	}

	// Set status to unknown if no status is set
	if len(remoteUnleash.Status.Conditions) == 0 {
		log.Info("Setting status to unknown for RemoteUnleash")

		err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
			if err := r.Get(ctx, req.NamespacedName, remoteUnleash); err != nil {
				return err
			}
			if len(remoteUnleash.Status.Conditions) > 0 {
				return nil // Already has conditions
			}
			meta.SetStatusCondition(&remoteUnleash.Status.Conditions, metav1.Condition{
				Type:    unleashv1.UnleashStatusConditionTypeReconciled,
				Status:  metav1.ConditionUnknown,
				Reason:  "Reconciling",
				Message: "Starting reconciliation",
			})
			return r.Status().Update(ctx, remoteUnleash)
		})
		if err != nil {
			log.Error(err, "Failed to update RemoteUnleash status")
			return ctrl.Result{}, err
		}

		if err := r.Get(ctx, req.NamespacedName, remoteUnleash); err != nil {
			log.Error(err, "Failed to get RemoteUnleash")
			return ctrl.Result{}, err
		}
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(remoteUnleash, tokenFinalizer) {
		log.Info("Adding finalizer to RemoteUnleash")

		err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
			if err := r.Get(ctx, req.NamespacedName, remoteUnleash); err != nil {
				return err
			}
			if controllerutil.ContainsFinalizer(remoteUnleash, tokenFinalizer) {
				return nil // Already has finalizer
			}
			controllerutil.AddFinalizer(remoteUnleash, tokenFinalizer)
			return r.Update(ctx, remoteUnleash)
		})
		if err != nil {
			log.Error(err, "Failed to update RemoteUnleash to add finalizer")
			return ctrl.Result{}, err
		}

		if err := r.Get(ctx, req.NamespacedName, remoteUnleash); err != nil {
			log.Error(err, "Failed to get RemoteUnleash")
			return ctrl.Result{}, err
		}
	}

	// Determine whether the admin secret lives in a different namespace than the
	// RemoteUnleash. Cross-namespace references are the confused-deputy surface:
	// the controller reads secrets with elevated privileges on the tenant's behalf.
	adminSecretNamespace := remoteUnleash.Spec.AdminSecret.Namespace
	isCrossNamespace := adminSecretNamespace != "" && adminSecretNamespace != remoteUnleash.Namespace

	// Cross-namespace references are only ever permitted to the operator namespace.
	if isCrossNamespace && adminSecretNamespace != r.OperatorNamespace {
		err := fmt.Errorf("cross-namespace secret references are only permitted to the operator namespace for security reasons")
		if updateErr := r.updateStatusReconcileFailed(ctx, remoteUnleash, nil, err, "Validation failed"); updateErr != nil {
			return ctrl.Result{}, updateErr
		}
		// Do not requeue, as this is a terminal configuration error until the user fixes it
		return ctrl.Result{}, nil
	}

	// Fetch the referenced admin secret ONCE. Both the URL validation value and the
	// admin token are derived from this single fetched object to avoid a second live
	// (uncached) API round-trip and the resulting TOCTOU window.
	adminSecret := &corev1.Secret{}
	if err := r.Get(ctx, remoteUnleash.AdminSecretNamespacedName(), adminSecret); err != nil {
		if err := r.updateStatusReconcileFailed(ctx, remoteUnleash, nil, err, "Failed to get admin token secret"); err != nil {
			return ctrl.Result{}, err
		}
		if apierrors.IsNotFound(err) {
			return ctrl.Result{RequeueAfter: remoteUnleashErrorRetryDelay}, nil
		}
		return ctrl.Result{}, err
	}

	authorizedNamespace, hasAuthorizedNamespace := adminSecret.Annotations[unleashv1.UnleashSecretAuthorizedNamespaceAnnotation]

	// Authorization gate for cross-namespace (operator namespace) references.
	// The authorized-namespace annotation is the PRIMARY, authoritative control and
	// cannot be bypassed by crafting the RemoteUnleash name. Name-based matching
	// survives only as gated legacy defense-in-depth for annotation-less secrets.
	if isCrossNamespace {
		switch {
		case hasAuthorizedNamespace:
			if authorizedNamespace != remoteUnleash.Namespace {
				err := fmt.Errorf("admin secret is authorized for namespace %q, not %q", authorizedNamespace, remoteUnleash.Namespace)
				if updateErr := r.updateStatusReconcileFailed(ctx, remoteUnleash, nil, err, "Validation failed"); updateErr != nil {
					return ctrl.Result{}, updateErr
				}
				return ctrl.Result{}, nil
			}
		case r.AllowLegacyNameBoundSecrets:
			// Legacy fallback (defense-in-depth only): annotation-less secrets created by
			// the old federation subscriber or by bash scripts. Security for these rests
			// on the random nonce (Fix 3) and on RBAC governing who may create secrets in
			// the operator namespace. We accept any previously-recognized name bound to the
			// instance name (including the exact "<prefix>-<name>-admin-key" without a
			// nonce) so migration does not hard-break existing tenants.
			if !secretNameBoundToInstance(remoteUnleash.Spec.AdminSecret.Name, remoteUnleash.Name) {
				err := fmt.Errorf("cross-namespace secret name must be bound to the RemoteUnleash instance name %q", remoteUnleash.Name)
				if updateErr := r.updateStatusReconcileFailed(ctx, remoteUnleash, nil, err, "Validation failed"); updateErr != nil {
					return ctrl.Result{}, updateErr
				}
				return ctrl.Result{}, nil
			}
		default:
			// AllowLegacyNameBoundSecrets disabled: only annotation-bearing secrets whose
			// annotation matches the tenant namespace may be referenced cross-namespace.
			err := fmt.Errorf("cross-namespace admin secret must carry the %s annotation authorizing namespace %q", unleashv1.UnleashSecretAuthorizedNamespaceAnnotation, remoteUnleash.Namespace)
			if updateErr := r.updateStatusReconcileFailed(ctx, remoteUnleash, nil, err, "Validation failed"); updateErr != nil {
				return ctrl.Result{}, updateErr
			}
			return ctrl.Result{}, nil
		}
	}

	// Validate the Server URL against the secret to prevent exfiltration (Server URL
	// spoofing). Secured secrets MUST fail closed: whenever the secret carries the
	// authorized-namespace annotation (a managed secret) OR the legacy fallback is
	// disabled, the url key must be present and must match. Only genuinely-legacy
	// annotation-less secrets under AllowLegacyNameBoundSecrets may omit it.
	expectedURL := adminSecret.Data[unleashv1.UnleashSecretServerURLKey]
	urlRequired := hasAuthorizedNamespace || !r.AllowLegacyNameBoundSecrets
	if urlRequired && len(expectedURL) == 0 {
		err := fmt.Errorf("admin secret is missing the required %q key for Server URL validation", unleashv1.UnleashSecretServerURLKey)
		if updateErr := r.updateStatusReconcileFailed(ctx, remoteUnleash, nil, err, "Server URL validation failed"); updateErr != nil {
			return ctrl.Result{}, updateErr
		}
		// Do not requeue, as this is a terminal configuration error until the user fixes it
		return ctrl.Result{}, nil
	}
	if len(expectedURL) > 0 && remoteUnleash.Spec.Server.URL != string(expectedURL) {
		err := fmt.Errorf("validation failed: RemoteUnleash Server URL (%s) does not match the authorized URL in the admin secret", remoteUnleash.Spec.Server.URL)
		if updateErr := r.updateStatusReconcileFailed(ctx, remoteUnleash, nil, err, "Server URL validation failed"); updateErr != nil {
			return ctrl.Result{}, updateErr
		}
		// Do not requeue, as this is a terminal configuration error until the user fixes it
		return ctrl.Result{}, nil
	}

	// Derive the admin token from the already-fetched secret (no second Get),
	// preserving AdminToken's key/defaulting behavior.
	adminToken := adminSecret.Data[remoteUnleash.Spec.AdminSecret.Key]

	// Check admin token
	if len(adminToken) == 0 {
		err := fmt.Errorf("admin token secret %q does not contain a token under key %q", remoteUnleash.Spec.AdminSecret.Name, remoteUnleash.Spec.AdminSecret.Key)
		if updateErr := r.updateStatusReconcileFailed(ctx, remoteUnleash, nil, err, "Admin token is empty"); updateErr != nil {
			return ctrl.Result{}, updateErr
		}

		return ctrl.Result{}, nil
	}

	// Create Unleash API client
	unleashClient, err := unleashclient.NewClient(remoteUnleash.Spec.Server.URL, string(adminToken))
	if err != nil {
		if err := r.updateStatusReconcileFailed(ctx, remoteUnleash, nil, err, "Failed to create Unleash client"); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, err
	}

	stats, _, err := unleashClient.GetInstanceAdminStats(ctx)
	if err != nil {
		if err := r.updateStatusConnectionFailed(ctx, remoteUnleash, stats, err, fmt.Sprintf("Failed to connect to Unleash instance statistics endpoint on host %s", remoteUnleash.URL())); err != nil {
			return ctrl.Result{}, err
		}

		// Requeue after 1 minute if we failed to connect to Unleash
		return ctrl.Result{}, err
	}

	// Set RemoteUnleash status to reconciled and connected in a single update
	err = r.updateStatusSuccess(ctx, stats, remoteUnleash)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: utils.RequeueAfterWithJitter(remoteUnleashRequeueAfter, remoteUnleashRequeueJitter)}, nil
}

func (r *RemoteUnleashReconciler) updateStatusSuccess(ctx context.Context, stats *unleashclient.InstanceAdminStatsResult, remoteUnleash *unleashv1.RemoteUnleash) error {
	log := log.FromContext(ctx).WithName("remoteunleash")

	log.Info("Successfully reconciled and connected to Unleash")

	// Get fresh copy before updating
	if err := r.Get(ctx, remoteUnleash.NamespacedName(), remoteUnleash); err != nil {
		log.Error(err, "Failed to get RemoteUnleash")
		return err
	}

	// Set version from stats
	if stats != nil {
		if stats.VersionEnterprise != "" {
			remoteUnleash.Status.Version = stats.VersionEnterprise
		} else {
			remoteUnleash.Status.Version = stats.VersionOSS
		}
	}

	// Set both statuses
	remoteUnleash.Status.Reconciled = true
	remoteUnleash.Status.Connected = true

	// Update metrics
	remoteUnleashStatus.WithLabelValues(remoteUnleash.Namespace, remoteUnleash.Name, unleashv1.UnleashStatusConditionTypeReconciled).Set(1)
	remoteUnleashStatus.WithLabelValues(remoteUnleash.Namespace, remoteUnleash.Name, unleashv1.UnleashStatusConditionTypeConnected).Set(1)

	// Set both conditions
	meta.SetStatusCondition(&remoteUnleash.Status.Conditions, metav1.Condition{
		Type:    unleashv1.UnleashStatusConditionTypeReconciled,
		Status:  metav1.ConditionTrue,
		Reason:  "Reconciling",
		Message: "Reconciled successfully",
	})
	meta.SetStatusCondition(&remoteUnleash.Status.Conditions, metav1.Condition{
		Type:    unleashv1.UnleashStatusConditionTypeConnected,
		Status:  metav1.ConditionTrue,
		Reason:  "Reconciling",
		Message: "Successfully connected to Unleash",
	})

	// Single status update
	if err := r.Status().Update(ctx, remoteUnleash); err != nil {
		log.Error(err, "Failed to update status for RemoteUnleash")
		return err
	}

	return nil
}

func (r *RemoteUnleashReconciler) updateStatusConnectionFailed(ctx context.Context, remoteUnleash *unleashv1.RemoteUnleash, stats *unleashclient.InstanceAdminStatsResult, err error, message string) error {
	log := log.FromContext(ctx).WithName("remoteunleash")

	log.Error(err, fmt.Sprintf("%s for Unleash", message))

	// Get fresh copy before updating
	if err := r.Get(ctx, remoteUnleash.NamespacedName(), remoteUnleash); err != nil {
		log.Error(err, "Failed to get RemoteUnleash")
		return err
	}

	// Set version from stats if available
	if stats != nil {
		if stats.VersionEnterprise != "" {
			remoteUnleash.Status.Version = stats.VersionEnterprise
		} else {
			remoteUnleash.Status.Version = stats.VersionOSS
		}
	}

	// Reconciled succeeded (we got this far), but connection failed
	remoteUnleash.Status.Reconciled = true
	remoteUnleash.Status.Connected = false

	// Update metrics
	remoteUnleashStatus.WithLabelValues(remoteUnleash.Namespace, remoteUnleash.Name, unleashv1.UnleashStatusConditionTypeReconciled).Set(1)
	remoteUnleashStatus.WithLabelValues(remoteUnleash.Namespace, remoteUnleash.Name, unleashv1.UnleashStatusConditionTypeConnected).Set(0)

	// Set both conditions in single update
	meta.SetStatusCondition(&remoteUnleash.Status.Conditions, metav1.Condition{
		Type:    unleashv1.UnleashStatusConditionTypeReconciled,
		Status:  metav1.ConditionTrue,
		Reason:  "Reconciling",
		Message: "Reconciled successfully",
	})
	meta.SetStatusCondition(&remoteUnleash.Status.Conditions, metav1.Condition{
		Type:    unleashv1.UnleashStatusConditionTypeConnected,
		Status:  metav1.ConditionFalse,
		Reason:  "Reconciling",
		Message: message,
	})

	if err := r.Status().Update(ctx, remoteUnleash); err != nil {
		log.Error(err, "Failed to update status for RemoteUnleash")
		return err
	}

	return nil
}

func (r *RemoteUnleashReconciler) updateStatusReconcileFailed(ctx context.Context, remoteUnleash *unleashv1.RemoteUnleash, stats *unleashclient.InstanceAdminStatsResult, err error, message string) error {
	log := log.FromContext(ctx).WithName("remoteunleash")

	log.Error(err, fmt.Sprintf("%s for RemoteUnleash", message))
	return r.updateStatus(ctx, remoteUnleash, stats, metav1.Condition{
		Type:    unleashv1.UnleashStatusConditionTypeReconciled,
		Status:  metav1.ConditionFalse,
		Reason:  "Reconciling",
		Message: message,
	})
}

func (r *RemoteUnleashReconciler) updateStatus(ctx context.Context, remoteUnleash *unleashv1.RemoteUnleash, stats *unleashclient.InstanceAdminStatsResult, status metav1.Condition) error {
	log := log.FromContext(ctx).WithName("remoteunleash")

	val := promGaugeValueForStatus(status.Status)
	remoteUnleashStatus.WithLabelValues(remoteUnleash.Namespace, remoteUnleash.Name, status.Type).Set(val)

	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		if err := r.Get(ctx, remoteUnleash.NamespacedName(), remoteUnleash); err != nil {
			return err
		}

		if stats != nil {
			if stats.VersionEnterprise != "" {
				remoteUnleash.Status.Version = stats.VersionEnterprise
			} else {
				remoteUnleash.Status.Version = stats.VersionOSS
			}
		}

		switch status.Type {
		case unleashv1.UnleashStatusConditionTypeReconciled:
			remoteUnleash.Status.Reconciled = status.Status == metav1.ConditionTrue
		case unleashv1.UnleashStatusConditionTypeConnected:
			remoteUnleash.Status.Connected = status.Status == metav1.ConditionTrue
		}

		meta.SetStatusCondition(&remoteUnleash.Status.Conditions, status)
		return r.Status().Update(ctx, remoteUnleash)
	})

	if err != nil {
		log.Error(err, "Failed to update status for RemoteUnleash")
		return err
	}

	return nil
}

func (r *RemoteUnleashReconciler) doFinalizerOperationsForToken(remoteUnleash *unleashv1.RemoteUnleash) {

}

func (r *RemoteUnleashReconciler) FederationSubscribe(ctx context.Context) error {
	log := log.FromContext(ctx).WithName("subscribe")

	if !r.Federation.Enabled {
		log.Info("Federation is disabled, not consuming pubsub messages")
		return nil
	}

	var permanentError error

	for ctx.Err() == nil && permanentError == nil {
		log.Info("Waiting for pubsub messages")
		err := r.Federation.Subscriber.Subscribe(ctx, func(ctx context.Context, remoteUnleashes []*unleashv1.RemoteUnleash, adminSecrets []*corev1.Secret, clusters []string, status pb.Status) error {
			if len(remoteUnleashes) == 0 {
				log.Info("Received pubsub message with no namespaces, ignoring", "status", status, "clusters", clusters)
				return nil
			}

			log.Info("Received pubsub message", "status", status, "unleash", remoteUnleashes[0].GetName(), "clusters", clusters)

			if !utils.StringInSlice(r.Federation.ClusterName, clusters) {
				log.Info("Ignoring message, not for this cluster", "cluster", r.Federation.ClusterName, "clusters", clusters)
				return nil
			}

			switch status {
			case pb.Status_Removed:
				log.Info("Received Status_Removed, deleting RemoteUnleash resources and secret")

				// Filter safe objects to prevent cross-namespace deletion hijacking
				var safeSecrets []*corev1.Secret
				var safeRUs []*unleashv1.RemoteUnleash

				for i, ru := range remoteUnleashes {
					existingRU := &unleashv1.RemoteUnleash{}
					err := r.Client.Get(ctx, client.ObjectKeyFromObject(ru), existingRU)
					if err == nil {
						if existingRU.Spec.Server.URL != ru.Spec.Server.URL {
							log.Info("Refusing to delete RemoteUnleash due to URL mismatch - possible hijack attempt",
								"name", ru.Name, "namespace", ru.Namespace,
								"existingURL", existingRU.Spec.Server.URL, "newURL", ru.Spec.Server.URL)
							continue
						}
					} else if !apierrors.IsNotFound(err) {
						if !retriableError(err) {
							permanentError = err
						}
						return err
					}
					safeRUs = append(safeRUs, ru)
					safeSecrets = append(safeSecrets, adminSecrets[i])
				}

				// Delete RemoteUnleash resources
				objectsCtx, objectsCancel := r.Timeout.WriteContext(ctx)
				defer objectsCancel()

				if errs := utils.DeleteAllObjects(objectsCtx, r.Client, safeRUs); len(errs) > 0 {
					for _, err := range errs {
						remoteUnleashReceived.WithLabelValues("removed", "failed").Inc()
						log.Error(err, "Failed to delete RemoteUnleash")

						if !retriableError(err) {
							permanentError = err
						}
					}
					if permanentError != nil {
						return permanentError
					}
					return errs[0]
				}

				// Delete the admin secrets
				secretCtx, secretCancel := r.Timeout.WriteContext(ctx)
				defer secretCancel()

				if errs := utils.DeleteAllObjects(secretCtx, r.Client, safeSecrets); len(errs) > 0 {
					for _, err := range errs {
						remoteUnleashReceived.WithLabelValues("removed", "failed").Inc()
						log.Error(err, "Failed to delete admin secret")

						if !retriableError(err) {
							permanentError = err
						}
					}
					if permanentError != nil {
						return permanentError
					}
					return errs[0]
				}

				remoteUnleashReceived.WithLabelValues("removed", "success").Inc()
				log.Info("Successfully deleted RemoteUnleash resources and secret")
				return nil

			case pb.Status_Provisioned:
				log.Info("Received Status_Provisioned")

				// Filter safe objects to prevent cross-namespace overwrite hijacking
				var safeSecrets []*corev1.Secret
				var safeRUs []*unleashv1.RemoteUnleash

				for i, ru := range remoteUnleashes {
					existingRU := &unleashv1.RemoteUnleash{}
					err := r.Client.Get(ctx, client.ObjectKeyFromObject(ru), existingRU)
					if err == nil {
						if existingRU.Spec.Server.URL != ru.Spec.Server.URL {
							log.Info("Refusing to overwrite RemoteUnleash due to URL mismatch - possible hijack attempt",
								"name", ru.Name, "namespace", ru.Namespace,
								"existingURL", existingRU.Spec.Server.URL, "newURL", ru.Spec.Server.URL)
							continue
						}
					} else if !apierrors.IsNotFound(err) {
						if !retriableError(err) {
							permanentError = err
						}
						return err
					}
					safeRUs = append(safeRUs, ru)
					safeSecrets = append(safeSecrets, adminSecrets[i])
				}

				secretCtx, secretCancel := r.Timeout.WriteContext(ctx)
				defer secretCancel()

				if errs := utils.UpsertAllObjects(secretCtx, r.Client, safeSecrets); len(errs) > 0 {
					for _, err := range errs {
						remoteUnleashReceived.WithLabelValues("provisioned", "failed").Inc()

						if !retriableError(err) {
							permanentError = err
						}
					}
					if permanentError != nil {
						return permanentError
					}
					return errs[0]
				}

				objectsCtx, objectsCancel := r.Timeout.WriteContext(ctx)
				defer objectsCancel()

				if err := utils.UpsertAllObjects(objectsCtx, r.Client, safeRUs); len(err) > 0 {
					for _, err := range err {
						remoteUnleashReceived.WithLabelValues("provisioned", "failed").Inc()

						if namespaceNotFoundError(err) {
							log.Info(fmt.Sprintf("Namespace %s not found for RemoteUnleash %s", err.(*apierrors.StatusError).ErrStatus.Details.Name, remoteUnleashes[0].GetName()))
							continue
						} else {
							if !retriableError(err) {
								permanentError = err
							}
							return err
						}
					}
				}

				remoteUnleashReceived.WithLabelValues("provisioned", "success").Inc()
				return nil
			default:
				remoteUnleashReceived.WithLabelValues("unknown", "failed").Inc()
				log.Error(fmt.Errorf("unknown status: %s", status), "Received unknown status")
				return nil
			}
		})

		if err != nil {
			return err
		}
	}

	return permanentError
}

// retriableError returns true if the error is not a forbidden or unauthorized error.
func retriableError(err error) bool {
	return !apierrors.IsForbidden(err) && !apierrors.IsUnauthorized(err)
}

// secretNameBoundToInstance reports whether an annotation-less legacy admin secret
// name is bound to the RemoteUnleash instance name. This is defense-in-depth ONLY,
// used as a gated legacy fallback; the authoritative confused-deputy control is the
// authorized-namespace annotation. It accepts the exact "<prefix>-<name>" base as well
// as any "<prefix>-<name>-..." suffix (namespace-bound, bash, and old-federation
// formats, with or without a nonce) so migration does not hard-break existing tenants.
func secretNameBoundToInstance(secretName, instanceName string) bool {
	base := fmt.Sprintf("%s-%s", unleashv1.UnleashSecretNamePrefix, instanceName)
	return secretName == base || strings.HasPrefix(secretName, base+"-")
}

// namespaceNotFoundError returns true if the error is a namespace not found error.
func namespaceNotFoundError(err error) bool {
	var statusErr *apierrors.StatusError
	return errors.As(err, &statusErr) && statusErr.ErrStatus.Reason == metav1.StatusReasonNotFound && statusErr.ErrStatus.Details.Kind == "namespaces"
}

// SetupWithManager sets up the controller with the Manager.
func (r *RemoteUnleashReconciler) SetupWithManager(mgr ctrl.Manager) error {
	pred := predicate.GenerationChangedPredicate{}
	return ctrl.NewControllerManagedBy(mgr).
		For(&unleashv1.RemoteUnleash{}).
		WithEventFilter(pred).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 4, // Process multiple instances in parallel
		}).
		Complete(r)
}
