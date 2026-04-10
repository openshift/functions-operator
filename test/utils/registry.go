package utils

import (
	"os"
	"strings"
	"sync"
)

var (
	registry         string
	registryInsecure bool
)

var registryOnce sync.Once

// Registry returns the registry used for the e2e tests
func Registry() string {
	registryOnce.Do(func() {
		// Setup vars from env
		registry = os.Getenv("REGISTRY")
		if registry == "" {
			registry = "kind-registry:5000"
		}
	})

	return registry
}

var registryInsecureOnce sync.Once

// IsRegistryInsecure returns if the registry for the e2e tests is insecure (no TLS verify)
func IsRegistryInsecure() bool {
	registryInsecureOnce.Do(func() {
		registryInsecure = false
		if sec := os.Getenv("REGISTRY_INSECURE"); strings.ToLower(sec) == "true" {
			registryInsecure = true
		}
	})

	return registryInsecure
}
