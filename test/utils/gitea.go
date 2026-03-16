/*
Copyright 2025.

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

package utils

import (
	"context"
	"fmt"

	"code.gitea.io/sdk/gitea"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	giteaAdminUser = "giteaadmin"
	giteaAdminPass = "giteapass"
)

// RepositoryProvider defines the interface for interacting with Git repository hosting providers
type RepositoryProvider interface {
	// User management
	CreateUser(username, password, email string) (cleanup func(), err error)
	DeleteUser(username string) error
	CreateRandomUser() (username, password, email string, cleanup func(), err error)

	// Repository management
	CreateRepo(owner, name string, private bool) (url string, cleanup func(), err error)
	DeleteRepo(owner, name string) error
	CreateRandomRepo(owner string, private bool) (name, url string, cleanup func(), err error)

	// Authentication
	CreateAccessToken(username, password, tokenName string) (string, error)
}

// GiteaClient wraps the Gitea SDK client and provides helper methods
type GiteaClient struct {
	client    *gitea.Client
	baseURL   string
	adminUser string
	adminPass string
}

// NewGiteaClient discovers Gitea endpoint from ConfigMap and creates client
func NewGiteaClient() (*GiteaClient, error) {
	// Load kubeconfig
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	configOverrides := &clientcmd.ConfigOverrides{}
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)

	cfg, err := kubeConfig.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	// Create Kubernetes client
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	// Get gitea-endpoint ConfigMap
	cm, err := clientset.CoreV1().
		ConfigMaps("kube-public").
		Get(context.Background(), "gitea-endpoint", metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get gitea-endpoint configmap: %w", err)
	}

	baseURL, ok := cm.Data["http"]
	if !ok {
		return nil, fmt.Errorf("gitea-endpoint configmap missing 'http' key")
	}

	// Create Gitea SDK client
	giteaClient, err := gitea.NewClient(baseURL, gitea.SetBasicAuth(giteaAdminUser, giteaAdminPass))
	if err != nil {
		return nil, fmt.Errorf("failed to create gitea client: %w", err)
	}

	return &GiteaClient{
		client:    giteaClient,
		baseURL:   baseURL,
		adminUser: giteaAdminUser,
		adminPass: giteaAdminPass,
	}, nil
}

// CreateUser creates a new Gitea user
func (g *GiteaClient) CreateUser(username, password, email string) (cleanup func(), err error) {
	mustChangePassword := false
	_, _, err = g.client.AdminCreateUser(gitea.CreateUserOption{
		Username:           username,
		Password:           password,
		Email:              email,
		MustChangePassword: &mustChangePassword,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create user %s: %w", username, err)
	}

	cleanup = func() {
		_ = g.DeleteUser(username)
	}
	return cleanup, nil
}

// DeleteUser deletes a Gitea user
func (g *GiteaClient) DeleteUser(username string) error {
	_, err := g.client.AdminDeleteUser(username)
	if err != nil {
		return fmt.Errorf("failed to delete user %s: %w", username, err)
	}
	return nil
}

// CreateRandomUser creates a user with random credentials
func (g *GiteaClient) CreateRandomUser() (username, password, email string, cleanup func(), err error) {
	username = "user-" + rand.String(8)
	password = "pass-" + rand.String(8)
	email = username + "@test.local"

	cleanup, err = g.CreateUser(username, password, email)
	return username, password, email, cleanup, err
}

// CreateRepo creates a new repository and returns its URL
func (g *GiteaClient) CreateRepo(owner, name string, private bool) (url string, cleanup func(), err error) {
	// Use admin client to create repo for the specified owner
	_, _, err = g.client.AdminCreateRepo(owner, gitea.CreateRepoOption{
		Name:    name,
		Private: private,
	})
	if err != nil {
		return "", nil, fmt.Errorf("failed to create repo %s/%s: %w", owner, name, err)
	}

	// Build repository URL
	url = fmt.Sprintf("%s/%s/%s.git", g.baseURL, owner, name)

	cleanup = func() {
		_ = g.DeleteRepo(owner, name)
	}
	return url, cleanup, nil
}

// DeleteRepo deletes a repository
func (g *GiteaClient) DeleteRepo(owner, name string) error {
	_, err := g.client.DeleteRepo(owner, name)
	if err != nil {
		return fmt.Errorf("failed to delete repo %s/%s: %w", owner, name, err)
	}
	return nil
}

// CreateRandomRepo creates a repo with a random name
func (g *GiteaClient) CreateRandomRepo(owner string, private bool) (name, url string, cleanup func(), err error) {
	name = "repo-" + rand.String(8)
	url, cleanup, err = g.CreateRepo(owner, name, private)
	return name, url, cleanup, err
}

// CreateAccessToken creates a personal access token for a user
func (g *GiteaClient) CreateAccessToken(username, password, tokenName string) (string, error) {
	// Create a client authenticated as the user
	userClient, err := gitea.NewClient(g.baseURL, gitea.SetBasicAuth(username, password))
	if err != nil {
		return "", fmt.Errorf("failed to create user client: %w", err)
	}

	// Create token
	token, _, err := userClient.CreateAccessToken(gitea.CreateAccessTokenOption{
		Name: tokenName,
	})
	if err != nil {
		return "", fmt.Errorf("failed to create access token for %s: %w", username, err)
	}

	return token.Token, nil
}
