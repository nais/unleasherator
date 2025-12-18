package controller

import (
	"context"

	unleashv1 "github.com/nais/unleasherator/api/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// MetricsInitializer populates gauge metrics on startup to prevent empty series after restarts.
type MetricsInitializer struct {
	Client client.Client
}

func (m *MetricsInitializer) Start(ctx context.Context) error {
	log := ctrl.Log.WithName("metrics-initializer")
	log.Info("Initializing metrics for existing resources")

	// Non-fatal: operator continues even if metrics init fails
	if err := m.initUnleashMetrics(ctx); err != nil {
		log.Error(err, "Failed to initialize Unleash metrics")
	}
	if err := m.initRemoteUnleashMetrics(ctx); err != nil {
		log.Error(err, "Failed to initialize RemoteUnleash metrics")
	}
	if err := m.initApiTokenMetrics(ctx); err != nil {
		log.Error(err, "Failed to initialize ApiToken metrics")
	}
	if err := m.initReleaseChannelMetrics(ctx); err != nil {
		log.Error(err, "Failed to initialize ReleaseChannel metrics")
	}

	log.Info("Metrics initialization complete")
	return nil
}

func (m *MetricsInitializer) initUnleashMetrics(ctx context.Context) error {
	log := ctrl.Log.WithName("metrics-initializer").WithName("unleash")

	var unleashList unleashv1.UnleashList
	if err := m.Client.List(ctx, &unleashList); err != nil {
		return err
	}

	log.Info("Initializing metrics for Unleash resources", "count", len(unleashList.Items))

	for _, unleash := range unleashList.Items {
		reconciledCond := meta.FindStatusCondition(unleash.Status.Conditions, unleashv1.UnleashStatusConditionTypeReconciled)
		if reconciledCond != nil {
			unleashStatus.WithLabelValues(unleash.Namespace, unleash.Name, unleashv1.UnleashStatusConditionTypeReconciled).Set(promGaugeValueForStatus(reconciledCond.Status))
		} else {
			unleashStatus.WithLabelValues(unleash.Namespace, unleash.Name, unleashv1.UnleashStatusConditionTypeReconciled).Set(0)
		}

		connectedCond := meta.FindStatusCondition(unleash.Status.Conditions, unleashv1.UnleashStatusConditionTypeConnected)
		if connectedCond != nil {
			unleashStatus.WithLabelValues(unleash.Namespace, unleash.Name, unleashv1.UnleashStatusConditionTypeConnected).Set(promGaugeValueForStatus(connectedCond.Status))
		} else {
			unleashStatus.WithLabelValues(unleash.Namespace, unleash.Name, unleashv1.UnleashStatusConditionTypeConnected).Set(0)
		}
	}

	return nil
}

func (m *MetricsInitializer) initRemoteUnleashMetrics(ctx context.Context) error {
	log := ctrl.Log.WithName("metrics-initializer").WithName("remoteunleash")

	var remoteUnleashList unleashv1.RemoteUnleashList
	if err := m.Client.List(ctx, &remoteUnleashList); err != nil {
		return err
	}

	log.Info("Initializing metrics for RemoteUnleash resources", "count", len(remoteUnleashList.Items))

	for _, remoteUnleash := range remoteUnleashList.Items {
		reconciledCond := meta.FindStatusCondition(remoteUnleash.Status.Conditions, unleashv1.UnleashStatusConditionTypeReconciled)
		if reconciledCond != nil {
			remoteUnleashStatus.WithLabelValues(remoteUnleash.Namespace, remoteUnleash.Name, unleashv1.UnleashStatusConditionTypeReconciled).Set(promGaugeValueForStatus(reconciledCond.Status))
		} else {
			remoteUnleashStatus.WithLabelValues(remoteUnleash.Namespace, remoteUnleash.Name, unleashv1.UnleashStatusConditionTypeReconciled).Set(0)
		}

		connectedCond := meta.FindStatusCondition(remoteUnleash.Status.Conditions, unleashv1.UnleashStatusConditionTypeConnected)
		if connectedCond != nil {
			remoteUnleashStatus.WithLabelValues(remoteUnleash.Namespace, remoteUnleash.Name, unleashv1.UnleashStatusConditionTypeConnected).Set(promGaugeValueForStatus(connectedCond.Status))
		} else {
			remoteUnleashStatus.WithLabelValues(remoteUnleash.Namespace, remoteUnleash.Name, unleashv1.UnleashStatusConditionTypeConnected).Set(0)
		}
	}

	return nil
}

func (m *MetricsInitializer) initApiTokenMetrics(ctx context.Context) error {
	log := ctrl.Log.WithName("metrics-initializer").WithName("apitoken")

	var apiTokenList unleashv1.ApiTokenList
	if err := m.Client.List(ctx, &apiTokenList); err != nil {
		return err
	}

	log.Info("Initializing metrics for ApiToken resources", "count", len(apiTokenList.Items))

	for _, apiToken := range apiTokenList.Items {
		createdCond := meta.FindStatusCondition(apiToken.Status.Conditions, unleashv1.ApiTokenStatusConditionTypeCreated)
		if createdCond != nil {
			apiTokenStatus.WithLabelValues(apiToken.Namespace, apiToken.Name, unleashv1.ApiTokenStatusConditionTypeCreated).Set(promGaugeValueForStatus(createdCond.Status))
		} else {
			apiTokenStatus.WithLabelValues(apiToken.Namespace, apiToken.Name, unleashv1.ApiTokenStatusConditionTypeCreated).Set(0)
		}

		failedCond := meta.FindStatusCondition(apiToken.Status.Conditions, unleashv1.ApiTokenStatusConditionTypeFailed)
		if failedCond != nil {
			apiTokenStatus.WithLabelValues(apiToken.Namespace, apiToken.Name, unleashv1.ApiTokenStatusConditionTypeFailed).Set(promGaugeValueForStatus(failedCond.Status))
		} else {
			apiTokenStatus.WithLabelValues(apiToken.Namespace, apiToken.Name, unleashv1.ApiTokenStatusConditionTypeFailed).Set(0)
		}
	}

	return nil
}

func (m *MetricsInitializer) initReleaseChannelMetrics(ctx context.Context) error {
	log := ctrl.Log.WithName("metrics-initializer").WithName("releasechannel")

	var releaseChannelList unleashv1.ReleaseChannelList
	if err := m.Client.List(ctx, &releaseChannelList); err != nil {
		return err
	}

	log.Info("Initializing metrics for ReleaseChannel resources", "count", len(releaseChannelList.Items))

	for _, releaseChannel := range releaseChannelList.Items {
		labels := []string{releaseChannel.Namespace, releaseChannel.Name}

		var status float64
		switch releaseChannel.Status.Phase {
		case unleashv1.ReleaseChannelPhaseCompleted, unleashv1.ReleaseChannelPhaseIdle:
			status = 1
		case unleashv1.ReleaseChannelPhaseFailed:
			status = 0
		default:
			status = 0.5
		}
		releaseChannelStatus.WithLabelValues(labels...).Set(status)
		releaseChannelInstances.WithLabelValues(labels...).Set(float64(releaseChannel.Status.Instances))
		releaseChannelInstancesUpToDate.WithLabelValues(labels...).Set(float64(releaseChannel.Status.InstancesUpToDate))
	}

	return nil
}
