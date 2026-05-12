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
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/functions-dev/func-operator/internal/funccli"
	ginkgo "github.com/onsi/ginkgo/v2"
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

// RunFuncDeploy runs func deploy with retry logic for transient network errors
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

	if opts.Deployer != "" && opts.Deployer != "knative" {
		args = append(args, "--deployer", opts.Deployer)
	}

	// When using the pack builder, create a per-deploy PACK_HOME to prevent
	// parallel builds from corrupting the shared ~/.pack/volume-keys.toml.
	if opts.Builder == "pack" {
		packHome, err := os.MkdirTemp("", "pack-home-*")
		if err != nil {
			return "", fmt.Errorf("failed to create PACK_HOME: %w", err)
		}
		defer os.RemoveAll(packHome)

		if opts.EnvVars == nil {
			opts.EnvVars = make(map[string]string)
		}
		opts.EnvVars["PACK_HOME"] = packHome
	}

	var output string
	var err error

	maxAttempts := 5
	retryDelay := 10 * time.Second

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			_, _ = fmt.Fprintf(ginkgo.GinkgoWriter,
				"func deploy attempt %d/%d failed: %v (retrying in %s)\n",
				attempt, maxAttempts, err, retryDelay)
			time.Sleep(retryDelay)
			retryDelay *= 2
		}

		var funcBinary string
		if opts.CliVersion != "" {
			funcBinary, err = ensureFuncVersion(opts.CliVersion)
			if err != nil {
				return "", err
			}
		} else {
			funcBinary = "func"
		}

		cmd := exec.Command(funcBinary, append([]string{"deploy"}, args...)...)
		for k, v := range opts.EnvVars {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
		}
		output, err = Run(cmd)

		if err == nil {
			return output, nil
		}
	}

	return output, err
}

type FuncDeployOptions struct {
	Registry         string
	RegistryInsecure bool
	Namespace        string
	Builder          string
	Deployer         string
	CliVersion       string
	EnvVars          map[string]string
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

func WithEnvVars(envVars map[string]string) FuncDeployOption {
	return func(opts *FuncDeployOptions) {
		opts.EnvVars = envVars
	}
}

// ensureFuncVersion ensures the specified func version is available and returns its path.
// Uses file locking to prevent parallel Ginkgo processes from racing on the download.
func ensureFuncVersion(version string) (string, error) {
	projectDir, err := GetProjectDir()
	if err != nil {
		return "", fmt.Errorf("failed to get project directory: %w", err)
	}

	versionDir := filepath.Join(projectDir, "bin", "func-cli", version)
	funcBinary := filepath.Join(versionDir, "func")

	// Fast path: binary already exists, no lock needed
	if _, err := os.Stat(funcBinary); err == nil {
		return funcBinary, nil
	}

	// Ensure the directory exists before creating the lock file
	if err := os.MkdirAll(versionDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create version directory: %w", err)
	}

	// Acquire an exclusive file lock so only one Ginkgo process downloads at a time.
	// Ginkgo's -p flag runs specs in separate OS processes, so sync.Mutex doesn't work.
	lockFile, err := os.OpenFile(filepath.Join(versionDir, ".lock"), os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return "", fmt.Errorf("failed to create lock file: %w", err)
	}
	defer lockFile.Close()

	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return "", fmt.Errorf("failed to acquire file lock: %w", err)
	}
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN) //nolint:errcheck

	// Re-check after acquiring the lock — another process may have finished the download
	if _, err := os.Stat(funcBinary); err == nil {
		return funcBinary, nil
	}

	// Download the version
	if err := downloadFuncVersion(version, funcBinary); err != nil {
		return "", err
	}

	return funcBinary, nil
}

// downloadFuncVersion downloads the specified func version from GitHub releases.
// It writes to a temporary file first and atomically renames it to avoid exposing
// a partially-written binary to other processes.
func downloadFuncVersion(version, funcBinary string) error {
	asset := funccli.AssetName()
	base := "https://github.com/knative/func/releases/download/knative-" + version
	binaryURL := base + "/" + asset
	checksumURL := base + "/checksums.txt"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if err := funccli.DownloadAndInstall(ctx, binaryURL, checksumURL, funcBinary, nil); err != nil {
		return fmt.Errorf("failed to download func %s: %w", version, err)
	}

	return nil
}
