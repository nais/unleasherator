package controller

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jarcoal/httpmock"
	"go.opentelemetry.io/otel"
	ctrl "sigs.k8s.io/controller-runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"

	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/nais/unleasherator/internal/config"
	mockfederation "github.com/nais/unleasherator/internal/federation/mockfediration"
	//+kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

const namespace = "default"

var (
	cfg                     *rest.Config
	k8sClient               client.Client // You'll be using this client in your tests.
	testEnv                 *envtest.Environment
	ctx                     context.Context
	cancel                  context.CancelFunc
	remoteUnleashReconciler *RemoteUnleashReconciler
	ApiTokenNameSuffix      = "unleasherator"
	mockSubscriber          = &mockfederation.MockSubscriber{}
	mockPublisher           = &mockfederation.MockPublisher{}
)

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	// Speed up tests by setting shorter timeouts for all package-level timeout variables
	// Keep some timeouts at expected values to match test assertions
	unleashDeploymentTimeout = time.Second * 1 // Test expects "timed out after 1s"
	unleashControllerRequeueAfter = time.Millisecond * 100

	// Remote Unleash controller timeouts
	remoteUnleashErrorRetryDelay = time.Millisecond * 50
	remoteUnleashRequeueAfter = time.Millisecond * 100

	// API Token controller timeouts
	apiTokenRequeueAfter = time.Millisecond * 100

	// Release Channel controller timeouts
	releaseChannelErrorRetryDelay = time.Millisecond * 50
	releaseChannelIdleRequeueInterval = time.Millisecond * 100
	releaseChannelInitialDeploymentCheck = time.Millisecond * 100
	releaseChannelValidatingRetryDelay = time.Millisecond * 200
	releaseChannelValidatingTransition = time.Millisecond * 50
	releaseChannelCanaryWaitDelay = time.Millisecond * 100
	releaseChannelRollingWaitDelay = time.Millisecond * 100
	releaseChannelRollingBackWaitDelay = time.Millisecond * 100
	releaseChannelFailedRetryDelay = time.Millisecond * 200
	releaseChannelStatusUpdateSuccess = time.Millisecond * 50
	releaseChannelStatusUpdateRetry = time.Millisecond * 50
	releaseChannelBackoffBase = time.Millisecond * 10
	releaseChannelBackoffMedium = time.Millisecond * 20
	releaseChannelBackoffLong = time.Millisecond * 30
	releaseChannelBatchInterval = time.Millisecond * 10
	releaseChannelHealthCheckInitialDelay = time.Millisecond * 10
	releaseChannelDeletionCheckInterval = time.Millisecond * 100

	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true), zap.JSONEncoder()))

	// Enable test mode for unleashclient to skip otelhttp wrapping and allow httpmock to work
	os.Setenv("UNLEASH_TEST_MODE", "true")

	// Activate httpmock globally so it's available when controllers create HTTP clients
	httpmock.Activate()

	// Use a default responder as catch-all for any unmatched HTTP requests.
	// Controllers run continuously and may reconcile resources from completed tests.
	// Without this, concurrent controller reconciliations fail with "no responder found".
	httpmock.RegisterNoResponder(httpmock.NewStringResponder(200, `{"health":"OK","versionOSS":"v5.1.2"}`))

	ctx, cancel = context.WithCancel(context.TODO())

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "config", "crd", "bases"),
			filepath.Join("..", "..", "config", "prometheus", "crd"),
		},
		ErrorIfCRDPathMissing: true,
	}

	var err error
	// cfg is defined in this file globally.
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	err = unleashv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = monitoringv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	//+kubebuilder:scaffold:scheme

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
		Metrics: server.Options{
			BindAddress: "0", // disable firewall prompt on mac
		},
	})
	Expect(err).ToNot(HaveOccurred())

	timeout := config.TimeoutConfig{
		Write: time.Duration(5) * time.Second,
	}

	err = (&UnleashReconciler{
		Client:            k8sManager.GetClient(),
		Scheme:            k8sManager.GetScheme(),
		OperatorNamespace: namespace,
		Recorder:          k8sManager.GetEventRecorderFor("unleash-controller"),
		Federation: UnleashFederation{
			Enabled:   true,
			Publisher: mockPublisher,
		},
		Tracer: otel.Tracer("unleash-controller"),
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	remoteUnleashReconciler = &RemoteUnleashReconciler{
		Client:            k8sManager.GetClient(),
		Scheme:            k8sManager.GetScheme(),
		OperatorNamespace: namespace,
		Timeout:           timeout,
		Federation: RemoteUnleashFederation{
			Enabled:     true,
			ClusterName: "test-cluster",
			Subscriber:  mockSubscriber,
		},
		Tracer: otel.Tracer("remoteunleash-controller"),
	}
	err = remoteUnleashReconciler.SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	err = (&ApiTokenReconciler{
		Client:                k8sManager.GetClient(),
		Scheme:                k8sManager.GetScheme(),
		OperatorNamespace:     namespace,
		ApiTokenNameSuffix:    ApiTokenNameSuffix,
		ApiTokenUpdateEnabled: true,
		Tracer:                otel.Tracer("apitoken-controller"),
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	err = (&ReleaseChannelReconciler{
		Client:   k8sManager.GetClient(),
		Scheme:   k8sManager.GetScheme(),
		Recorder: k8sManager.GetEventRecorderFor("releasechannel-controller"),
		Tracer:   otel.Tracer("releasechannel-controller"),
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	go func() {
		defer GinkgoRecover()
		err = k8sManager.Start(ctx)
		Expect(err).ToNot(HaveOccurred(), "failed to run manager")
	}()
})

var _ = AfterSuite(func() {
	cancel()
	httpmock.DeactivateAndReset()
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})
