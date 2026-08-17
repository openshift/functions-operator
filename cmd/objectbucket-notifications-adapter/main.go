/*
Copyright 2026.

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

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
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

	sourcesv1alpha1 "github.com/functions-dev/func-operator/api/sources/v1alpha1"
	"github.com/functions-dev/func-operator/internal/objectbucketsource/config"
	"github.com/functions-dev/func-operator/internal/objectbucketsource/controller"
	"github.com/functions-dev/func-operator/internal/objectbucketsource/notificationserver"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(sourcesv1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

// nolint:gocyclo
func main() {
	var metricsAddr string
	var metricsCertPath, metricsCertName, metricsCertKey string
	var webhookCertPath, webhookCertName, webhookCertKey string
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	var configMapName string
	var createConfig bool
	var adapterPort int
	var notificationsMode string
	var kafkaBrokers string
	var kafkaNotificationsTopics string
	var kafkaNotificationsGroupID string
	var tlsOpts []func(*tls.Config)

	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.StringVar(&webhookCertPath, "webhook-cert-path", "", "The directory that contains the webhook certificate.")
	flag.StringVar(&webhookCertName, "webhook-cert-name", "tls.crt", "The name of the webhook certificate file.")
	flag.StringVar(&webhookCertKey, "webhook-cert-key", "tls.key", "The name of the webhook key file.")
	flag.StringVar(&metricsCertPath, "metrics-cert-path", "",
		"The directory that contains the metrics server certificate.")
	flag.StringVar(&metricsCertName, "metrics-cert-name", "tls.crt", "The name of the metrics server certificate file.")
	flag.StringVar(&metricsCertKey, "metrics-cert-key", "tls.key", "The name of the metrics server key file.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	flag.StringVar(&configMapName, "config", "objectbucket-notifications-adapter-config",
		"Name of the ConfigMap containing adapter configuration")
	flag.BoolVar(&createConfig, "create-config", false,
		"If set, create the default configuration ConfigMap at startup if it does not already exist.")
	flag.IntVar(&adapterPort, "adapter-port", 8888,
		"Port the notification HTTP server listens on (HTTP mode only)")
	flag.StringVar(&notificationsMode, "notifications-mode", "http",
		"http or kafka - selects how the adapter receives notifications. "+
			"Default for the NOTIFICATIONS_MODE ConfigMap key, which can override it at runtime.")
	flag.StringVar(&kafkaBrokers, "kafka-brokers", "",
		"Comma-separated list of Kafka broker addresses (required for Kafka mode). "+
			"Default for the KAFKA_BROKERS ConfigMap key, which can override it at runtime.")
	flag.StringVar(&kafkaNotificationsTopics, "kafka-notifications-topics", "",
		"Comma-separated list of Kafka topics to consume notifications from (required for Kafka mode). "+
			"Default for the KAFKA_NOTIFICATIONS_TOPICS ConfigMap key, which can override it at runtime.")
	flag.StringVar(&kafkaNotificationsGroupID, "kafka-notifications-group-id", "",
		"Consumer group ID for consuming notifications (required for Kafka mode). "+
			"Default for the KAFKA_NOTIFICATIONS_GROUP_ID ConfigMap key, which can override it at runtime.")

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("disabling http/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	var metricsCertWatcher, webhookCertWatcher *certwatcher.CertWatcher

	webhookTLSOpts := tlsOpts

	if len(webhookCertPath) > 0 {
		setupLog.Info("Initializing webhook certificate watcher using provided certificates",
			"webhook-cert-path", webhookCertPath, "webhook-cert-name", webhookCertName, "webhook-cert-key", webhookCertKey)

		var err error
		webhookCertWatcher, err = certwatcher.New(
			filepath.Join(webhookCertPath, webhookCertName),
			filepath.Join(webhookCertPath, webhookCertKey),
		)
		if err != nil {
			setupLog.Error(err, "Failed to initialize webhook certificate watcher")
			os.Exit(1)
		}

		webhookTLSOpts = append(webhookTLSOpts, func(config *tls.Config) {
			config.GetCertificate = webhookCertWatcher.GetCertificate
		})
	}

	webhookServer := webhook.NewServer(webhook.Options{
		TLSOpts: webhookTLSOpts,
	})

	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       tlsOpts,
	}

	if secureMetrics {
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	if len(metricsCertPath) > 0 {
		setupLog.Info("Initializing metrics certificate watcher using provided certificates",
			"metrics-cert-path", metricsCertPath, "metrics-cert-name", metricsCertName, "metrics-cert-key", metricsCertKey)

		var err error
		metricsCertWatcher, err = certwatcher.New(
			filepath.Join(metricsCertPath, metricsCertName),
			filepath.Join(metricsCertPath, metricsCertKey),
		)
		if err != nil {
			setupLog.Error(err, "to initialize metrics certificate watcher", "error", err)
			os.Exit(1)
		}

		metricsServerOptions.TLSOpts = append(metricsServerOptions.TLSOpts, func(config *tls.Config) {
			config.GetCertificate = metricsCertWatcher.GetCertificate
		})
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "dd939d79.functions.dev",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// The command-line flags provide the defaults for the notification settings.
	// The actual values are resolved from the ConfigMap (falling back to these
	// defaults) and can be changed at runtime. Validation happens in the config
	// provider when the ConfigMap is loaded.
	notificationDefaults := config.NotificationSettings{
		Mode:                      notificationsMode,
		KafkaBrokers:              splitAndTrim(kafkaBrokers),
		KafkaNotificationsTopics:  splitAndTrim(kafkaNotificationsTopics),
		KafkaNotificationsGroupID: kafkaNotificationsGroupID,
	}

	// Determine namespace for ConfigMap
	ns := os.Getenv("POD_NAMESPACE")
	if ns == "" {
		nsBytes, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
		if err != nil {
			setupLog.Error(err, "cannot determine pod namespace")
			os.Exit(1)
		}
		ns = strings.TrimSpace(string(nsBytes))
	}

	// Optionally create the default configuration ConfigMap before the provider
	// tries to read it.
	if createConfig {
		if err := config.EnsureDefaultConfigMap(context.Background(), ns, configMapName, notificationDefaults); err != nil {
			setupLog.Error(err, "failed to ensure default configuration ConfigMap")
			os.Exit(1)
		}
	}

	// Create configuration provider
	configProvider, err := config.NewProvider(context.Background(), ns, configMapName, notificationDefaults)
	if err != nil {
		setupLog.Error(err, "failed to create configuration provider")
		os.Exit(1)
	}

	// Add config provider to manager
	if err := mgr.Add(configProvider); err != nil {
		setupLog.Error(err, "unable to add config provider to manager")
		os.Exit(1)
	}

	if err := (&controller.ObjectBucketSourceReconciler{
		Client:         mgr.GetClient(),
		Scheme:         mgr.GetScheme(),
		ConfigProvider: configProvider,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ObjectBucketSource")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	notifServer := &notificationserver.NotificationServer{
		Client:         mgr.GetClient(),
		Port:           adapterPort,
		ConfigProvider: configProvider,
	}
	if err := mgr.Add(notifServer); err != nil {
		setupLog.Error(err, "unable to add notification server")
		os.Exit(1)
	}

	if metricsCertWatcher != nil {
		setupLog.Info("Adding metrics certificate watcher to manager")
		if err := mgr.Add(metricsCertWatcher); err != nil {
			setupLog.Error(err, "unable to add metrics certificate watcher to manager")
			os.Exit(1)
		}
	}

	if webhookCertWatcher != nil {
		setupLog.Info("Adding webhook certificate watcher to manager")
		if err := mgr.Add(webhookCertWatcher); err != nil {
			setupLog.Error(err, "unable to add webhook certificate watcher to manager")
			os.Exit(1)
		}
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

// splitAndTrim splits a comma-separated flag value, trimming whitespace and
// dropping empty entries.
func splitAndTrim(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
