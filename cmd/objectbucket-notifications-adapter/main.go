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
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/certwatcher"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/IBM/sarama"

	sourcesv1alpha1 "github.com/functions-dev/func-operator/api/sources/v1alpha1"
	"github.com/functions-dev/func-operator/internal/objectbucketsource/controller"
	kafkaconfig "github.com/functions-dev/func-operator/internal/objectbucketsource/kafka"
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

	noobaaAdapterID := envOrDefault("NOOBAA_ADAPTER_ID", "mcg-adapter")
	noobaaAdapterTopic := envOrDefault("NOOBAA_ADAPTER_TOPIC_ARN", "mcg-adapter-connection/connect.json")
	noobaaStorageClassPattern := envOrDefault("NOOBAA_ADAPTER_STORAGECLASS_PATTERN", `.*noobaa\.io$`)

	radosgwAdapterID := envOrDefault("RADOSGW_ADAPTER_ID", "rgw-adapter")
	radosgwAdapterTopic := envOrDefault("RADOSGW_ADAPTER_TOPIC_ARN",
		"arn:aws:sns:ocs-storagecluster-cephobjectstore::rgw-adapter-notifications")
	radosgwStorageClassPattern := envOrDefault("RADOSGW_ADAPTER_STORAGECLASS_PATTERN", `.*ceph-rgw$`)

	adapterConfigs := make([]controller.AdapterConfig, 0, 2)
	for _, cfg := range []struct {
		id, topic, pattern string
	}{
		{noobaaAdapterID, noobaaAdapterTopic, noobaaStorageClassPattern},
		{radosgwAdapterID, radosgwAdapterTopic, radosgwStorageClassPattern},
	} {
		re, err := regexp.Compile(cfg.pattern)
		if err != nil {
			setupLog.Error(err, "invalid storageclass pattern", "pattern", cfg.pattern)
			os.Exit(1)
		}
		adapterConfigs = append(adapterConfigs, controller.AdapterConfig{
			ID:                  cfg.id,
			Topic:               cfg.topic,
			StorageClassPattern: re,
		})
	}

	adapterPort := 8888
	if portStr := os.Getenv("ADAPTER_PORT"); portStr != "" {
		var err error
		adapterPort, err = strconv.Atoi(portStr)
		if err != nil {
			setupLog.Error(err, "invalid ADAPTER_PORT")
			os.Exit(1)
		}
	}
	notificationsMode := os.Getenv("NOTIFICATIONS_MODE")
	if notificationsMode == "" {
		notificationsMode = "http"
	}
	if notificationsMode != "http" && notificationsMode != "kafka" {
		setupLog.Error(fmt.Errorf("invalid NOTIFICATIONS_MODE %q", notificationsMode), "must be \"http\" or \"kafka\"")
		os.Exit(1)
	}

	var kafkaNotificationsTopics []string
	if topicsStr := os.Getenv("KAFKA_NOTIFICATIONS_TOPIC"); topicsStr != "" {
		for _, t := range strings.Split(topicsStr, ",") {
			if trimmed := strings.TrimSpace(t); trimmed != "" {
				kafkaNotificationsTopics = append(kafkaNotificationsTopics, trimmed)
			}
		}
	}
	kafkaNotificationsGroupID := os.Getenv("KAFKA_NOTIFICATIONS_GROUP_ID")

	var kafkaBrokers []string
	if brokersStr := os.Getenv("KAFKA_BROKERS"); brokersStr != "" {
		kafkaBrokers = strings.Split(brokersStr, ",")
	}

	var kafkaCfg *sarama.Config
	if kafkaSecretName := os.Getenv("KAFKA_SECRET"); kafkaSecretName != "" {
		ns := os.Getenv("POD_NAMESPACE")
		if ns == "" {
			nsBytes, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
			if err != nil {
				setupLog.Error(err, "cannot determine pod namespace for KAFKA_SECRET")
				os.Exit(1)
			}
			ns = strings.TrimSpace(string(nsBytes))
		}
		clientset, err := kubernetes.NewForConfig(ctrl.GetConfigOrDie())
		if err != nil {
			setupLog.Error(err, "creating kubernetes clientset for KAFKA_SECRET")
			os.Exit(1)
		}
		secret, err := clientset.CoreV1().Secrets(ns).Get(context.Background(), kafkaSecretName, metav1.GetOptions{})
		if err != nil {
			setupLog.Error(err, "reading KAFKA_SECRET", "name", kafkaSecretName, "namespace", ns)
			os.Exit(1)
		}
		kafkaCfg, err = kafkaconfig.NewConfig(secret.Data)
		if err != nil {
			setupLog.Error(err, "configuring kafka from secret", "name", kafkaSecretName)
			os.Exit(1)
		}
		setupLog.Info("kafka configured from secret", "name", kafkaSecretName, "namespace", ns)
	} else {
		var err error
		kafkaCfg, err = kafkaconfig.NewConfig(nil)
		if err != nil {
			setupLog.Error(err, "creating default kafka config")
			os.Exit(1)
		}
	}

	if err := (&controller.ObjectBucketSourceReconciler{
		Client:         mgr.GetClient(),
		Scheme:         mgr.GetScheme(),
		AdapterConfigs: adapterConfigs,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ObjectBucketSource")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	if notificationsMode == "kafka" {
		if len(kafkaNotificationsTopics) == 0 {
			setupLog.Error(fmt.Errorf("KAFKA_NOTIFICATIONS_TOPIC is required when NOTIFICATIONS_MODE=kafka"), "missing env")
			os.Exit(1)
		}
		if kafkaNotificationsGroupID == "" {
			setupLog.Error(fmt.Errorf("KAFKA_NOTIFICATIONS_GROUP_ID is required when NOTIFICATIONS_MODE=kafka"), "missing env")
			os.Exit(1)
		}
		if len(kafkaBrokers) == 0 {
			setupLog.Error(fmt.Errorf("KAFKA_BROKERS is required when NOTIFICATIONS_MODE=kafka"), "missing env")
			os.Exit(1)
		}
	}

	notifServer := &notificationserver.NotificationServer{
		Client:                    mgr.GetClient(),
		Port:                      adapterPort,
		KafkaBrokers:              kafkaBrokers,
		KafkaConfig:               kafkaCfg,
		NotificationsMode:         notificationsMode,
		KafkaNotificationsTopics:  kafkaNotificationsTopics,
		KafkaNotificationsGroupID: kafkaNotificationsGroupID,
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

func envOrDefault(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}
