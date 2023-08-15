package utils

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
	errs := []error{}

	for _, obj := range objects {
		err := UpsertObject(ctx, r, obj)
		if err != nil {
			errs = append(errs, err)
		}
	}

	return errs
}
