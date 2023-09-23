package utils

import (
	"context"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
)

// envVar returns an environment variable for the given name and value
func EnvVar(name, value string) corev1.EnvVar {
	return corev1.EnvVar{
		Name:  name,
		Value: value,
	}
}

// secretEnvVar returns an environment variable with the given name and value
func SecretEnvVar(name, secretName, secretKey string) corev1.EnvVar {
	return corev1.EnvVar{
		Name: name,
		ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: secretName,
				},
				Key: secretKey,
			},
		},
	}
}

// ReconcileObject reconciles the desired state of the given object with its current state in the Kubernetes cluster.
// If the object does not exist in the cluster, it will be created. If it does exist, it will be updated if necessary.
// If the object is not enabled, it will be deleted if it exists in the cluster.
// The function returns an error if there was a problem getting, creating, updating, or deleting the object.
func ReconcileObject[T client.Object](ctx context.Context, r client.Client, obj T, enabled bool) error {
	objectKey := client.ObjectKeyFromObject(obj)
	existing := obj.DeepCopyObject().(T)

	err := r.Get(ctx, objectKey, existing)
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	if !enabled {
		if !apierrors.IsNotFound(err) {
			if existing.GetDeletionTimestamp() == nil {
				return r.Delete(ctx, obj)
			}
		}
		return nil
	}

	if err != nil && apierrors.IsNotFound(err) {
		return r.Create(ctx, obj)
	}

	rawObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return err
	}

	rawExisting, err := runtime.DefaultUnstructuredConverter.ToUnstructured(existing)
	if err != nil {
		return err
	}

	if !equality.Semantic.DeepDerivative(rawObj["spec"], rawExisting["spec"]) || !equality.Semantic.DeepDerivative(rawObj["meatadata"].(map[string]interface{})["labels"], rawExisting["metadata"].(map[string]interface{})["labels"]) {
		obj.SetCreationTimestamp(existing.GetCreationTimestamp())
		obj.SetResourceVersion(existing.GetResourceVersion())
		obj.SetUID(existing.GetUID())

		err = r.Update(ctx, obj)
		if err != nil {
			return err
		}
	}

	return nil
}

// UpsertObject upserts the given object in Kubernetes. If the object already exists, it is updated.
// If the object does not exist, it is created. The object is identified by its key, which is extracted
// from the object itself. The function returns an error if the upsert operation fails.
func UpsertObject[T client.Object](ctx context.Context, r client.Client, obj T) error {
	objectKey := client.ObjectKeyFromObject(obj)
	existing := obj.DeepCopyObject().(T)

	err := r.Get(ctx, objectKey, existing)
	if apierrors.IsNotFound(err) {
		return r.Create(ctx, obj)
	} else if err != nil {
		return err
	}

	obj.SetCreationTimestamp(existing.GetCreationTimestamp())
	obj.SetResourceVersion(existing.GetResourceVersion())
	obj.SetUID(existing.GetUID())

	return r.Update(ctx, obj)
}

// UpsertAllObjects upserts all objects in the given slice to the Kubernetes API server.
// If an object already exists, it is updated. If an object does not exist, it is created.
// The objects are identified by their keys, which are extracted from the objects themselves.
// The function returns a slice of errors, one for each object that failed to be upserted.
func UpsertAllObjects[T client.Object](ctx context.Context, r client.Client, objects []T) []error {
	errs := make([]error, len(objects))
	var wg sync.WaitGroup

	for i, obj := range objects {
		wg.Add(1)
		go func(i int, obj T) {
			defer wg.Done()
			errs[i] = UpsertObject(ctx, r, obj)
		}(i, obj)
	}

	wg.Wait()

	return removeEmptyErrs(errs)
}

// removeEmptyErrs returns a slice of non-nil errors from the input slice.
// If all errors in the input slice are nil, it returns nil.
func removeEmptyErrs(slice []error) []error {
	result := make([]error, 0, len(slice))
	for _, s := range slice {
		if s != nil {
			result = append(result, s)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}
