package git

import (
	"testing"

	"github.com/go-git/go-git/v6/plumbing/transport"
)

const sshScheme = "ssh"

const testEd25519PrivateKey = `-----BEGIN OPENSSH PRIVATE KEY-----
b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAAAMwAAAAtzc2gtZW
QyNTUxOQAAACDAtq/Kt1/J1J/YivGDJIO57fFW1v68f1eq1N1Vr77BLAAAALB+/pd5fv6X
eQAAAAtzc2gtZWQyNTUxOQAAACDAtq/Kt1/J1J/YivGDJIO57fFW1v68f1eq1N1Vr77BLA
AAAEDDodLIs7cKTLW+FFH5jgfGo2b2iae1w5lbsIXiu8UZKcC2r8q3X8nUn9iK8YMkg7nt
8VbW/rx/V6rU3VWvvsEsAAAAKmNzdGFibGVyQGNzdGFibGVyLXRoaW5rcGFkcDFnZW43Ln
JtdGRlLmNzYgECAw==
-----END OPENSSH PRIVATE KEY-----`

func TestGetClientOptions_HTTPToken(t *testing.T) {
	m := &managerImpl{}
	secret := map[string][]byte{"token": []byte("my-token")}
	opts := m.getClientOptions("https", secret)
	if len(opts) != 1 {
		t.Fatalf("expected 1 option, got %d", len(opts))
	}
}

func TestGetClientOptions_HTTPUsernamePassword(t *testing.T) {
	m := &managerImpl{}
	secret := map[string][]byte{"username": []byte("user"), "password": []byte("pass")}
	opts := m.getClientOptions("http", secret)
	if len(opts) != 1 {
		t.Fatalf("expected 1 option, got %d", len(opts))
	}
}

func TestGetClientOptions_HTTPEmpty(t *testing.T) {
	m := &managerImpl{}
	opts := m.getClientOptions("https", nil)
	if opts != nil {
		t.Fatalf("expected nil options, got %v", opts)
	}
}

func TestGetClientOptions_SSHNoSecret(t *testing.T) {
	m := &managerImpl{}
	opts := m.getClientOptions(sshScheme, nil)
	if len(opts) != 1 {
		t.Fatalf("expected 1 option for insecure SSH, got %d", len(opts))
	}
}

func TestGetClientOptions_SSHEmptySecret(t *testing.T) {
	m := &managerImpl{}
	opts := m.getClientOptions(sshScheme, map[string][]byte{})
	if len(opts) != 1 {
		t.Fatalf("expected 1 option for insecure SSH, got %d", len(opts))
	}
}

func TestGetClientOptions_SSHWithPrivateKey(t *testing.T) {
	m := &managerImpl{}
	secret := map[string][]byte{"sshPrivateKey": []byte(testEd25519PrivateKey)}
	opts := m.getClientOptions(sshScheme, secret)
	if len(opts) != 1 {
		t.Fatalf("expected 1 option, got %d", len(opts))
	}
}

func TestGetClientOptions_SSHWithInvalidKey(t *testing.T) {
	m := &managerImpl{}
	secret := map[string][]byte{"sshPrivateKey": []byte("not-a-valid-key")}
	opts := m.getClientOptions(sshScheme, secret)
	if opts != nil {
		t.Fatalf("expected nil options for invalid key, got %v", opts)
	}
}

func TestParseURL_SCPStyle(t *testing.T) {
	u, err := transport.ParseURL("git@github.com:owner/repo.git")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u.Scheme != sshScheme {
		t.Fatalf("expected scheme ssh, got %s", u.Scheme)
	}
}

func TestParseURL_SSHScheme(t *testing.T) {
	u, err := transport.ParseURL("ssh://git@github.com/owner/repo.git")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u.Scheme != sshScheme {
		t.Fatalf("expected scheme ssh, got %s", u.Scheme)
	}
}

func TestParseURL_HTTPSScheme(t *testing.T) {
	u, err := transport.ParseURL("https://github.com/owner/repo.git")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u.Scheme != "https" {
		t.Fatalf("expected scheme https, got %s", u.Scheme)
	}
}

func TestParseURL_HTTPScheme(t *testing.T) {
	u, err := transport.ParseURL("http://github.com/owner/repo.git")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u.Scheme != "http" {
		t.Fatalf("expected scheme http, got %s", u.Scheme)
	}
}
