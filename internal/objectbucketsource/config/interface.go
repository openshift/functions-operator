package config

import "github.com/IBM/sarama"

// ConfigProvider provides access to adapter configuration
type ConfigProvider interface {
	GetConfig() Config
	GetKafkaConfig() *sarama.Config
}
