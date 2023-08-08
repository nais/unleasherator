package main

import (
	"context"
	"flag"
	"os"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/nais/unleasherator/controllers"
	"github.com/nais/unleasherator/pkg/config"
	//+kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(unleashv1.AddToScheme(scheme))
	utilruntime.Must(monitoringv1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

func main() {
	var configFile string
	flag.StringVar(&configFile, "config", "",
		"The controller will load its initial configuration from this file. "+
			"Omit this flag to use the default configuration values. "+
			"Command-line flags override configuration from this file.")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	// Load configuration from environment
	cfg, err := config.LoadFromEnv()
	if err != nil {
		setupLog.Error(err, "unable to load configuration from environment")
		os.Exit(1)
	}

	// Global parent context for program
	signalHandlerContext := ctrl.SetupSignalHandler()
	ctx, cancel := context.WithCancel(signalHandlerContext)
	defer cancel()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	options := ctrl.Options{Scheme: scheme}
	if configFile != "" {
		options, err = options.AndFrom(ctrl.ConfigFile().AtPath(configFile))
		if err != nil {
			setupLog.Error(err, "unable to load the config file")
			os.Exit(1)
		}
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), options)
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if cfg.OperatorNamespace == "" {
		setupLog.Error(err, "unable to get OPERATOR_NAMESPACE from environment")
		os.Exit(1)
	}

	subscriber, err := cfg.PubsubSubscriber(ctx)
	if err != nil {
		setupLog.Error(err, "create pubsub subscriber: %s", err)
		os.Exit(1)
	}
	defer subscriber.Close()

	publisher, err := cfg.PubsubPublisher(ctx)
	if err != nil {
		setupLog.Error(err, "create pubsub publisher: %s", err)
		os.Exit(1)
	}
	defer publisher.Close()

	if err = (&controllers.UnleashReconciler{
		Client:            mgr.GetClient(),
		Scheme:            mgr.GetScheme(),
		Recorder:          mgr.GetEventRecorderFor("unleash-controller"),
		OperatorNamespace: cfg.OperatorNamespace,
		Federation: controllers.UnleashFederation{
			Enabled:   cfg.Federation.IsEnabled(),
			Publisher: publisher,
		},
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Unleash")
		os.Exit(1)
	}
	remoteUnleashReconciler := &controllers.RemoteUnleashReconciler{
		Client:            mgr.GetClient(),
		Scheme:            mgr.GetScheme(),
		Recorder:          mgr.GetEventRecorderFor("remote-unleash-controller"),
		OperatorNamespace: cfg.OperatorNamespace,
		Federation: controllers.RemoteUnleashFederation{
			Enabled:     cfg.Federation.IsEnabled(),
			ClusterName: cfg.Federation.ClusterName,
			Subscriber:  subscriber,
		},
	}
	if err = remoteUnleashReconciler.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "RemoteUnleash")
		os.Exit(1)
	}
	if err = (&controllers.ApiTokenReconciler{
		Client:             mgr.GetClient(),
		Scheme:             mgr.GetScheme(),
		Recorder:           mgr.GetEventRecorderFor("api-token-controller"),
		OperatorNamespace:  cfg.OperatorNamespace,
		ApiTokenNameSuffix: cfg.ApiTokenNameSuffix,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ApiToken")
		os.Exit(1)
	}
	//+kubebuilder:scaffold:builder

	go func(ctx context.Context) {
		er := remoteUnleashReconciler.FederationSubscribe(ctx)
		if er != nil {
			setupLog.Error(err, "PubSub subscriber error, shutting down")
			cancel()
		}
	}(ctx)

	// @TODO add webhooks here

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
