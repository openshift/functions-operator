package config

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/IBM/sarama"
	kafkaconfig "github.com/functions-dev/func-operator/internal/objectbucketsource/kafka"
)

var log = logf.Log.WithName("adapter-config")

// Default values for the adapter configuration. They are used both when resolving
// missing ConfigMap keys and when creating the default ConfigMap (--create-config).
const (
	defaultNoobaaAdapterID       = "mcg-adapter"
	defaultNoobaaTopicARN        = "mcg-adapter-connection/connect.json"
	defaultNoobaaStorageClassRE  = `.*noobaa\.io$`
	defaultRadosgwAdapterID      = "rgw-adapter"
	defaultRadosgwTopicARN       = "arn:aws:sns:ocs-storagecluster-cephobjectstore::rgw-adapter-notifications"
	defaultRadosgwStorageClassRE = `.*ceph-rgw$`
)

// AdapterBackendConfig holds configuration for a single storage backend adapter
type AdapterBackendConfig struct {
	ID                  string
	TopicARN            string
	StorageClassPattern *regexp.Regexp
}

// NotificationSettings holds the transport configuration that controls how the
// adapter receives NooBaa/RadosGW notifications. These settings can be changed at
// runtime via the ConfigMap; the notification server restarts its Kafka consumer
// when any of them change.
type NotificationSettings struct {
	// Mode is "http" or "kafka".
	Mode                      string
	KafkaBrokers              []string
	KafkaNotificationsTopics  []string
	KafkaNotificationsGroupID string
}

// Config holds runtime-configurable settings for the objectbucket-notifications-adapter
type Config struct {
	NoobaaAdapter  AdapterBackendConfig
	RadosgwAdapter AdapterBackendConfig
	Notifications  NotificationSettings
}

// Provider provides access to the current configuration and watches for changes
type Provider struct {
	mu     sync.RWMutex
	config Config

	namespace     string
	configMapName string
	clientset     *kubernetes.Clientset
	cancelWatch   context.CancelFunc

	// defaults holds the notification settings supplied via command-line flags.
	// They are used whenever the corresponding ConfigMap keys are absent.
	defaults NotificationSettings

	kafkaConfigMu sync.RWMutex
	kafkaConfig   *sarama.Config
	kafkaSecret   string
	// kafkaFingerprint is a content hash of the Kafka secret name and its data.
	// It changes whenever the Kafka credentials/connection settings change, which
	// lets consumers detect credential rotation and restart their connections.
	kafkaFingerprint string

	subscribersMu sync.Mutex
	subscribers   []chan struct{}

	// reloadSignal is fired internally after every successful reload so the
	// Secret watcher can re-evaluate which Secret it should be watching.
	reloadSignal chan struct{}
}

// NewProvider creates a new configuration provider that watches a ConfigMap.
// The defaults are used for any notification settings not present in the ConfigMap.
func NewProvider(ctx context.Context, namespace, configMapName string, defaults NotificationSettings) (*Provider, error) {
	clientset, err := kubernetes.NewForConfig(ctrl.GetConfigOrDie())
	if err != nil {
		return nil, fmt.Errorf("creating kubernetes clientset: %w", err)
	}

	p := &Provider{
		namespace:     namespace,
		configMapName: configMapName,
		clientset:     clientset,
		defaults:      defaults,
		reloadSignal:  make(chan struct{}, 1),
	}

	if err := p.loadConfig(ctx); err != nil {
		return nil, fmt.Errorf("loading initial config: %w", err)
	}

	watchCtx, cancel := context.WithCancel(context.Background())
	p.cancelWatch = cancel
	go p.watchConfigMap(watchCtx)
	go p.watchSecret(watchCtx)

	return p, nil
}

