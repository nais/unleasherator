package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"

	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/nais/unleasherator/internal/unleashclient"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	errRemoteUnleashAuthorization = errors.New("remote Unleash admin secret authorization failed")
	errRemoteUnleashServerURL     = errors.New("remote Unleash server URL validation failed")
	errRemoteUnleashEmptyToken    = errors.New("remote Unleash admin token is empty")
)

func validatedRemoteUnleashAdminToken(
	ctx context.Context,
	k8sClient client.Client,
	remoteUnleash *unleashv1.RemoteUnleash,
	operatorNamespace string,
	allowLegacyNameBoundSecrets bool,
) ([]byte, error) {
	adminSecretNamespace := remoteUnleash.Spec.AdminSecret.Namespace
	isCrossNamespace := adminSecretNamespace != "" && adminSecretNamespace != remoteUnleash.Namespace
	if isCrossNamespace && adminSecretNamespace != operatorNamespace {
		return nil, fmt.Errorf("%w: cross-namespace secret references are only permitted to the operator namespace", errRemoteUnleashAuthorization)
	}

	adminSecret := &corev1.Secret{}
	if err := k8sClient.Get(ctx, remoteUnleash.AdminSecretNamespacedName(), adminSecret); err != nil {
		return nil, err
	}

	authorizedNamespace, hasAuthorizedNamespace := adminSecret.Annotations[unleashv1.UnleashSecretAuthorizedNamespaceAnnotation]
	if isCrossNamespace {
		switch {
		case hasAuthorizedNamespace:
			if authorizedNamespace != remoteUnleash.Namespace {
				return nil, fmt.Errorf(
					"%w: admin secret is authorized for namespace %q, not %q",
					errRemoteUnleashAuthorization,
					authorizedNamespace,
					remoteUnleash.Namespace,
				)
			}
		case allowLegacyNameBoundSecrets:
			if !secretNameBoundToInstance(remoteUnleash.Spec.AdminSecret.Name, remoteUnleash.Name) {
				return nil, fmt.Errorf(
					"%w: cross-namespace secret name must be bound to RemoteUnleash %q",
					errRemoteUnleashAuthorization,
					remoteUnleash.Name,
				)
			}
		default:
			return nil, fmt.Errorf(
				"%w: cross-namespace admin secret must carry %s for namespace %q",
				errRemoteUnleashAuthorization,
				unleashv1.UnleashSecretAuthorizedNamespaceAnnotation,
				remoteUnleash.Namespace,
			)
		}
	}

	expectedURL := adminSecret.Data[unleashv1.UnleashSecretServerURLKey]
	urlRequired := hasAuthorizedNamespace || !allowLegacyNameBoundSecrets
	if urlRequired && len(expectedURL) == 0 {
		return nil, fmt.Errorf(
			"%w: admin secret is missing required key %q",
			errRemoteUnleashServerURL,
			unleashv1.UnleashSecretServerURLKey,
		)
	}
	if len(expectedURL) > 0 && remoteUnleash.Spec.Server.URL != string(expectedURL) {
		return nil, fmt.Errorf(
			"%w: RemoteUnleash URL does not match the URL authorized by the admin secret",
			errRemoteUnleashServerURL,
		)
	}

	adminToken := adminSecret.Data[remoteUnleash.Spec.AdminSecret.Key]
	if len(adminToken) == 0 {
		return nil, fmt.Errorf(
			"%w: secret %q does not contain key %q",
			errRemoteUnleashEmptyToken,
			remoteUnleash.Spec.AdminSecret.Name,
			remoteUnleash.Spec.AdminSecret.Key,
		)
	}

	return adminToken, nil
}

func validatedRemoteUnleashAPIClient(
	ctx context.Context,
	k8sClient client.Client,
	remoteUnleash *unleashv1.RemoteUnleash,
	operatorNamespace string,
	allowLegacyNameBoundSecrets bool,
) (*unleashclient.Client, error) {
	adminToken, err := validatedRemoteUnleashAdminToken(
		ctx,
		k8sClient,
		remoteUnleash,
		operatorNamespace,
		allowLegacyNameBoundSecrets,
	)
	if err != nil {
		return nil, err
	}

	return unleashclient.NewClient(remoteUnleash.Spec.Server.URL, string(adminToken))
}

func secretNameBoundToInstance(secretName, instanceName string) bool {
	base := fmt.Sprintf("%s-%s", unleashv1.UnleashSecretNamePrefix, instanceName)
	return secretName == base || strings.HasPrefix(secretName, base+"-")
}
