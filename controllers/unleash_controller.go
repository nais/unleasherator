/*
Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
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

	"github.com/go-logr/logr"
	unleashv1 "github.com/nais/liberator/pkg/apis/unleash.nais.io/v1"
	"github.com/nais/unleasherator/pkg/resources"
)

const unleashFinalizer = "unleash.nais.io/finalizer"

const (
	// typeAvailableUnleash represents the status of the Deployment reconciliation
	typeAvailableUnleash = "Available"

	// typeDegradedUnleash represents the status used when the custom resource is deleted and the finalizer operations are must to occur.
	typeDegradedUnleash = "Degraded"
)

// UnleashReconciler reconciles a Unleash object
type UnleashReconciler struct {
	client.Client
	Scheme            *runtime.Scheme
	Recorder          record.EventRecorder
	OperatorNamespace string
}

//+kubebuilder:rbac:groups=unleash.nais.io,resources=unleashes,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=unleash.nais.io,resources=unleashes/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=unleash.nais.io,resources=unleashes/finalizers,verbs=update
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=services/finalizers,verbs=update
//+kubebuilder:rbac:groups=core,resources=secrets/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=events,verbs=create;patch
//+kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses/finalizers,verbs=update
//+kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies/finalizers,verbs=update
//+kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies/status,verbs=get;update;patch

// It is essential for the controller's reconciliation loop to be idempotent. By following the Operator
// pattern you will create Controllers which provide a reconcile function
// responsible for synchronizing resources until the desired state is reached on the cluster.
// Breaking this recommendation goes against the design principles of controller-runtime.
// and may lead to unforeseen consequences such as resources becoming stuck and requiring manual intervention.
// For further info:
// - About Operator Pattern: https://kubernetes.io/docs/concepts/extend-kubernetes/operator/
// - About Controllers: https://kubernetes.io/docs/concepts/architecture/controller/
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.13.0/pkg/reconcile
func (r *UnleashReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Fetch the Unleash instance
	// The purpose is check if the Custom Resource for the Kind Unleash
	// is applied on the cluster if not we return nil to stop the reconciliation
	unleash := &unleashv1.Unleash{}
	err := r.Get(ctx, req.NamespacedName, unleash)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// If the custom resource is not found then, it usually means that it was deleted or not created
			// In this way, we will stop the reconciliation
			log.Info("unleash resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		log.Error(err, "Failed to get unleash")
		return ctrl.Result{}, err
	}

	// Let's just set the status as Unknown when no status are available
	if unleash.Status.Conditions == nil || len(unleash.Status.Conditions) == 0 {
		meta.SetStatusCondition(&unleash.Status.Conditions, metav1.Condition{Type: typeAvailableUnleash, Status: metav1.ConditionUnknown, Reason: "Reconciling", Message: "Starting reconciliation"})
		if err = r.Status().Update(ctx, unleash); err != nil {
			log.Error(err, "Failed to update Unleash status")
			return ctrl.Result{}, err
		}

		// Let's re-fetch the unleash Custom Resource after update the status
		// so that we have the latest state of the resource on the cluster and we will avoid
		// raise the issue "the object has been modified, please apply
		// your changes to the latest version and try again" which would re-trigger the reconciliation
		// if we try to update it again in the following operations
		if err := r.Get(ctx, req.NamespacedName, unleash); err != nil {
			log.Error(err, "Failed to re-fetch unleash")
			return ctrl.Result{}, err
		}
	}

	// Let's add a finalizer. Then, we can define some operations which should
	// occurs before the custom resource to be deleted.
	// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/finalizers
	if !controllerutil.ContainsFinalizer(unleash, unleashFinalizer) {
		log.Info("Adding Finalizer for Unleash")
		if ok := controllerutil.AddFinalizer(unleash, unleashFinalizer); !ok {
			log.Error(err, "Failed to add finalizer into the custom resource")
			return ctrl.Result{Requeue: true}, nil
		}

		if err = r.Update(ctx, unleash); err != nil {
			log.Error(err, "Failed to update custom resource to add finalizer")
			return ctrl.Result{}, err
		}
	}

	// Check if the Unleash instance is marked to be deleted, which is
	// indicated by the deletion timestamp being set.
	isUnleashMarkedToBeDeleted := unleash.GetDeletionTimestamp() != nil
	if isUnleashMarkedToBeDeleted {
		if controllerutil.ContainsFinalizer(unleash, unleashFinalizer) {
			log.Info("Performing Finalizer Operations for Unleash before delete CR")

			// Let's add here an status "Downgrade" to define that this resource begin its process to be terminated.
			meta.SetStatusCondition(&unleash.Status.Conditions, metav1.Condition{Type: typeDegradedUnleash,
				Status: metav1.ConditionUnknown, Reason: "Finalizing",
				Message: fmt.Sprintf("Performing finalizer operations for the custom resource: %s ", unleash.Name)})

			if err := r.Status().Update(ctx, unleash); err != nil {
				log.Error(err, "Failed to update Unleash status")
				return ctrl.Result{}, err
			}

			// Perform all operations required before remove the finalizer and allow
			// the Kubernetes API to remove the custom resource.
			r.doFinalizerOperationsForUnleash(unleash, ctx, log)

			// TODO(user): If you add operations to the doFinalizerOperationsForUnleash method
			// then you need to ensure that all worked fine before deleting and updating the Downgrade status
			// otherwise, you should requeue here.

			// Re-fetch the unleash Custom Resource before update the status
			// so that we have the latest state of the resource on the cluster and we will avoid
			// raise the issue "the object has been modified, please apply
			// your changes to the latest version and try again" which would re-trigger the reconciliation
			if err := r.Get(ctx, req.NamespacedName, unleash); err != nil {
				log.Error(err, "Failed to re-fetch unleash")
				return ctrl.Result{}, err
			}

			meta.SetStatusCondition(&unleash.Status.Conditions, metav1.Condition{Type: typeDegradedUnleash,
				Status: metav1.ConditionTrue, Reason: "Finalizing",
				Message: fmt.Sprintf("Finalizer operations for custom resource %s name were successfully accomplished", unleash.Name)})

			if err := r.Status().Update(ctx, unleash); err != nil {
				log.Error(err, "Failed to update Unleash status")
				return ctrl.Result{}, err
			}

			log.Info("Removing Finalizer for Unleash after successfully perform the operations")
			if ok := controllerutil.RemoveFinalizer(unleash, unleashFinalizer); !ok {
				log.Error(err, "Failed to remove finalizer for Unleash")
				return ctrl.Result{Requeue: true}, nil
			}

			if err := r.Update(ctx, unleash); err != nil {
				log.Error(err, "Failed to remove finalizer for Unleash")
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	var res ctrl.Result

	_, res, err = r.reconcileSecrets(unleash, ctx, log)
	if err != nil {
		log.Error(err, "Failed to reconcile Secrets for Unleash")
		return ctrl.Result{}, err
	} else if res.Requeue {
		return res, nil
	}

	_, res, err = r.reconcileDeployment(unleash, ctx, log)
	if err != nil {
		log.Error(err, "Failed to reconcile Deployment for Unleash")
		return ctrl.Result{}, err
	} else if res.Requeue {
		return res, nil
	}

	_, res, err = r.reconcileService(unleash, ctx, log)
	if err != nil {
		log.Error(err, "Failed to reconcile Service for Unleash")
		return ctrl.Result{}, err
	} else if res.Requeue {
		return res, nil
	}

	_, res, err = r.reconcileNetworkPolicy(unleash, ctx, log)
	if err != nil {
		log.Error(err, "Failed to reconcile NetworkPolicy for Unleash")
		return ctrl.Result{}, err
	} else if res.Requeue {
		return res, nil
	}

	err = r.testConnection(unleash, ctx, log)
	if err != nil {
		log.Error(err, "Failed to test connection to Unleash")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// finalizeUnleash will perform the required operations before delete the CR.
func (r *UnleashReconciler) doFinalizerOperationsForUnleash(cr *unleashv1.Unleash, ctx context.Context, log logr.Logger) {
	// TODO(user): Add the cleanup steps that the operator
	// needs to do before the CR can be deleted. Examples
	// of finalizers include performing backups and deleting
	// resources that are not owned by this CR, like a PVC.

	operatorSecret := cr.NamespacedOperatorSecretName(r.OperatorNamespace)

	err := r.Delete(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      operatorSecret.Name,
			Namespace: operatorSecret.Namespace,
		},
	})
	if err != nil {
		log.Error(err, fmt.Sprintf("Failed to delete the secret %s from the namespace %s",
			operatorSecret.Name,
			operatorSecret.Namespace))

		r.Recorder.Event(cr, "Warning", "Deleting",
			fmt.Sprintf("Failed to delete the secret %s from the namespace %s",
				operatorSecret.Name,
				operatorSecret.Namespace))
	}

	// Note: It is not recommended to use finalizers with the purpose of delete resources which are
	// created and managed in the reconciliation. These ones, such as the Deployment created on this reconcile,
	// are defined as depended of the custom resource. See that we use the method ctrl.SetControllerReference.
	// to set the ownerRef which means that the Deployment will be deleted by the Kubernetes API.
	// More info: https://kubernetes.io/docs/tasks/administer-cluster/use-cascading-deletion/

	// The following implementation will raise an event
	r.Recorder.Event(cr, "Warning", "Deleting",
		fmt.Sprintf("Custom Resource %s is being deleted from the namespace %s",
			cr.Name,
			cr.Namespace))
}

// reconcileSecrets will ensure that the secrets required for the Unleash deployment are created.
func (r *UnleashReconciler) reconcileNetworkPolicy(unleash *unleashv1.Unleash, ctx context.Context, log logr.Logger) (*networkingv1.NetworkPolicy, ctrl.Result, error) {
	// Check if network policy already exists, if not create a new one
	found := &networkingv1.NetworkPolicy{}
	err := r.Get(ctx, unleash.NamespacedName(), found)
	if err != nil && errors.IsNotFound(err) {
		np, err := resources.NetworkPolicyForUnleash(unleash, r.Scheme, r.OperatorNamespace)
		if err != nil {
			return found, ctrl.Result{}, err
		}
		log.Info("Creating a new NetworkPolicy", "NetworkPolicy.Namespace", np.Namespace, "NetworkPolicy.Name", np.Name)
		err = r.Create(ctx, np)
		if err != nil {
			return found, ctrl.Result{}, err
		}
		return np, ctrl.Result{Requeue: true}, nil
	} else if err != nil {
		return found, ctrl.Result{}, err
	}

	// NetworkPolicy already exists - don't requeue
	log.Info("Skip reconcile: NetworkPolicy already exists", "NetworkPolicy.Namespace", found.Namespace, "NetworkPolicy.Name", found.Name)
	return found, ctrl.Result{}, nil
}

// reconcileSecrets will ensure that the required secrets are created
func (r *UnleashReconciler) reconcileSecrets(unleash *unleashv1.Unleash, ctx context.Context, log logr.Logger) (*corev1.Secret, ctrl.Result, error) {
	// Check if operator secret already exists, if not create a new one
	found := &corev1.Secret{}
	err := r.Get(ctx, unleash.NamespacedOperatorSecretName(r.OperatorNamespace), found)
	if err != nil && errors.IsNotFound(err) {
		adminKey := resources.GenerateAdminKey()
		secret, err := resources.SecretForUnleash(unleash, r.Scheme, unleash.GetOperatorSecretName(), r.OperatorNamespace, adminKey, false)
		if err != nil {
			return found, ctrl.Result{}, err
		}
		log.Info("Creating a new Operator Secret", "Secret.Namespace", secret.Namespace, "Secret.Name", secret.Name)
		err = r.Create(ctx, secret)
		if err != nil {
			return found, ctrl.Result{}, err
		}

		// Secret created successfully - return and requeue
		return found, ctrl.Result{Requeue: true}, nil
	} else if err != nil {
		return found, ctrl.Result{}, err
	}

	// Secret already exists - don't requeue
	log.Info("Skip reconcile: Operator Secret already exists", "Secret.Namespace", found.Namespace, "Secret.Name", found.Name)

	// Get the admin key from the operator secret
	adminKey := string(found.Data[resources.EnvInitAdminAPIToken])
	if adminKey == "" {
		return found, ctrl.Result{}, fmt.Errorf("operator secret AdminKey is empty")
	}

	// Check if instance secret already exists, if not create a new one
	found = &corev1.Secret{}
	err = r.Get(ctx, unleash.NamespacedInstanceSecretName(), found)
	if err != nil && errors.IsNotFound(err) {
		secret, err := resources.SecretForUnleash(unleash, r.Scheme, unleash.GetInstanceSecretName(), unleash.Namespace, adminKey, true)
		if err != nil {
			return found, ctrl.Result{}, err
		}
		log.Info("Creating a new Instance Secret", "Secret.Namespace", secret.Namespace, "Secret.Name", secret.Name)
		err = r.Create(ctx, secret)
		if err != nil {
			return found, ctrl.Result{}, err
		}

		// Secret created successfully - return and requeue
		return found, ctrl.Result{Requeue: true}, nil
	} else if err != nil {
		return found, ctrl.Result{}, err
	}

	// Secret already exists - don't requeue
	log.Info("Skip reconcile: Instance Secret already exists", "Secret.Namespace", found.Namespace, "Secret.Name", found.Name)

	return found, ctrl.Result{}, nil
}

// reconcileDeployment will ensure that the required deployment is created
func (r *UnleashReconciler) reconcileDeployment(unleash *unleashv1.Unleash, ctx context.Context, log logr.Logger) (*appsv1.Deployment, ctrl.Result, error) {
	// Check if the deployment already exists, if not create a new one
	found := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: unleash.Name, Namespace: unleash.Namespace}, found)
	if err != nil && apierrors.IsNotFound(err) {
		// Define a new deployment
		dep, err := resources.DeploymentForUnleash(unleash, r.Scheme)
		if err != nil {
			log.Error(err, "Failed to define new Deployment resource for Unleash")

			// The following implementation will update the status
			meta.SetStatusCondition(&unleash.Status.Conditions, metav1.Condition{Type: typeAvailableUnleash,
				Status: metav1.ConditionFalse, Reason: "Reconciling",
				Message: fmt.Sprintf("Failed to create Deployment for the custom resource (%s): (%s)", unleash.Name, err)})

			if err := r.Status().Update(ctx, unleash); err != nil {
				log.Error(err, "Failed to update Unleash status")
				return found, ctrl.Result{}, err
			}

			return found, ctrl.Result{}, err
		}

		log.Info("Creating a new Deployment",
			"Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
		if err = r.Create(ctx, dep); err != nil {
			log.Error(err, "Failed to create new Deployment",
				"Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
			return found, ctrl.Result{}, err
		}

		// Deployment created successfully
		// We will requeue the reconciliation so that we can ensure the state
		// and move forward for the next operations
		return found, ctrl.Result{RequeueAfter: time.Minute}, nil
	} else if err != nil {
		log.Error(err, "Failed to get Deployment")
		// Let's return the error for the reconciliation be re-trigged again
		return found, ctrl.Result{}, err
	}

	// Deployment already exists - don't requeue
	log.Info("Skip reconcile: Deployment already exists", "Deployment.Namespace", found.Namespace, "Deployment.Name", found.Name)

	// The CRD API is defining that the Unleash type, have a UnleashSpec.Size field
	// to set the quantity of Deployment instances is the desired state on the cluster.
	// Therefore, the following code will ensure the Deployment size is the same as defined
	// via the Size spec of the Custom Resource which we are reconciling.
	size := unleash.Spec.Size
	if *found.Spec.Replicas != size {
		found.Spec.Replicas = &size
		if err = r.Update(ctx, found); err != nil {
			log.Error(err, "Failed to update Deployment",
				"Deployment.Namespace", found.Namespace, "Deployment.Name", found.Name)

			// Re-fetch the unleash Custom Resource before update the status
			// so that we have the latest state of the resource on the cluster and we will avoid
			// raise the issue "the object has been modified, please apply
			// your changes to the latest version and try again" which would re-trigger the reconciliation
			if err := r.Get(ctx, unleash.NamespacedName(), unleash); err != nil {
				log.Error(err, "Failed to re-fetch unleash")
				return found, ctrl.Result{}, err
			}

			// The following implementation will update the status
			meta.SetStatusCondition(&unleash.Status.Conditions, metav1.Condition{Type: typeAvailableUnleash,
				Status: metav1.ConditionFalse, Reason: "Resizing",
				Message: fmt.Sprintf("Failed to update the size for the custom resource (%s): (%s)", unleash.Name, err)})

			if err := r.Status().Update(ctx, unleash); err != nil {
				log.Error(err, "Failed to update Unleash status")
				return found, ctrl.Result{}, err
			}

			return found, ctrl.Result{}, err
		}

		// Now, that we update the size we want to requeue the reconciliation
		// so that we can ensure that we have the latest state of the resource before
		// update. Also, it will help ensure the desired state on the cluster
		return found, ctrl.Result{Requeue: true}, nil
	}

	// Deployment size is correct - don't requeue
	log.Info("Skip reconcile: Deployment size is correct", "Deployment.Namespace", found.Namespace, "Deployment.Name", found.Name)

	// Check if the deployment image is the same as defined as default image
	// for the Unleash operator.
	// @TODO: Allow the user to define the image via the CRD API
	if found.Spec.Template.Spec.Containers[0].Image != resources.ImageForUnleash() {
		found.Spec.Template.Spec.Containers[0].Image = resources.ImageForUnleash()
		if err = r.Update(ctx, found); err != nil {
			log.Error(err, "Failed to update Deployment",
				"Deployment.Namespace", found.Namespace, "Deployment.Name", found.Name)

			// Re-fetch the unleash Custom Resource before update the status
			// so that we have the latest state of the resource on the cluster and we will avoid
			// raise the issue "the object has been modified, please apply
			// your changes to the latest version and try again" which would re-trigger the reconciliation
			if err := r.Get(ctx, unleash.NamespacedName(), unleash); err != nil {
				log.Error(err, "Failed to re-fetch unleash")
				return found, ctrl.Result{}, err
			}

			// The following implementation will update the status
			meta.SetStatusCondition(&unleash.Status.Conditions, metav1.Condition{Type: typeAvailableUnleash,
				Status: metav1.ConditionFalse, Reason: "Upgrading",
				Message: fmt.Sprintf("Failed to update the image for the custom resource (%s): (%s)", unleash.Name, err)})

			if err := r.Status().Update(ctx, unleash); err != nil {
				log.Error(err, "Failed to update Unleash status")
				return found, ctrl.Result{}, err
			}

			return found, ctrl.Result{}, err
		}

		// Now, that we update the image we want to requeue the reconciliation
		// so that we can ensure that we have the latest state of the resource before
		// update. Also, it will help ensure the desired state on the cluster
		return found, ctrl.Result{Requeue: true}, nil
	}

	// Deployment container image is correct - don't requeue
	log.Info("Skip reconcile: Deployment container image is correct", "Deployment.Namespace", found.Namespace, "Deployment.Name", found.Name)

	// The following implementation will update the status
	meta.SetStatusCondition(&unleash.Status.Conditions, metav1.Condition{Type: typeAvailableUnleash,
		Status: metav1.ConditionTrue, Reason: "Reconciling",
		Message: fmt.Sprintf("Deployment for custom resource (%s) with %d replicas created successfully", unleash.Name, size)})

	if err := r.Status().Update(ctx, unleash); err != nil {
		log.Error(err, "Failed to update Unleash status")
		return found, ctrl.Result{}, err
	}

	return found, ctrl.Result{}, nil
}

func (r *UnleashReconciler) reconcileService(unleash *unleashv1.Unleash, ctx context.Context, log logr.Logger) (*corev1.Service, ctrl.Result, error) {
	// Check if this Service already exists
	found := &corev1.Service{}
	err := r.Get(ctx, unleash.NamespacedName(), found)
	if err != nil && errors.IsNotFound(err) {
		// Define a new Service
		svc, err := resources.ServiceForUnleash(unleash, r.Scheme)
		if err != nil {
			log.Error(err, "Failed to create service for Unleash")
			return found, ctrl.Result{}, err
		}

		log.Info("Creating a new Service", "Service.Namespace", svc.Namespace, "Service.Name", svc.Name)
		err = r.Create(ctx, svc)
		if err != nil {
			log.Error(err, "Failed to create new Service", "Service.Namespace", svc.Namespace, "Service.Name", svc.Name)
			return found, ctrl.Result{}, err
		}

		// Service created successfully - don't requeue
		return svc, ctrl.Result{}, nil
	}

	// Service already exists - don't requeue
	log.Info("Skip reconcile: Service already exists", "Service.Namespace", found.Namespace, "Service.Name", found.Name)

	return found, ctrl.Result{}, nil
}

func (r *UnleashReconciler) testConnection(unleash *unleashv1.Unleash, ctx context.Context, log logr.Logger) error {
	// Get admin token from the secret
	secret := &corev1.Secret{}
	err := r.Get(ctx, unleash.NamespacedOperatorSecretName(r.OperatorNamespace), secret)
	if err != nil {
		log.Error(err, "Failed to get the secret")
		return err
	}

	// Get the admin token
	adminToken := string(secret.Data[resources.EnvInitAdminAPIToken])

	// Check if the Unleash server is up and running
	url := fmt.Sprintf("%s/api/admin/instance-admin/statistics", resources.UnleashURL(unleash))
	method := "GET"

	client := &http.Client{}
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		log.Error(err, "Failed to create a new request to the Unleash instance")
		return err
	}

	req.Header.Add("Accept", "application/json")
	req.Header.Add("Authorization", adminToken)

	res, err := client.Do(req)
	if err != nil {
		log.Error(err, "Failed to send the request to the Unleash instance")
		return err
	}
	defer res.Body.Close()

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		log.Error(err, "Failed to read the response from the Unleash instance")
		return err
	}
	fmt.Println(string(body))

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *UnleashReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&unleashv1.Unleash{}).
		Owns(&appsv1.Deployment{}).
		//WithOptions(controller.Options{MaxConcurrentReconciles: 2}).
		Complete(r)
}

func GetApiToken(ctx context.Context, r client.Client, unleashName, operatorNamespace string) (string, error) {
	secret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Name: unleashName, Namespace: operatorNamespace}, secret)
	if err != nil {
		return "", err
	}

	return string(secret.Data[resources.EnvInitAdminAPIToken]), nil
}