// EnsureDefaultConfigMap creates the adapter configuration ConfigMap with default
// values if it does not already exist. It is a no-op when the ConfigMap is already
// present. The notification-related defaults (from the command-line flags) are
// written into the ConfigMap so it reflects the effective startup configuration.
func EnsureDefaultConfigMap(ctx context.Context, namespace, configMapName string, defaults NotificationSettings) error {
	clientset, err := kubernetes.NewForConfig(ctrl.GetConfigOrDie())
	if err != nil {
		return fmt.Errorf("creating kubernetes clientset: %w", err)
	}

	_, err = clientset.CoreV1().ConfigMaps(namespace).Get(ctx, configMapName, metav1.GetOptions{})
	if err == nil {
		log.Info("configuration ConfigMap already exists, not creating", "name", configMapName, "namespace", namespace)
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("checking for ConfigMap %s/%s: %w", namespace, configMapName, err)
	}

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: namespace,
		},
		Data: defaultConfigMapData(defaults),
	}

	if _, err := clientset.CoreV1().ConfigMaps(namespace).Create(ctx, cm, metav1.CreateOptions{}); err != nil {
		if apierrors.IsAlreadyExists(err) {
			// Another replica created it concurrently; treat as success.
			log.Info("configuration ConfigMap already created concurrently", "name", configMapName, "namespace", namespace)
			return nil
		}
		return fmt.Errorf("creating default ConfigMap %s/%s: %w", namespace, configMapName, err)
	}

	log.Info("created default configuration ConfigMap", "name", configMapName, "namespace", namespace)
	return nil
}

// defaultConfigMapData builds the data map for the default configuration ConfigMap.
func defaultConfigMapData(defaults NotificationSettings) map[string]string {
	data := map[string]string{
		"NOOBAA_ADAPTER_ID":                    defaultNoobaaAdapterID,
		"NOOBAA_ADAPTER_TOPIC_ARN":             defaultNoobaaTopicARN,
		"NOOBAA_ADAPTER_STORAGECLASS_PATTERN":  defaultNoobaaStorageClassRE,
		"RADOSGW_ADAPTER_ID":                   defaultRadosgwAdapterID,
		"RADOSGW_ADAPTER_TOPIC_ARN":            defaultRadosgwTopicARN,
		"RADOSGW_ADAPTER_STORAGECLASS_PATTERN": defaultRadosgwStorageClassRE,
	}

	mode := defaults.Mode
	if mode == "" {
		mode = "http"
	}
	data["NOTIFICATIONS_MODE"] = mode

	if len(defaults.KafkaBrokers) > 0 {
		data["KAFKA_BROKERS"] = strings.Join(defaults.KafkaBrokers, ",")
	}
	if len(defaults.KafkaNotificationsTopics) > 0 {
		data["KAFKA_NOTIFICATIONS_TOPICS"] = strings.Join(defaults.KafkaNotificationsTopics, ",")
	}
	if defaults.KafkaNotificationsGroupID != "" {
		data["KAFKA_NOTIFICATIONS_GROUP_ID"] = defaults.KafkaNotificationsGroupID
	}

	return data
}

// GetConfig returns a copy of the current configuration
func (p *Provider) GetConfig() Config {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.config
}

// GetKafkaConfig returns the current Kafka configuration
func (p *Provider) GetKafkaConfig() *sarama.Config {
	p.kafkaConfigMu.RLock()
	defer p.kafkaConfigMu.RUnlock()
	return p.kafkaConfig
}

// GetKafkaFingerprint returns a content hash of the current Kafka secret. It
// changes whenever the Kafka credentials/connection settings change, allowing
// callers to detect credential rotation.
func (p *Provider) GetKafkaFingerprint() string {
	p.kafkaConfigMu.RLock()
	defer p.kafkaConfigMu.RUnlock()
	return p.kafkaFingerprint
}

// Subscribe returns a channel that receives a signal whenever the configuration
// is successfully reloaded. The channel is buffered (size 1) and signals are
// coalesced, so a slow subscriber never blocks the config watcher.
func (p *Provider) Subscribe() <-chan struct{} {
	ch := make(chan struct{}, 1)
	p.subscribersMu.Lock()
	p.subscribers = append(p.subscribers, ch)
	p.subscribersMu.Unlock()
	return ch
}

