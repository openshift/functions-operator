package git

import (
	"context"
	"fmt"
	neturl "net/url"
	"os"
	"strings"

	"github.com/functions-dev/func-operator/internal/monitoring"
	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/transport"
	"github.com/go-git/go-git/v6/plumbing/transport/http"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	cloneBaseDir = "/git-repos"
)

type Manager interface {
	CloneRepository(ctx context.Context, url, subPath, reference string, auth map[string][]byte) (*Repository, error)
}

func NewManager() Manager {
	return &managerImpl{}
}

type managerImpl struct{}

func (m *managerImpl) CloneRepository(ctx context.Context, repoUrl, subPath, reference string, auth map[string][]byte) (*Repository, error) {
	timer := prometheus.NewTimer(monitoring.GitCloneDuration)
	defer timer.ObserveDuration()

	url, err := neturl.Parse(repoUrl)
	if err != nil {
		return nil, fmt.Errorf("failed to parse repository URL: %w", err)
	}

	pattern := fmt.Sprintf("%s-%s-%s", url.Host, strings.ReplaceAll(strings.TrimSuffix(url.Path, ".git"), "/", "-"), reference)
	targetDir, err := os.MkdirTemp(cloneBaseDir, pattern)
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary directory: %w", err)
	}

	repo, err := git.PlainCloneContext(ctx, targetDir, &git.CloneOptions{
		URL:           repoUrl,
		ReferenceName: plumbing.ReferenceName(reference),
		SingleBranch:  true,
		Depth:         1,
		Auth:          m.getAuth(auth),
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

func (m *managerImpl) getAuth(authSecret map[string][]byte) transport.AuthMethod {
	if len(authSecret) == 0 {
		return nil
	} else if token, ok := authSecret["token"]; ok {
		return &http.BasicAuth{
			Username: "empty", // can be anything except an empty string
			Password: string(token),
		}
	} else if username, ok := authSecret["username"]; ok {
		if password, ok := authSecret["password"]; ok {
			return &http.BasicAuth{
				Username: string(username),
				Password: string(password),
			}
		}
		return nil
	} // add other auth methods when needed
	return nil
}
