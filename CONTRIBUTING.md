# Contributing to func-operator

Thank you for your interest in contributing to func-operator! This guide will help you get started.

## How to Contribute

### Reporting Issues

If you find a bug or have a feature request, please [open an issue](https://github.com/functions-dev/func-operator/issues/new) on GitHub.

### Submitting Pull Requests

1. Fork the repository and create your branch from `main`.
2. Make your changes, ensuring they follow the guidelines below.
3. Run tests and linting locally before pushing.
4. Open a pull request against `main`.

All CI checks must pass before a pull request can be merged.

### Backporting Changes

To backport a merged PR to a release branch, comment `/cherry-pick release-X.Y` on the PR. This automatically creates a new PR with the cherry-picked changes against the target branch.

## Further Reading

- [Architecture Overview](docs/architecture.md) — system components, reconciliation flow, CRD lifecycle
- [Release Process](docs/release.md) — branching model, versioning, automated tag management
- [Gitea Integration](docs/development/gitea-integration.md) — e2e test infrastructure details

## Development

### Prerequisites

- Go (see `go.mod` for the required version)
- Docker
- [Kind](https://kind.sigs.k8s.io/) (for local development)
- kubectl

### Local Development Cluster

For local development, you can use the provided script to set up a Kind cluster with all prerequisites:

```bash
./hack/create-kind-cluster.sh
```

This script will:
- Create a local Kind cluster with multiple worker nodes
- Set up a local container registry on `localhost:5001`
- Install Tekton Pipelines
- Install Knative Serving with Kourier
- Configure the cluster to use the local registry

**Note:** The local registry uses HTTPS with a self-signed certificate. When running E2E tests locally, you need to set `REGISTRY_INSECURE=true` so the operator accepts the self-signed certificate (see [E2E Tests](#e2e-tests)).

### Build and Install the Operator

```bash
make docker-build IMG=<your-registry>/func-operator:latest
make deploy IMG=<your-registry>/func-operator:latest
```

### Debugging

For debugging the operator with [Delve](https://github.com/go-delve/delve), use the debug targets:

```bash
# Build the debug image (includes Delve debugger and debug symbols)
make docker-build-debugger IMAGE_TAG_BASE=<your-registry>/func-operator

# Push the debug image
make docker-push-debugger IMAGE_TAG_BASE=<your-registry>/func-operator

# Deploy the operator in debug mode
make deploy-debugger IMAGE_TAG_BASE=<your-registry>/func-operator
```

The debug deployment runs the operator under Delve in headless mode, listening on port 40000. To connect your debugger:

```bash
# Port-forward to access the debugger
kubectl port-forward -n func-operator-system deployment/func-operator-controller-manager 40000:40000

# Connect with Delve CLI
dlv connect localhost:40000
```

You can also connect using your IDE's remote debugging features (VS Code, GoLand, etc.) by configuring it to connect to `localhost:40000`.

## Code Generation

After modifying API types or interfaces, regenerate the derived code:

```bash
make update-codegen
```

This runs three sub-targets:
- `generate` — DeepCopy and DeepCopyInto methods
- `manifests` — CRDs, ClusterRoles, and webhook configurations
- `gen-mocks` — Mock implementations via [mockery](https://github.com/vektra/mockery)

## Testing

### Unit Tests

```bash
make test
```

### E2E Tests

E2E tests require a Kind cluster with Gitea (an in-cluster Git server providing complete test isolation):

```bash
make create-kind-cluster  # Sets up cluster with Gitea
REGISTRY_INSECURE=true make test-e2e
```

The `REGISTRY_INSECURE=true` flag is required because the local Kind registry uses a self-signed TLS certificate.

See [Gitea Integration](docs/development/gitea-integration.md) for details on the test infrastructure.

### Bundle Tests

```bash
REGISTRY_INSECURE=true make test-e2e-bundle
```

## Linting

```bash
# Run linter
make lint

# Run linter with auto-fix
make lint-fix
```