func (p *Provider) notifySubscribers() {
	p.subscribersMu.Lock()
	defer p.subscribersMu.Unlock()
	for _, ch := range p.subscribers {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// Stop stops watching the ConfigMap
func (p *Provider) Stop() {
	if p.cancelWatch != nil {
		p.cancelWatch()
	}
}

// NeedLeaderElection implements the manager.Runnable interface
func (p *Provider) NeedLeaderElection() bool {
	return false
}

// Start implements the manager.Runnable interface
func (p *Provider) Start(ctx context.Context) error {
	<-ctx.Done()
	p.Stop()
	return nil
}

func (p *Provider) loadConfig(ctx context.Context) error {
	cm, err := p.clientset.CoreV1().ConfigMaps(p.namespace).Get(ctx, p.configMapName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("getting ConfigMap %s/%s: %w", p.namespace, p.configMapName, err)
	}

	config, err := parseConfig(cm, p.defaults)
	if err != nil {
		return fmt.Errorf("parsing ConfigMap: %w", err)
	}

	kafkaSecret := cm.Data["KAFKA_SECRET"]
	var kafkaCfg *sarama.Config
	var secretData map[string][]byte
	if kafkaSecret != "" {
		secret, err := p.clientset.CoreV1().Secrets(p.namespace).Get(ctx, kafkaSecret, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("reading KAFKA_SECRET %s/%s: %w", p.namespace, kafkaSecret, err)
		}
		secretData = secret.Data
		kafkaCfg, err = kafkaconfig.NewConfig(secretData)
		if err != nil {
			return fmt.Errorf("configuring kafka from secret %s: %w", kafkaSecret, err)
		}
		log.Info("kafka configured from secret", "name", kafkaSecret, "namespace", p.namespace)
	} else {
		kafkaCfg, err = kafkaconfig.NewConfig(nil)
		if err != nil {
			return fmt.Errorf("creating default kafka config: %w", err)
		}
	}
	fingerprint := kafkaFingerprint(kafkaSecret, secretData)

	p.mu.Lock()
	p.config = config
	p.mu.Unlock()

	p.kafkaConfigMu.Lock()
	p.kafkaConfig = kafkaCfg
	p.kafkaSecret = kafkaSecret
	p.kafkaFingerprint = fingerprint
	p.kafkaConfigMu.Unlock()

	log.Info("configuration loaded",
		"noobaa-adapter-id", config.NoobaaAdapter.ID,
		"radosgw-adapter-id", config.RadosgwAdapter.ID,
		"notifications-mode", config.Notifications.Mode,
		"kafka-brokers", config.Notifications.KafkaBrokers,
		"kafka-notifications-topics", config.Notifications.KafkaNotificationsTopics,
		"kafka-notifications-group-id", config.Notifications.KafkaNotificationsGroupID)

	p.notifySubscribers()
	p.signalReload()

	return nil
}

// signalReload notifies the Secret watcher (non-blocking) that the configuration
// was reloaded so it can re-evaluate which Secret to watch.
func (p *Provider) signalReload() {
	if p.reloadSignal == nil {
		return
	}
	select {
	case p.reloadSignal <- struct{}{}:
	default:
	}
}

func (p *Provider) currentKafkaSecret() string {
	p.kafkaConfigMu.RLock()
	defer p.kafkaConfigMu.RUnlock()
	return p.kafkaSecret
}

func (p *Provider) watchConfigMap(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		watcher, err := p.clientset.CoreV1().ConfigMaps(p.namespace).Watch(ctx, metav1.ListOptions{
			FieldSelector: fmt.Sprintf("metadata.name=%s", p.configMapName),
		})
		if err != nil {
			log.Error(err, "failed to create ConfigMap watcher, retrying in 5s")
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}

		log.Info("watching ConfigMap for changes", "name", p.configMapName, "namespace", p.namespace)

		func() {
			defer watcher.Stop()

			for {
				select {
				case <-ctx.Done():
					return
				case event, ok := <-watcher.ResultChan():
					if !ok {
						log.Info("ConfigMap watch channel closed, restarting watcher")
						return
					}

					if event.Type == watch.Modified || event.Type == watch.Added {
						cm, ok := event.Object.(*corev1.ConfigMap)
						if !ok {
							log.Error(fmt.Errorf("unexpected object type"), "failed to cast to ConfigMap")
							continue
						}

						log.Info("ConfigMap changed, reloading configuration", "name", cm.Name)
						if err := p.loadConfig(ctx); err != nil {
							log.Error(err, "failed to reload configuration")
						} else {
							log.Info("configuration reloaded successfully")
						}
					} else if event.Type == watch.Deleted {
						log.Error(fmt.Errorf("ConfigMap deleted"), "adapter configuration unavailable", "name", p.configMapName)
					}
				}
			}
		}()
	}
}

// watchSecret watches the Kafka Secret currently referenced by KAFKA_SECRET and
// reloads the configuration when its contents change, so an in-place credential
// rotation is picked up without requiring a ConfigMap edit. The watched Secret
// name is dynamic: when a ConfigMap reload changes (or clears) the reference, the
// watcher re-establishes itself against the new Secret.
func (p *Provider) watchSecret(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		secretName := p.currentKafkaSecret()
		if secretName == "" {
			// No Secret referenced; wait until a reload might add one.
			select {
			case <-ctx.Done():
				return
			case <-p.reloadSignal:
				continue
			}
		}

		watcher, err := p.clientset.CoreV1().Secrets(p.namespace).Watch(ctx, metav1.ListOptions{
			FieldSelector: fmt.Sprintf("metadata.name=%s", secretName),
		})
		if err != nil {
			log.Error(err, "failed to create Secret watcher, retrying in 5s", "secret", secretName)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}

		log.Info("watching Kafka Secret for changes", "name", secretName, "namespace", p.namespace)
		p.consumeSecretEvents(ctx, watcher, secretName)
	}
}

