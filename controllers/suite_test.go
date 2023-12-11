package controllers

import (
	"context"
	"path/filepath"
	"testing"
	"time"

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
	"github.com/nais/unleasherator/pkg/config"
	mockfederation "github.com/nais/unleasherator/pkg/federation/mockfediration"
	//+kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

const operatorNamespace = "default"

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
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.TODO())

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "config", "crd", "bases"),
			filepath.Join("..", "config", "prometheus", "crd"),
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
		OperatorNamespace: operatorNamespace,
		Recorder:          k8sManager.GetEventRecorderFor("unleash-controller"),
		Federation: UnleashFederation{
			Enabled:   true,
			Publisher: mockPublisher,
		},
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	remoteUnleashReconciler = &RemoteUnleashReconciler{
		Client:            k8sManager.GetClient(),
		Scheme:            k8sManager.GetScheme(),
		OperatorNamespace: operatorNamespace,
		Timeout:           timeout,
		Federation: RemoteUnleashFederation{
			Enabled:     true,
			ClusterName: "test-cluster",
			Subscriber:  mockSubscriber,
		},
	}
	err = remoteUnleashReconciler.SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	err = (&ApiTokenReconciler{
		Client:                k8sManager.GetClient(),
		Scheme:                k8sManager.GetScheme(),
		OperatorNamespace:     operatorNamespace,
		ApiTokenNameSuffix:    ApiTokenNameSuffix,
		ApiTokenUpdateEnabled: true,
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
