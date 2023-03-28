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
	"k8s.io/apimachinery/pkg/api/equality"
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
	unleashv1 "github.com/nais/unleasherator/api/v1"
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

	res, err = r.reconcileNetworkPolicy(unleash, ctx, log)
	if err != nil {
		log.Error(err, "Failed to reconcile NetworkPolicy for Unleash")
		return ctrl.Result{}, err
	} else if res.Requeue {
		return res, nil
	}

	res, err = r.reconcileIngresses(unleash, ctx, log)
	if err != nil {
		log.Error(err, "Failed to reconcile Ingresses for Unleash")
		return ctrl.Result{}, err
	} else if res.Requeue {
		return res, nil
	}

	err = r.testConnection(unleash, ctx, log)
	if err != nil {
		log.Error(err, "Failed to test connection to Unleash")
		return ctrl.Result{}, err
	}

	err = r.Get(ctx, req.NamespacedName, unleash)
	if err != nil {
		log.Error(err, "Failed to re-fetch unleash")
		return ctrl.Result{}, err
	}

	meta.SetStatusCondition(&unleash.Status.Conditions, metav1.Condition{Type: typeAvailableUnleash,
		Status: metav1.ConditionTrue, Reason: "Reconciling",
		Message: fmt.Sprintf("Unleash %s is available", unleash.Name)})

	if err := r.Status().Update(ctx, unleash); err != nil {
		log.Error(err, "Failed to update Unleash status")
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
func (r *UnleashReconciler) reconcileNetworkPolicy(unleash *unleashv1.Unleash, ctx context.Context, log logr.Logger) (ctrl.Result, error) {
	newNetPol, err := resources.NetworkPolicyForUnleash(unleash, r.Scheme, r.OperatorNamespace)
	if err != nil {
		return ctrl.Result{}, err
	}

	existingNetPol := &networkingv1.NetworkPolicy{}
	err = r.Get(ctx, unleash.NamespacedName(), existingNetPol)

	if err != nil && !errors.IsNotFound(err) {
		log.Error(err, "Failed to get NetworkPolicy for Unleash", "NetworkPolicy.Namespace", existingNetPol.Namespace, "NetworkPolicy.Name", existingNetPol.Name)
		return ctrl.Result{}, err
	}

	// If the netpol is not enabled, we delete it if it exists.
	if !unleash.Spec.NetworkPolicy.Enabled {
		if err == nil {
			log.Info("Deleting NetworkPolicy", "NetworkPolicy.Namespace", existingNetPol.Namespace, "NetworkPolicy.Name", existingNetPol.Name)
			err = r.Delete(ctx, existingNetPol)
			if err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, nil
	}

	// If the netpol is enabled and does not exist, we create it.
	if err != nil && errors.IsNotFound(err) {
		log.Info("Creating NetworkPolicy", "NetworkPolicy.Namespace", newNetPol.Namespace, "NetworkPolicy.Name", newNetPol.Name)
		err = r.Create(ctx, newNetPol)
		if err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// If the netpol is enabled and exists, we update it if it is not up to date.
	if !equality.Semantic.DeepDerivative(newNetPol.Spec, existingNetPol.Spec) || !equality.Semantic.DeepDerivative(newNetPol.Labels, existingNetPol.Labels) {
		log.Info("Updating NetworkPolicy", "NetworkPolicy.Namespace", existingNetPol.Namespace, "NetworkPolicy.Name", existingNetPol.Name)
		existingNetPol.Spec = newNetPol.Spec
		existingNetPol.Labels = newNetPol.Labels
		err = r.Update(ctx, existingNetPol)
		if err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	return ctrl.Result{}, nil
}

// reconcileSecrets will ensure that the secrets required for the Unleash deployment are created.
func (r *UnleashReconciler) reconcileIngress(
	unleash *unleashv1.Unleash,
	ingress *unleashv1.IngressConfig,
	nameSuffix string,
	ctx context.Context,
	log logr.Logger) (ctrl.Result, error) {

	newIngress, err := resources.IngressForUnleash(unleash, ingress, nameSuffix, r.Scheme)
	if err != nil {
		return ctrl.Result{}, err
	}

	existingIngress := &networkingv1.Ingress{}
	err = r.Get(ctx, unleash.NamespacedNameWithSuffix(nameSuffix), existingIngress)

	if err != nil && !errors.IsNotFound(err) {
		log.Error(err, "Failed to get Ingress for Unleash", "Ingress.Namespace", existingIngress.Namespace, "Ingress.Name", existingIngress.Name)
		return ctrl.Result{}, err
	}

	// If the ingress is not enabled, we delete the existing one if it exists.
	if !ingress.Enabled {
		if err == nil {
			log.Info("Deleting Ingress", "Ingress.Namespace", existingIngress.Namespace, "Ingress.Name", existingIngress.Name)
			err = r.Delete(ctx, existingIngress)
			if err != nil {
				log.Error(err, "Failed to delete Ingress for Unleash", "Ingress.Namespace", existingIngress.Namespace, "Ingress.Name", existingIngress.Name)
				return ctrl.Result{}, err
			}
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, nil
	}

	// If the ingress is enabled and does not exist, we create it.
	if err != nil && errors.IsNotFound(err) {
		log.Info("Creating Ingress", "Ingress.Namespace", newIngress.Namespace, "Ingress.Name", newIngress.Name)
		err = r.Create(ctx, newIngress)
		if err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// If the ingress is enabled and exists, we update it if it has changed.
	if !equality.Semantic.DeepDerivative(newIngress.Spec, existingIngress.Spec) || !equality.Semantic.DeepDerivative(newIngress.Labels, existingIngress.Labels) {
		log.Info("Updating Ingress", "Ingress.Namespace", existingIngress.Namespace, "Ingress.Name", existingIngress.Name)
		existingIngress.Spec = newIngress.Spec
		existingIngress.Labels = newIngress.Labels
		err = r.Update(ctx, existingIngress)
		if err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	return ctrl.Result{}, nil
}

// reconcileIngresses will ensure that the required ingresses are created
func (r *UnleashReconciler) reconcileIngresses(unleash *unleashv1.Unleash, ctx context.Context, log logr.Logger) (ctrl.Result, error) {
	res, err := r.reconcileIngress(unleash, &unleash.Spec.WebIngress, "web", ctx, log)
	if err != nil || res.Requeue {
		return res, err

	}

	res, err = r.reconcileIngress(unleash, &unleash.Spec.ApiIngress, "api", ctx, log)
	if err != nil || res.Requeue {
		return res, err
	}

	return ctrl.Result{}, nil
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

		return found, ctrl.Result{Requeue: true}, nil
	} else if err != nil {
		return found, ctrl.Result{}, err
	}

	log.Info("Skip reconcile: Operator Secret already exists", "Secret.Namespace", found.Namespace, "Secret.Name", found.Name)

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

		return found, ctrl.Result{Requeue: true}, nil
	} else if err != nil {
		return found, ctrl.Result{}, err
	}

	log.Info("Skip reconcile: Instance Secret already exists", "Secret.Namespace", found.Namespace, "Secret.Name", found.Name)
	return found, ctrl.Result{}, nil
}

// reconcileDeployment will ensure that the required deployment is created
func (r *UnleashReconciler) reconcileDeployment(unleash *unleashv1.Unleash, ctx context.Context, log logr.Logger) (*appsv1.Deployment, ctrl.Result, error) {
	dep, err := resources.DeploymentForUnleash(unleash, r.Scheme)
	if err != nil {
		meta.SetStatusCondition(&unleash.Status.Conditions, metav1.Condition{Type: typeAvailableUnleash,
			Status: metav1.ConditionFalse, Reason: "Reconciling",
			Message: fmt.Sprintf("Failed to create Deployment for the custom resource (%s): (%s)", unleash.Name, err)})

		if err := r.Status().Update(ctx, unleash); err != nil {
			log.Error(err, "Failed to update Unleash status")
			return nil, ctrl.Result{}, err
		}

		return nil, ctrl.Result{}, err
	}

	found := &appsv1.Deployment{}
	err = r.Get(ctx, types.NamespacedName{Name: unleash.Name, Namespace: unleash.Namespace}, found)
	if err != nil && apierrors.IsNotFound(err) {
		log.Info("Creating Deployment", "Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
		if err = r.Create(ctx, dep); err != nil {
			log.Error(err, "Failed to create Deployment", "Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
			return found, ctrl.Result{}, err
		}

		return found, ctrl.Result{RequeueAfter: time.Minute}, nil
	} else if err != nil {
		log.Error(err, "Failed to get Deployment")
		return found, ctrl.Result{}, err
	} else if !equality.Semantic.DeepDerivative(dep.Spec, found.Spec) {
		log.Info("Updating Deployment", "Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)

		found.Spec = dep.Spec
		if err = r.Update(ctx, found); err != nil {
			log.Error(err, "Failed to update Deployment", "Deployment.Namespace", found.Namespace, "Deployment.Name", found.Name)
			return found, ctrl.Result{}, err
		}
	}

	log.Info("Skip reconcile: Deployment already up to date", "Deployment.Namespace", found.Namespace, "Deployment.Name", found.Name)
	return found, ctrl.Result{}, nil
}

// reconcileService will ensure that the Service for the Unleash instance is created
func (r *UnleashReconciler) reconcileService(unleash *unleashv1.Unleash, ctx context.Context, log logr.Logger) (*corev1.Service, ctrl.Result, error) {
	newSvc, err := resources.ServiceForUnleash(unleash, r.Scheme)
	if err != nil {
		return nil, ctrl.Result{}, err
	}

	existingSvc := &corev1.Service{}
	err = r.Get(ctx, unleash.NamespacedName(), existingSvc)
	if err != nil && errors.IsNotFound(err) {
		log.Info("Creating Service", "Service.Namespace", newSvc.Namespace, "Service.Name", newSvc.Name)
		err = r.Create(ctx, newSvc)
		if err != nil {
			log.Error(err, "Failed to create new Service", "Service.Namespace", newSvc.Namespace, "Service.Name", newSvc.Name)
			return existingSvc, ctrl.Result{}, err
		}

		return newSvc, ctrl.Result{}, nil
	} else if err != nil {
		return existingSvc, ctrl.Result{}, err
	} else if !equality.Semantic.DeepDerivative(newSvc.Spec, existingSvc.Spec) {
		existingSvc.Spec = newSvc.Spec
		log.Info("Updating Service", "Service.Namespace", existingSvc.Namespace, "Service.Name", existingSvc.Name)
		err = r.Update(ctx, existingSvc)
		if err != nil {
			log.Error(err, "Failed to update Service", "Service.Namespace", existingSvc.Namespace, "Service.Name", existingSvc.Name)
			return existingSvc, ctrl.Result{}, err
		}
	}

	log.Info("Skip reconcile: Service up to date", "Service.Namespace", existingSvc.Namespace, "Service.Name", existingSvc.Name)
	return existingSvc, ctrl.Result{}, nil
}

// testConnection will test the connection to the Unleash instance
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

	if res.StatusCode != http.StatusOK {
		log.Error(err, "Failed to get a 200 OK response from the Unleash instance")
		return err
	}

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		log.Error(err, "Failed to read the response from the Unleash instance")
		return err
	}

	log.Info("Response from Unleash instance", "body", string(body))

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

// GetApiToken will return the API token for the Unleash instance
func GetApiToken(ctx context.Context, r client.Client, unleashName, operatorNamespace string) (string, error) {
	secret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Name: unleashName, Namespace: operatorNamespace}, secret)
	if err != nil {
		return "", err
	}

	return string(secret.Data[resources.EnvInitAdminAPIToken]), nil
}