func (p *Provider) consumeSecretEvents(ctx context.Context, watcher watch.Interface, secretName string) {
	defer watcher.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-p.reloadSignal:
			// A reload happened; the referenced Secret may have changed. If so,
			// restart the watcher against the new Secret.
			if p.currentKafkaSecret() != secretName {
				log.Info("Kafka Secret reference changed, restarting Secret watcher",
					"old", secretName, "new", p.currentKafkaSecret())
				return
			}
		case event, ok := <-watcher.ResultChan():
			if !ok {
				log.Info("Secret watch channel closed, restarting watcher", "secret", secretName)
				return
			}

			switch event.Type {
			case watch.Modified, watch.Added:
				log.Info("Kafka Secret changed, reloading configuration", "name", secretName)
				if err := p.loadConfig(ctx); err != nil {
					log.Error(err, "failed to reload configuration after Secret change")
				} else {
					log.Info("configuration reloaded successfully after Secret change")
				}
			case watch.Deleted:
				log.Error(fmt.Errorf("kafka secret deleted"), "kafka credentials unavailable", "name", secretName)
			}
		}
	}
}

func parseConfig(cm *corev1.ConfigMap, defaults NotificationSettings) (Config, error) {
	noobaaPattern := getOrDefault(cm.Data, "NOOBAA_ADAPTER_STORAGECLASS_PATTERN", defaultNoobaaStorageClassRE)
	radosgwPattern := getOrDefault(cm.Data, "RADOSGW_ADAPTER_STORAGECLASS_PATTERN", defaultRadosgwStorageClassRE)

	noobaaRe, err := regexp.Compile(noobaaPattern)
	if err != nil {
		return Config{}, fmt.Errorf("invalid NOOBAA_ADAPTER_STORAGECLASS_PATTERN: %w", err)
	}

	radosgwRe, err := regexp.Compile(radosgwPattern)
	if err != nil {
		return Config{}, fmt.Errorf("invalid RADOSGW_ADAPTER_STORAGECLASS_PATTERN: %w", err)
	}

	notifications, err := parseNotificationSettings(cm.Data, defaults)
	if err != nil {
		return Config{}, err
	}

	config := Config{
		NoobaaAdapter: AdapterBackendConfig{
			ID:                  getOrDefault(cm.Data, "NOOBAA_ADAPTER_ID", defaultNoobaaAdapterID),
			TopicARN:            getOrDefault(cm.Data, "NOOBAA_ADAPTER_TOPIC_ARN", defaultNoobaaTopicARN),
			StorageClassPattern: noobaaRe,
		},
		RadosgwAdapter: AdapterBackendConfig{
			ID:                  getOrDefault(cm.Data, "RADOSGW_ADAPTER_ID", defaultRadosgwAdapterID),
			TopicARN:            getOrDefault(cm.Data, "RADOSGW_ADAPTER_TOPIC_ARN", defaultRadosgwTopicARN),
			StorageClassPattern: radosgwRe,
		},
		Notifications: notifications,
	}

	return config, nil
}

