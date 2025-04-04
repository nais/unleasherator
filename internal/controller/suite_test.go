package controller

import (
	"context"
	"path/filepath"
	"testing"
	"time"

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
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true), zap.JSONEncoder()))

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

	go func() {
		defer GinkgoRecover()
		err = k8sManager.Start(ctx)
		Expect(err).ToNot(HaveOccurred(), "failed to run manager")
	}()
})

var _ = AfterSuite(func() {
	cancel()
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})
