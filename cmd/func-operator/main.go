/*
Copyright 2025.

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

package main

import (
	"context"
	"crypto/tls"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/functions-dev/func-operator/internal/git"
	"github.com/functions-dev/func-operator/internal/monitoring"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	v1 "k8s.io/api/core/v1"
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/certwatcher"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	functionsdevv1alpha1 "github.com/functions-dev/func-operator/api/v1alpha1"
	"github.com/functions-dev/func-operator/internal/controller"
	"github.com/functions-dev/func-operator/internal/funccli"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(functionsdevv1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme

	monitoring.RegisterMetrics()
}

type cliFlags struct {
	metricsAddr          string
	metricsCertPath      string
	metricsCertName      string
	metricsCertKey       string
	webhookCertPath      string
	webhookCertName      string
	webhookCertKey       string
	enableLeaderElection bool
	probeAddr            string
	secureMetrics        bool
	enableHTTP2          bool
	funcCLIPath          string
	funcCLICheckInterval time.Duration
	disableFuncCLIUpdate bool
}

func parseFlags() cliFlags {
	var flags cliFlags
	flag.StringVar(&flags.metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.StringVar(&flags.probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&flags.enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&flags.secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.StringVar(&flags.webhookCertPath, "webhook-cert-path", "", "The directory that contains the webhook certificate.")
	flag.StringVar(&flags.webhookCertName, "webhook-cert-name", "tls.crt", "The name of the webhook certificate file.")
	flag.StringVar(&flags.webhookCertKey, "webhook-cert-key", "tls.key", "The name of the webhook key file.")
	flag.StringVar(&flags.metricsCertPath, "metrics-cert-path", "",
		"The directory that contains the metrics server certificate.")
	flag.StringVar(&flags.metricsCertName, "metrics-cert-name", "tls.crt",
		"The name of the metrics server certificate file.")
	flag.StringVar(&flags.metricsCertKey, "metrics-cert-key", "tls.key", "The name of the metrics server key file.")
	flag.BoolVar(&flags.enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	flag.StringVar(&flags.funcCLIPath, "func-cli-path", filepath.Join(os.TempDir(), "func-operator", "bin"),
		"The directory where the func CLI binary will be installed")
	flag.DurationVar(&flags.funcCLICheckInterval, "func-cli-check-interval", 5*time.Minute,
		"How often to check for new func CLI versions")
	flag.BoolVar(&flags.disableFuncCLIUpdate, "disable-func-cli-update", false, "Disable the function-cli update")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	return flags
}

func setupTLSOptions(enableHTTP2 bool) []func(*tls.Config) {
	var tlsOpts []func(*tls.Config)

	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	if !enableHTTP2 {
		disableHTTP2 := func(c *tls.Config) {
			setupLog.Info("disabling http/2")
			c.NextProtos = []string{"http/1.1"}
		}
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	return tlsOpts
}

func setupWebhookCertWatcher(certPath, certName, certKey string) (*certwatcher.CertWatcher, error) {
	if len(certPath) == 0 {
		return nil, nil
	}

	setupLog.Info("Initializing webhook certificate watcher using provided certificates",
		"webhook-cert-path", certPath, "webhook-cert-name", certName, "webhook-cert-key", certKey)

	watcher, err := certwatcher.New(
		filepath.Join(certPath, certName),
		filepath.Join(certPath, certKey),
	)
	if err != nil {
		return nil, err
	}
	return watcher, nil
}

func setupWebhookServer(webhookCertWatcher *certwatcher.CertWatcher, tlsOpts []func(*tls.Config)) webhook.Server {
	webhookTLSOpts := tlsOpts
	if webhookCertWatcher != nil {
		webhookTLSOpts = append(webhookTLSOpts, func(config *tls.Config) {
			config.GetCertificate = webhookCertWatcher.GetCertificate
		})
	}
	return webhook.NewServer(webhook.Options{
		TLSOpts: webhookTLSOpts,
	})
}

func setupMetricsCertWatcher(certPath, certName, certKey string) (*certwatcher.CertWatcher, error) {
	// If the certificate is not specified, controller-runtime will automatically
	// generate self-signed certificates for the metrics server. While convenient for development and testing,
	// this setup is not recommended for production.
	//
	// TODO(user): If you enable certManager, uncomment the following lines:
	// - [METRICS-WITH-CERTS] at config/default/kustomization.yaml to generate and use certificates
	// managed by cert-manager for the metrics server.
	// - [PROMETHEUS-WITH-CERTS] at config/prometheus/kustomization.yaml for TLS certification.
	if len(certPath) == 0 {
		return nil, nil
	}

	setupLog.Info("Initializing metrics certificate watcher using provided certificates",
		"metrics-cert-path", certPath, "metrics-cert-name", certName, "metrics-cert-key", certKey)

	watcher, err := certwatcher.New(
		filepath.Join(certPath, certName),
		filepath.Join(certPath, certKey),
	)
	if err != nil {
		return nil, err
	}
	return watcher, nil
}

func setupMetricsServerOptions(
	metricsAddr string,
	secureMetrics bool,
	metricsCertWatcher *certwatcher.CertWatcher,
	tlsOpts []func(*tls.Config),
) metricsserver.Options {
	// Metrics endpoint is enabled in 'config/default/kustomization.yaml'. The Metrics options configure the server.
	// More info:
	// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.21.0/pkg/metrics/server
	// - https://book.kubebuilder.io/reference/metrics.html
	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       tlsOpts,
	}

	if secureMetrics {
		// FilterProvider is used to protect the metrics endpoint with authn/authz.
		// These configurations ensure that only authorized users and service accounts
		// can access the metrics endpoint. The RBAC are configured in 'config/rbac/kustomization.yaml'. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.21.0/pkg/metrics/filters#WithAuthenticationAndAuthorization
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	if metricsCertWatcher != nil {
		metricsServerOptions.TLSOpts = append(metricsServerOptions.TLSOpts, func(config *tls.Config) {
			config.GetCertificate = metricsCertWatcher.GetCertificate
		})
	}

	return metricsServerOptions
}

func getOperatorNamespace() string {
	operatorNamespace := os.Getenv("SYSTEM_NAMESPACE")
	if operatorNamespace == "" {
		setupLog.Info("Operator namespace not set, defaulting to func-operator-system")
		operatorNamespace = "func-operator-system"
	}
	return operatorNamespace
}

func setupCacheOptions(operatorNamespace string) cache.Options {
	watchNamespaces := getWatchNamespaces()
	var cacheOpts cache.Options
	if len(watchNamespaces) > 0 {
		setupLog.Info("Operator watching specific namespaces", "namespaces", watchNamespaces)

		// Map the namespaces into the Cache DefaultNamespaces map
		cacheOpts.DefaultNamespaces = make(map[string]cache.Config)
		for _, ns := range watchNamespaces {
			cacheOpts.DefaultNamespaces[strings.TrimSpace(ns)] = cache.Config{}
		}
	} else {
		setupLog.Info("Operator watching all namespaces")
	}

	// Always watch ConfigMaps in the operator's namespace so it can access the controller-config ConfigMap,
	// without affecting which namespaces Functions are watched in
	cacheOpts.ByObject = map[client.Object]cache.ByObject{
		&v1.ConfigMap{}: {
			Namespaces: map[string]cache.Config{
				operatorNamespace: {},
			},
		},
	}

	return cacheOpts
}

func addCertWatchers(mgr ctrl.Manager, metricsCertWatcher, webhookCertWatcher *certwatcher.CertWatcher) error {
	if metricsCertWatcher != nil {
		setupLog.Info("Adding metrics certificate watcher to manager")
		if err := mgr.Add(metricsCertWatcher); err != nil {
			return err
		}
	}

	if webhookCertWatcher != nil {
		setupLog.Info("Adding webhook certificate watcher to manager")
		if err := mgr.Add(webhookCertWatcher); err != nil {
			return err
		}
	}

	return nil
}

func main() {
	flags := parseFlags()

	tlsOpts := setupTLSOptions(flags.enableHTTP2)

	// Create watchers for metrics and webhooks certificates
	webhookCertWatcher, err := setupWebhookCertWatcher(flags.webhookCertPath, flags.webhookCertName, flags.webhookCertKey)
	if err != nil {
		setupLog.Error(err, "Failed to initialize webhook certificate watcher")
		os.Exit(1)
	}

	metricsCertWatcher, err := setupMetricsCertWatcher(flags.metricsCertPath, flags.metricsCertName, flags.metricsCertKey)
	if err != nil {
		setupLog.Error(err, "Failed to initialize metrics certificate watcher")
		os.Exit(1)
	}

	webhookServer := setupWebhookServer(webhookCertWatcher, tlsOpts)
	metricsServerOptions := setupMetricsServerOptions(flags.metricsAddr, flags.secureMetrics, metricsCertWatcher, tlsOpts)

	operatorNamespace := getOperatorNamespace()
	cacheOpts := setupCacheOptions(operatorNamespace)

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: flags.probeAddr,
		LeaderElection:         flags.enableLeaderElection,
		LeaderElectionID:       "668e89e1.functions.dev",
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the managerImpl ends. This requires the binary to immediately end when the
		// managerImpl is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,
		Cache: cacheOpts,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Initialize func CLI manager
	funcCLIManager, err := funccli.NewManager(
		ctrl.Log, flags.funcCLIPath, flags.funcCLICheckInterval, flags.disableFuncCLIUpdate)
	if err != nil {
		setupLog.Error(err, "unable to create func CLI manager")
		os.Exit(1)
	}

	// Ensure func CLI is ready before controllers start
	setupLog.Info("Downloading func CLI before starting controllers")
	ctx := context.Background()
	if err := funcCLIManager.EnsureReady(ctx); err != nil {
		setupLog.Error(err, "failed to ensure func CLI is ready")
		os.Exit(1)
	}
	setupLog.Info("Func CLI is ready")

	gitManager, err := git.NewManager()
	if err != nil {
		setupLog.Error(err, "failed to initialize git manager")
		os.Exit(1)
	}

	if err := (&controller.FunctionReconciler{
		Client:            mgr.GetClient(),
		Scheme:            mgr.GetScheme(),
		Recorder:          mgr.GetEventRecorder("functions-controller"),
		FuncCliManager:    funcCLIManager,
		GitManager:        gitManager,
		OperatorNamespace: operatorNamespace,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Function")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	if err := addCertWatchers(mgr, metricsCertWatcher, webhookCertWatcher); err != nil {
		setupLog.Error(err, "unable to add cert watchers to manager")
		os.Exit(1)
	}

	if err := mgr.Add(funcCLIManager); err != nil {
		setupLog.Error(err, "unable to add func CLI manager to manager")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

// getWatchNamespaces returns the Namespaces the operator should be watching for changes
func getWatchNamespaces() []string {
	watchNamespaceEnvVar := "WATCH_NAMESPACE"
	ns, found := os.LookupEnv(watchNamespaceEnvVar)
	if !found || ns == "" {
		return nil // Return nil to signify "watch all namespaces"
	}

	// Split by comma to support multiple namespaces
	return strings.Split(ns, ",")
}