// parseNotificationSettings resolves the notification transport settings from the
// ConfigMap, falling back to the provided defaults (from command-line flags) when
// a key is absent. It validates the resulting settings so that invalid ConfigMap
// changes are rejected and the previous valid configuration is retained.
func parseNotificationSettings(data map[string]string, defaults NotificationSettings) (NotificationSettings, error) {
	mode := getOrDefault(data, "NOTIFICATIONS_MODE", defaults.Mode)
	if mode == "" {
		mode = "http"
	}
	if mode != "http" && mode != "kafka" {
		return NotificationSettings{}, fmt.Errorf("invalid NOTIFICATIONS_MODE %q: must be \"http\" or \"kafka\"", mode)
	}

	settings := NotificationSettings{
		Mode:                      mode,
		KafkaBrokers:              defaults.KafkaBrokers,
		KafkaNotificationsTopics:  defaults.KafkaNotificationsTopics,
		KafkaNotificationsGroupID: getOrDefault(data, "KAFKA_NOTIFICATIONS_GROUP_ID", defaults.KafkaNotificationsGroupID),
	}
	if v, ok := data["KAFKA_BROKERS"]; ok && strings.TrimSpace(v) != "" {
		settings.KafkaBrokers = splitAndTrim(v)
	}
	if v, ok := data["KAFKA_NOTIFICATIONS_TOPICS"]; ok && strings.TrimSpace(v) != "" {
		settings.KafkaNotificationsTopics = splitAndTrim(v)
	}

	if mode == "kafka" {
		if len(settings.KafkaBrokers) == 0 {
			return NotificationSettings{}, fmt.Errorf("KAFKA_BROKERS is required when NOTIFICATIONS_MODE=kafka")
		}
		if len(settings.KafkaNotificationsTopics) == 0 {
			return NotificationSettings{}, fmt.Errorf("KAFKA_NOTIFICATIONS_TOPICS is required when NOTIFICATIONS_MODE=kafka")
		}
		if settings.KafkaNotificationsGroupID == "" {
			return NotificationSettings{}, fmt.Errorf("KAFKA_NOTIFICATIONS_GROUP_ID is required when NOTIFICATIONS_MODE=kafka")
		}
	}

	return settings, nil
}

func getOrDefault(data map[string]string, key, defaultValue string) string {
	if v, ok := data[key]; ok && v != "" {
		return v
	}
	return defaultValue
}

// kafkaFingerprint returns a stable content hash of the Kafka secret name and its
// data. Two calls return the same value iff the secret reference and its contents
// are identical, so a change indicates the Kafka credentials/connection settings
// were rotated.
func kafkaFingerprint(secretName string, data map[string][]byte) string {
	h := sha256.New()
	h.Write([]byte(secretName))
	h.Write([]byte{0})

	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte{0})
		h.Write(data[k])
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// splitAndTrim splits a comma-separated string, trimming whitespace and dropping
// empty entries.
func splitAndTrim(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
