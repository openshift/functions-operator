package config

import (
	"regexp"
	"sync"

	"github.com/IBM/sarama"
)

// MockProvider is a simple mock implementation of the config provider for testing
type MockProvider struct {
	mu               sync.RWMutex
	config           Config
	kafkaConfig      *sarama.Config
	kafkaFingerprint string
	subscribers      []chan struct{}
}

// NewMockProvider creates a mock provider with default test configuration
func NewMockProvider() *MockProvider {
	noobaaRe := regexp.MustCompile(`.*noobaa\.io$`)
	radosgwRe := regexp.MustCompile(`.*ceph-rgw$`)

	return &MockProvider{
		config: Config{
			NoobaaAdapter: AdapterBackendConfig{
				ID:                  "mcg-adapter",
				TopicARN:            "mcg-adapter-connection/connect.json",
				StorageClassPattern: noobaaRe,
			},
			RadosgwAdapter: AdapterBackendConfig{
				ID:                  "rgw-adapter",
				TopicARN:            "arn:aws:sns:ocs-storagecluster-cephobjectstore::rgw-adapter-notifications",
				StorageClassPattern: radosgwRe,
			},
			Notifications: NotificationSettings{
				Mode: "http",
			},
		},
		kafkaConfig: sarama.NewConfig(),
	}
}

// GetConfig returns the mock configuration
func (m *MockProvider) GetConfig() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config
}

// GetKafkaConfig returns the mock Kafka configuration
func (m *MockProvider) GetKafkaConfig() *sarama.Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.kafkaConfig
}

// GetKafkaFingerprint returns the mock Kafka fingerprint
func (m *MockProvider) GetKafkaFingerprint() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.kafkaFingerprint
}

// Subscribe returns a channel that is signaled whenever the mock configuration changes.
func (m *MockProvider) Subscribe() <-chan struct{} {
	ch := make(chan struct{}, 1)
	m.mu.Lock()
	m.subscribers = append(m.subscribers, ch)
	m.mu.Unlock()
	return ch
}

func (m *MockProvider) notify() {
	for _, ch := range m.subscribers {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// SetConfig updates the mock configuration (for testing)
func (m *MockProvider) SetConfig(cfg Config) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config = cfg
	m.notify()
}

// SetKafkaConfig updates the mock Kafka configuration (for testing)
func (m *MockProvider) SetKafkaConfig(cfg *sarama.Config) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.kafkaConfig = cfg
	m.notify()
}

// SetKafkaFingerprint updates the mock Kafka fingerprint (for testing)
func (m *MockProvider) SetKafkaFingerprint(fingerprint string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.kafkaFingerprint = fingerprint
	m.notify()
}
