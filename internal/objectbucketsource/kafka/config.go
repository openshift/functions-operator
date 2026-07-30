package kafka

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"strings"

	"github.com/IBM/sarama"
	"github.com/xdg-go/scram"
)

const (
	SASLMechanismPlain  = "PLAIN"
	SASLMechanismSHA256 = "SCRAM-SHA-256"
	SASLMechanismSHA512 = "SCRAM-SHA-512"
)

func NewConfig(secretData map[string][]byte) (*sarama.Config, error) {
	config := sarama.NewConfig()
	config.Producer.Return.Successes = true

	if len(secretData) == 0 {
		return config, nil
	}

	protocol := strings.TrimSpace(string(secretData["protocol"]))
	if protocol == "" {
		return nil, fmt.Errorf("kafka secret missing required key \"protocol\"")
	}

	switch protocol {
	case "PLAINTEXT":
		return config, nil
	case "SASL_PLAINTEXT":
		if err := configureSASL(config, secretData); err != nil {
			return nil, err
		}
	case "SSL":
		if err := configureTLS(config, secretData, true); err != nil {
			return nil, err
		}
	case "SASL_SSL":
		if err := configureSASL(config, secretData); err != nil {
			return nil, err
		}
		if err := configureTLS(config, secretData, false); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported kafka protocol %q", protocol)
	}

	return config, nil
}

func configureSASL(config *sarama.Config, data map[string][]byte) error {
	user := string(data["user"])
	password := string(data["password"])
	if user == "" || password == "" {
		return fmt.Errorf("kafka SASL requires \"user\" and \"password\" keys")
	}

	mechanism := strings.TrimSpace(string(data["sasl.mechanism"]))
	if mechanism == "" {
		mechanism = SASLMechanismPlain
	}

	config.Net.SASL.Enable = true
	config.Net.SASL.User = user
	config.Net.SASL.Password = password

	switch mechanism {
	case SASLMechanismPlain:
		config.Net.SASL.Mechanism = sarama.SASLTypePlaintext
	case SASLMechanismSHA256:
		config.Net.SASL.Mechanism = sarama.SASLTypeSCRAMSHA256
		config.Net.SASL.SCRAMClientGeneratorFunc = func() sarama.SCRAMClient {
			return &scramClient{HashGeneratorFcn: scram.SHA256}
		}
	case SASLMechanismSHA512:
		config.Net.SASL.Mechanism = sarama.SASLTypeSCRAMSHA512
		config.Net.SASL.SCRAMClientGeneratorFunc = func() sarama.SCRAMClient {
			return &scramClient{HashGeneratorFcn: scram.SHA512}
		}
	default:
		return fmt.Errorf("unsupported SASL mechanism %q", mechanism)
	}

	return nil
}

func configureTLS(config *sarama.Config, data map[string][]byte, requireClientCert bool) error {
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}

	if caCert, ok := data["ca.crt"]; ok && len(caCert) > 0 {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caCert) {
			return fmt.Errorf("failed to parse ca.crt")
		}
		tlsConfig.RootCAs = pool
	}

	if requireClientCert {
		skip := strings.TrimSpace(string(data["user.skip"])) == "true"
		if !skip {
			certPEM, hasCert := data["user.crt"]
			keyPEM, hasKey := data["user.key"]
			if !hasCert || !hasKey {
				return fmt.Errorf("kafka SSL requires \"user.crt\" and \"user.key\" (or set \"user.skip\" to \"true\")")
			}
			cert, err := tls.X509KeyPair(certPEM, keyPEM)
			if err != nil {
				return fmt.Errorf("parsing client certificate: %w", err)
			}
			tlsConfig.Certificates = []tls.Certificate{cert}
		}
	}

	config.Net.TLS.Enable = true
	config.Net.TLS.Config = tlsConfig
	return nil
}

type scramClient struct {
	*scram.ClientConversation
	scram.HashGeneratorFcn
}

func (c *scramClient) Begin(userName, password, authzID string) error {
	client, err := c.NewClient(userName, password, authzID)
	if err != nil {
		return err
	}
	c.ClientConversation = client.NewConversation()
	return nil
}

func (c *scramClient) Step(challenge string) (string, error) {
	return c.ClientConversation.Step(challenge)
}

func (c *scramClient) Done() bool {
	return c.ClientConversation.Done()
}
