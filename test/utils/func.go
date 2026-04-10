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
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// RunFunc executes the func CLI with the current/latest version
func RunFunc(command string, args ...string) (string, error) {
	allArgs := append([]string{command}, args...)
	cmd := exec.Command("func", allArgs...)
	return Run(cmd)
}

// RunFuncWithVersion executes the func CLI with a specific version
// It downloads and caches the version if not already present
func RunFuncWithVersion(version string, command string, args ...string) (string, error) {
	funcBinary, err := ensureFuncVersion(version)
	if err != nil {
		return "", err
	}

	allArgs := append([]string{command}, args...)
	cmd := exec.Command(funcBinary, allArgs...)
	return Run(cmd)
}

// RunFuncDeploy runs func deploy
func RunFuncDeploy(functionDir string, optFns ...FuncDeployOption) (string, error) {
	opts := &FuncDeployOptions{
		// defaults
		Registry:         Registry(),
		RegistryInsecure: IsRegistryInsecure(),
		Builder:          os.Getenv("DEFAULT_BUILDER"),
		Deployer:         os.Getenv("DEFAULT_DEPLOYER"),
	}

	for _, optFn := range optFns {
		optFn(opts)
	}

	args := []string{
		"--path", functionDir,
		"--registry", opts.Registry,
		fmt.Sprintf("--registry-insecure=%t", opts.RegistryInsecure),
	}

	if opts.Namespace != "" {
		args = append(args, "--namespace", opts.Namespace)
	}

	if opts.Builder != "" {
		args = append(args, "--builder", opts.Builder)
	}

	if opts.Deployer != "" {
		args = append(args, "--deployer", opts.Deployer)
	}

	if opts.CliVersion != "" {
		return RunFuncWithVersion(opts.CliVersion, "deploy", args...)
	}

	return RunFunc("deploy", args...)
}

type FuncDeployOptions struct {
	Registry         string
	RegistryInsecure bool
	Namespace        string
	Builder          string
	Deployer         string
	CliVersion       string
}

type FuncDeployOption func(*FuncDeployOptions)

func WithNamespace(namespace string) FuncDeployOption {
	return func(opts *FuncDeployOptions) {
		opts.Namespace = namespace
	}
}

func WithBuilder(builder string) FuncDeployOption {
	return func(o *FuncDeployOptions) {
		o.Builder = builder
	}
}

func WithDeployer(deployer string) FuncDeployOption {
	return func(o *FuncDeployOptions) {
		o.Deployer = deployer
	}
}

func WithDeployCliVersion(version string) FuncDeployOption {
	return func(opts *FuncDeployOptions) {
		opts.CliVersion = version
	}
}

// ensureFuncVersion ensures the specified func version is available and returns its path
func ensureFuncVersion(version string) (string, error) {
	projectDir, err := GetProjectDir()
	if err != nil {
		return "", fmt.Errorf("failed to get project directory: %w", err)
	}

	versionDir := filepath.Join(projectDir, "bin", "func-cli", version)
	funcBinary := filepath.Join(versionDir, "func")

	// Check if already cached
	if _, err := os.Stat(funcBinary); err == nil {
		return funcBinary, nil
	}

	// Download the version
	if err := downloadFuncVersion(version, versionDir, funcBinary); err != nil {
		return "", err
	}

	return funcBinary, nil
}

// downloadFuncVersion downloads the specified func version from GitHub releases
func downloadFuncVersion(version, versionDir, funcBinary string) error {
	// Create version directory
	if err := os.MkdirAll(versionDir, 0755); err != nil {
		return fmt.Errorf("failed to create version directory: %w", err)
	}

	// Construct download URL
	goos := runtime.GOOS
	goarch := runtime.GOARCH
	url := fmt.Sprintf("https://github.com/knative/func/releases/download/knative-%s/func_%s_%s",
		version, goos, goarch)

	// Download binary
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("failed to download func %s: %w", version, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download func %s: HTTP %d", version, resp.StatusCode)
	}

	// Write to file
	out, err := os.Create(funcBinary)
	if err != nil {
		return fmt.Errorf("failed to create binary file: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("failed to write binary: %w", err)
	}

	// Make executable
	if err := os.Chmod(funcBinary, 0755); err != nil {
		return fmt.Errorf("failed to make binary executable: %w", err)
	}

	return nil
}
