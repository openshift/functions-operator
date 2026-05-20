package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/functions-dev/func-operator/internal/monitoring"
	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/client"
	"github.com/go-git/go-git/v6/plumbing/transport"
	"github.com/go-git/go-git/v6/plumbing/transport/http"
	gitssh "github.com/go-git/go-git/v6/plumbing/transport/ssh"
	"github.com/prometheus/client_golang/prometheus"
	gossh "golang.org/x/crypto/ssh"
)

const (
	cloneBaseDir = "/git-repos"
)

type Manager interface {
	CloneRepository(ctx context.Context, url, subPath, reference string, auth map[string][]byte) (*Repository, error)
}

func NewManager() (Manager, error) {
	if err := os.MkdirAll(cloneBaseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create git clone base directory: %w", err)
	}
	// go-git's SSH transport requires a known_hosts file for host key algorithm
	// discovery, even when HostKeyCallback is already set. Without this file,
	// SSH connections fail in containers that lack ~/.ssh/known_hosts.
	if err := ensureKnownHostsExists(); err != nil {
		return nil, fmt.Errorf("failed to ensure known_hosts exists: %w", err)
	}
	return &managerImpl{}, nil
}

type managerImpl struct{}

func (m *managerImpl) CloneRepository(ctx context.Context, repoUrl, subPath, reference string, auth map[string][]byte) (*Repository, error) {
	timer := prometheus.NewTimer(monitoring.GitCloneDuration)
	defer timer.ObserveDuration()

	parsedURL, err := transport.ParseURL(repoUrl)
	if err != nil {
		return nil, fmt.Errorf("failed to parse repository URL: %w", err)
	}

	targetDir, err := os.MkdirTemp(cloneBaseDir, "repo-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary directory: %w", err)
	}

	clientOpts, err := m.getClientOptions(parsedURL.Scheme, auth)
	if err != nil {
		return nil, fmt.Errorf("failed to configure auth: %w", err)
	}

	repo, err := git.PlainCloneContext(ctx, targetDir, &git.CloneOptions{
		URL:           repoUrl,
		ReferenceName: plumbing.ReferenceName(reference),
		SingleBranch:  true,
		Depth:         1,
		ClientOptions: clientOpts,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to clone repo: %w", err)
	}

	head, err := repo.Head()
	if err != nil {
		return nil, fmt.Errorf("failed to find head: %w", err)
	}

	return &Repository{
		CloneDir: targetDir,
		SubPath:  subPath,
		Commit:   head.Hash().String(),
		Branch:   reference,
	}, nil
}

func (m *managerImpl) getClientOptions(scheme string, authSecret map[string][]byte) ([]client.Option, error) {
	if scheme == "ssh" {
		return m.getSSHClientOptions(authSecret)
	}
	return m.getHTTPClientOptions(authSecret), nil
}

func (m *managerImpl) getHTTPClientOptions(authSecret map[string][]byte) []client.Option {
	if len(authSecret) == 0 {
		return nil
	} else if token, ok := authSecret["token"]; ok {
		return []client.Option{
			client.WithHTTPAuth(&http.BasicAuth{
				Username: "empty", // can be anything except an empty string
				Password: string(token),
			}),
		}
	} else if username, ok := authSecret["username"]; ok {
		if password, ok := authSecret["password"]; ok {
			return []client.Option{
				client.WithHTTPAuth(&http.BasicAuth{
					Username: string(username),
					Password: string(password),
				}),
			}
		}
		return nil
	}
	return nil
}

func ensureKnownHostsExists() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		return err
	}
	knownHostsPath := filepath.Join(sshDir, "known_hosts")
	if _, err := os.Stat(knownHostsPath); os.IsNotExist(err) {
		return os.WriteFile(knownHostsPath, nil, 0644)
	}
	return nil
}

func (m *managerImpl) getSSHClientOptions(authSecret map[string][]byte) ([]client.Option, error) {
	privateKey, hasKey := authSecret["sshPrivateKey"]
	if !hasKey {
		return []client.Option{
			client.WithSSHAuth(&gitssh.Password{
				User:                  "git",
				HostKeyCallbackHelper: gitssh.HostKeyCallbackHelper{HostKeyCallback: gossh.InsecureIgnoreHostKey()},
			}),
		}, nil
	}

	password := string(authSecret["sshPrivateKeyPassword"])
	auth, err := gitssh.NewPublicKeys("git", privateKey, password)
	if err != nil {
		return nil, fmt.Errorf("failed to parse SSH private key: %w", err)
	}
	auth.HostKeyCallback = gossh.InsecureIgnoreHostKey()

	if knownHostsData, ok := authSecret["known_hosts"]; ok {
		tmpFile, err := os.CreateTemp("", "known_hosts-*")
		if err == nil {
			defer os.Remove(tmpFile.Name())
			if _, err := tmpFile.Write(knownHostsData); err == nil {
				_ = tmpFile.Close()
				cb, err := gitssh.NewKnownHostsCallback(tmpFile.Name())
				if err == nil {
					auth.HostKeyCallback = cb
				}
			}
		}
	}

	return []client.Option{client.WithSSHAuth(auth)}, nil
}
