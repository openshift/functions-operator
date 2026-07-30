package kafka

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/IBM/sarama"
)

const (
	keyProtocol = "protocol"
	keyUser     = "user"
	keyPassword = "password"
	keyCACrt    = "ca.crt"
)

func TestNewConfig_NilData(t *testing.T) {
	cfg, err := NewConfig(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Net.TLS.Enable {
		t.Error("TLS should be disabled")
	}
	if cfg.Net.SASL.Enable {
		t.Error("SASL should be disabled")
	}
	if !cfg.Producer.Return.Successes {
		t.Error("Producer.Return.Successes should be true")
	}
}

func TestNewConfig_MissingProtocol(t *testing.T) {
	_, err := NewConfig(map[string][]byte{keyUser: []byte("x")})
	if err == nil {
		t.Fatal("expected error for missing protocol")
	}
}

func TestNewConfig_Plaintext(t *testing.T) {
	cfg, err := NewConfig(map[string][]byte{keyProtocol: []byte("PLAINTEXT")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Net.TLS.Enable || cfg.Net.SASL.Enable {
		t.Error("PLAINTEXT should have no TLS/SASL")
	}
}

func TestNewConfig_SASLPlaintext(t *testing.T) {
	tests := []struct {
		name      string
		mechanism string
		wantMech  sarama.SASLMechanism
	}{
		{"default PLAIN", "", sarama.SASLTypePlaintext},
		{"explicit PLAIN", SASLMechanismPlain, sarama.SASLTypePlaintext},
		{SASLMechanismSHA256, SASLMechanismSHA256, sarama.SASLTypeSCRAMSHA256},
		{SASLMechanismSHA512, SASLMechanismSHA512, sarama.SASLTypeSCRAMSHA512},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := map[string][]byte{
				keyProtocol: []byte("SASL_PLAINTEXT"),
				keyUser:     []byte("alice"),
				keyPassword: []byte("secret"),
			}
			if tt.mechanism != "" {
				data["sasl.mechanism"] = []byte(tt.mechanism)
			}

			cfg, err := NewConfig(data)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !cfg.Net.SASL.Enable {
				t.Error("SASL should be enabled")
			}
			if cfg.Net.SASL.User != "alice" {
				t.Errorf("user = %q, want alice", cfg.Net.SASL.User)
			}
			if cfg.Net.SASL.Mechanism != tt.wantMech {
				t.Errorf("mechanism = %v, want %v", cfg.Net.SASL.Mechanism, tt.wantMech)
			}
			if cfg.Net.TLS.Enable {
				t.Error("TLS should be disabled for SASL_PLAINTEXT")
			}
			if tt.wantMech == sarama.SASLTypeSCRAMSHA256 || tt.wantMech == sarama.SASLTypeSCRAMSHA512 {
				if cfg.Net.SASL.SCRAMClientGeneratorFunc == nil {
					t.Error("SCRAM generator should be set")
				}
			}
		})
	}
}

func TestNewConfig_SASLMissingCredentials(t *testing.T) {
	data := map[string][]byte{
		keyProtocol: []byte("SASL_PLAINTEXT"),
		keyUser:     []byte("alice"),
	}
	_, err := NewConfig(data)
	if err == nil {
		t.Fatal("expected error for missing password")
	}
}

func TestNewConfig_UnsupportedMechanism(t *testing.T) {
	data := map[string][]byte{
		keyProtocol:      []byte("SASL_PLAINTEXT"),
		keyUser:          []byte("alice"),
		keyPassword:      []byte("secret"),
		"sasl.mechanism": []byte("OAUTHBEARER"),
	}
	_, err := NewConfig(data)
	if err == nil {
		t.Fatal("expected error for OAUTHBEARER")
	}
}

func TestNewConfig_SSL(t *testing.T) {
	caCert, caKey := generateTestCA(t)
	clientCert, clientKey := generateTestClientCert(t, caCert, caKey)

	t.Run("with client cert", func(t *testing.T) {
		data := map[string][]byte{
			keyProtocol: []byte("SSL"),
			keyCACrt:    pemEncodeCert(caCert),
			"user.crt":  pemEncodeCert(clientCert),
			"user.key":  pemEncodeKey(t, clientKey),
		}
		cfg, err := NewConfig(data)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !cfg.Net.TLS.Enable {
			t.Error("TLS should be enabled")
		}
		if cfg.Net.TLS.Config.RootCAs == nil {
			t.Error("RootCAs should be set")
		}
		if len(cfg.Net.TLS.Config.Certificates) != 1 {
			t.Error("client certificate should be set")
		}
	})

	t.Run("with user.skip", func(t *testing.T) {
		data := map[string][]byte{
			keyProtocol: []byte("SSL"),
			keyCACrt:    pemEncodeCert(caCert),
			"user.skip": []byte("true"),
		}
		cfg, err := NewConfig(data)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !cfg.Net.TLS.Enable {
			t.Error("TLS should be enabled")
		}
		if len(cfg.Net.TLS.Config.Certificates) != 0 {
			t.Error("client certificate should not be set when user.skip=true")
		}
	})

	t.Run("missing client cert", func(t *testing.T) {
		data := map[string][]byte{
			keyProtocol: []byte("SSL"),
			keyCACrt:    pemEncodeCert(caCert),
		}
		_, err := NewConfig(data)
		if err == nil {
			t.Fatal("expected error for missing client cert")
		}
	})

	t.Run("no ca.crt uses system roots", func(t *testing.T) {
		data := map[string][]byte{
			keyProtocol: []byte("SSL"),
			"user.skip": []byte("true"),
		}
		cfg, err := NewConfig(data)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Net.TLS.Config.RootCAs != nil {
			t.Error("RootCAs should be nil (system roots)")
		}
	})
}

func TestNewConfig_SASL_SSL(t *testing.T) {
	caCert, _ := generateTestCA(t)

	data := map[string][]byte{
		keyProtocol:      []byte("SASL_SSL"),
		"sasl.mechanism": []byte("SCRAM-SHA-512"),
		keyUser:          []byte("alice"),
		keyPassword:      []byte("secret"),
		keyCACrt:         pemEncodeCert(caCert),
	}
	cfg, err := NewConfig(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.Net.SASL.Enable {
		t.Error("SASL should be enabled")
	}
	if !cfg.Net.TLS.Enable {
		t.Error("TLS should be enabled")
	}
	if len(cfg.Net.TLS.Config.Certificates) != 0 {
		t.Error("SASL_SSL should not require client certs")
	}
}

func TestNewConfig_UnsupportedProtocol(t *testing.T) {
	_, err := NewConfig(map[string][]byte{keyProtocol: []byte("BOGUS")})
	if err == nil {
		t.Fatal("expected error for unsupported protocol")
	}
}

func generateTestCA(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert, key
}

func generateTestClientCert(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "test-client"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert, key
}

func pemEncodeCert(cert *x509.Certificate) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
}

func pemEncodeKey(t *testing.T, key *ecdsa.PrivateKey) []byte {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}
