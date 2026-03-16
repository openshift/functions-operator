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

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/functions-dev/func-operator/test/utils"
	"k8s.io/apimachinery/pkg/util/rand"
)

// buildAuthURL embeds credentials into git URL for authenticated operations
func buildAuthURL(repoURL, username, password string) string {
	return strings.Replace(repoURL, "http://",
		fmt.Sprintf("http://%s:%s@", username, password), 1)
}

// InitializeRepoWithFunction creates a function project and pushes it to the Gitea repo
func InitializeRepoWithFunction(repoURL, username, password, language string) (repoDir string, err error) {
	repoDir = fmt.Sprintf("%s/func-test-%s", os.TempDir(), rand.String(10))

	// Build authenticated URL
	authURL := buildAuthURL(repoURL, username, password)

	// Initialize function (func init creates the directory)
	cmd := exec.Command("func", "init", "-l", language, repoDir)
	if _, err = utils.Run(cmd); err != nil {
		return "", fmt.Errorf("failed to init function: %w", err)
	}

	// Initialize git repo with main as default branch
	cmd = exec.Command("git", "-C", repoDir, "init", "-b", "main")
	if _, err = utils.Run(cmd); err != nil {
		return "", fmt.Errorf("failed to git init: %w", err)
	}

	// Configure git user
	cmd = exec.Command("git", "-C", repoDir, "config", "user.name", "Test User")
	if _, err = utils.Run(cmd); err != nil {
		return "", fmt.Errorf("failed to set git user.name: %w", err)
	}
	cmd = exec.Command("git", "-C", repoDir, "config", "user.email", "test@example.com")
	if _, err = utils.Run(cmd); err != nil {
		return "", fmt.Errorf("failed to set git user.email: %w", err)
	}

	// Add remote
	cmd = exec.Command("git", "-C", repoDir, "remote", "add", "origin", authURL)
	if _, err = utils.Run(cmd); err != nil {
		return "", fmt.Errorf("failed to add remote: %w", err)
	}

	// Commit and push
	cmd = exec.Command("git", "-C", repoDir, "add", ".")
	if _, err = utils.Run(cmd); err != nil {
		return "", fmt.Errorf("failed to git add: %w", err)
	}
	cmd = exec.Command("git", "-C", repoDir, "commit", "-m", "Initial function")
	if _, err = utils.Run(cmd); err != nil {
		return "", fmt.Errorf("failed to git commit: %w", err)
	}
	cmd = exec.Command("git", "-C", repoDir, "push", "-u", "origin", "main")
	if _, err = utils.Run(cmd); err != nil {
		return "", fmt.Errorf("failed to push initial commit: %w", err)
	}

	return repoDir, nil
}

// CommitAndPush commits and pushes specified files with a custom message
// Requires at least one file to be specified
func CommitAndPush(repoDir string, msg string, file string, otherFiles ...string) error {
	// Add first file
	cmd := exec.Command("git", "-C", repoDir, "add", file)
	if _, err := utils.Run(cmd); err != nil {
		return fmt.Errorf("failed to git add %s: %w", file, err)
	}

	// Add other files if provided
	for _, f := range otherFiles {
		cmd = exec.Command("git", "-C", repoDir, "add", f)
		if _, err := utils.Run(cmd); err != nil {
			return fmt.Errorf("failed to git add %s: %w", f, err)
		}
	}

	// Commit
	cmd = exec.Command("git", "-C", repoDir, "commit", "-m", msg)
	if _, err := utils.Run(cmd); err != nil {
		return fmt.Errorf("failed to git commit: %w", err)
	}

	// Push
	cmd = exec.Command("git", "-C", repoDir, "push")
	if _, err := utils.Run(cmd); err != nil {
		return fmt.Errorf("failed to push: %w", err)
	}

	return nil
}
