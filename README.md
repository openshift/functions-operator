# func-operator

[![GitHub release](https://img.shields.io/github/v/release/functions-dev/func-operator?style=flat-square)](https://github.com/functions-dev/func-operator/releases)
[![Go Report Card](https://goreportcard.com/badge/github.com/functions-dev/func-operator?style=flat-square)](https://goreportcard.com/report/github.com/functions-dev/func-operator)
[![Go Version](https://img.shields.io/github/go-mod/go-version/functions-dev/func-operator?style=flat-square)](https://go.dev/)
[![License](https://img.shields.io/github/license/functions-dev/func-operator?style=flat-square)](LICENSE)
[![CI](https://img.shields.io/github/actions/workflow/status/functions-dev/func-operator/test.yml?branch=main&label=CI&style=flat-square)](https://github.com/functions-dev/func-operator/actions/workflows/test.yml)

A Kubernetes operator for managing middleware updates for serverless functions deployed with the `func` CLI. This operator monitors deployed functions and automatically rebuilds them when outdated middleware is detected, ensuring functions stay up-to-date with the latest middleware versions.

## Prerequisites

- Kubernetes cluster (1.31+)
- [Knative Serving](https://knative.dev/docs/install/) installed
- [Tekton Pipelines](https://tekton.dev/docs/installation/) installed
- Container registry for storing function images

## Installation

### Install the Operator

Deploy the operator to your cluster:

```bash
kubectl apply -f https://github.com/functions-dev/func-operator/releases/latest/download/func-operator.yaml
```

## Usage

### Register a Function for Middleware Management

Create a `Function` custom resource to register an existing function for middleware monitoring and updates:

```yaml
apiVersion: functions.dev/v1alpha1
kind: Function
metadata:
  name: my-function
  namespace: default
spec:
  repository:
    url: https://github.com/your-org/your-function.git
    authSecretRef:
      name: git-credentials
  registry:
    authSecretRef:
      name: registry-credentials
```

Apply the resource:

```bash
kubectl apply -f function.yaml
```

**Note:** This registers an existing function with the operator for middleware management. To initially deploy a function, use the `func` CLI directly:

```bash
func deploy --path <function-path> --registry <registry-path>
```

### Registry Authentication

For private registries, create a secret with registry credentials:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: registry-credentials
  namespace: default
type: kubernetes.io/dockerconfigjson
data:
  .dockerconfigjson: <base64-encoded-docker-config>
```

Or use kubectl:

```bash
kubectl create secret docker-registry registry-credentials \
  --docker-server=<registry-url> \
  --docker-username=<username> \
  --docker-password=<password> \
  --docker-email=<email>
```

### Git Authentication

For private Git repositories, create a secret with the Git credentials:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: git-credentials
  namespace: default
data:
  token: <base64-encoded-access-token>
```

or 

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: git-credentials
  namespace: default
data:
  username: <base64-encoded-username>
  password: <base64-encoded-password>
```

Then reference it in the Function under `.spec.repository.authSecretRef.name`

```yaml
apiVersion: functions.dev/v1alpha1
kind: Function
metadata:
  name: my-function
  namespace: default
spec:
  repository:
    url: https://github.com/your-org/your-function.git
    authSecretRef:
      name: git-credentials
```

### Check Function Status

View the middleware status of your function:

```bash
kubectl get function my-function -o yaml
```

The status will include:
- Function name and conditions
- Git information (branch, commit, last checked time)
- Deployment details (image, runtime, build time, deployer)
- Middleware status (current and available versions, auto-update settings, pending rebuild status)

## Advanced Use Cases

### Functions in Monorepos

For functions located in a subdirectory of a repository (e.g., in a monorepo), use the `repository.path` field to specify the path to your function:

```yaml
apiVersion: functions.dev/v1alpha1
kind: Function
metadata:
  name: my-function
  namespace: default
spec:
  repository:
    url: https://github.com/your-org/your-monorepo.git
    path: functions/my-function
    authSecretRef:
      name: git-credentials
  registry:
    authSecretRef:
      name: registry-credentials
```

The operator will clone the repository and use the specified path as the function root directory.

### Configuring Automatic Middleware Updates

The operators main responsibility it to rebuild functions when outdated middleware is detected. Anyhow this behavior can be enabled/disabled at two levels:

#### Operator-Level Default

Configure the operator-wide default by editing the `func-operator-controller-config` ConfigMap in the operators namespace (`func-operator-system` by default):

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: func-operator-controller-config
  namespace: func-operator-system
data:
  autoUpdateMiddleware: "true"  # or "false" to disable by default
```

#### Per-Function Override

Individual functions can override the operator default using the `autoUpdateMiddleware` field:

```yaml
apiVersion: functions.dev/v1alpha1
kind: Function
metadata:
  name: my-function
  namespace: default
spec:
  repository:
    url: https://github.com/your-org/your-function.git
  autoUpdateMiddleware: false  # Disable middleware updates for this function
```

**Precedence:** Function-level settings always take priority over the operator default.

## Development

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

### Run Tests

```bash
# Unit tests
make test

# E2E tests (requires Kind cluster with Gitea)
make create-kind-cluster  # Sets up cluster with Gitea
make test-e2e

# Bundle tests
make test-e2e-bundle
```

E2E tests use an in-cluster Gitea instance instead of GitHub, providing complete test isolation. See [Gitea Integration](docs/development/gitea-integration.md) for details on the test infrastructure.

### Linting

```bash
# Run linter
make lint
```

## API Reference

### Function Spec

| Field                       | Type    | Required | Description                                                                                      |
|-----------------------------|---------|----------|--------------------------------------------------------------------------------------------------|
| `repository.url`            | string  | Yes      | URL of the Git repository containing the function                                                |
| `repository.branch`         | string  | No       | Branch of the repository                                                                         |
| `repository.path`           | string  | No       | Path to the function inside the repository. Defaults to "."                                      |
| `repository.authSecretRef`  | object  | No       | Reference to the auth secret for private repository authentication                               |
| `registry.authSecretRef`    | object  | No       | Reference to the secret containing credentials for registry authentication                       |
| `autoUpdateMiddleware`      | boolean | No       | Defines if the operator should rebuild when outdated middleware is detected. When not specified, defaults to the operator-wide setting in the `func-operator-controller-config` ConfigMap (default: `true`). Function-level setting takes precedence over operator default |

### Function Status

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Function name from metadata |
| `conditions` | array | Status conditions (see below) |
| `git.resolvedBranch` | string | Git branch that is being monitored |
| `git.observedCommit` | string | Latest Git commit SHA observed |
| `git.lastChecked` | timestamp | Last time the repository was checked |
| `deployment.image` | string | Container image of the deployed function |
| `deployment.imageBuilt` | timestamp | When the current image was built |
| `deployment.deployer` | string | Tool/method used to deploy the function (e.g., "func") |
| `deployment.runtime` | string | Detected function runtime |
| `middleware.current` | string | Current middleware version in use |
| `middleware.available` | string | Latest available middleware version |
| `middleware.autoUpdate.enabled` | boolean | Whether automatic middleware updates are enabled |
| `middleware.autoUpdate.source` | string | Source of the autoUpdate setting ("function" or "operator") |
| `middleware.pendingRebuild` | boolean | Whether a rebuild is pending due to outdated middleware |
| `middleware.lastRebuild` | timestamp | Last time the function was rebuilt for middleware updates |

#### Status Conditions

| Condition | Description                                                           |
|-----------|-----------------------------------------------------------------------|
| `Ready` | Summary condition that is `True` when all other conditions are `True` |
| `SourceReady` | Git source was cloned and function metadata was read successfully     |
| `Deployed` | Function is deployed                                                  |
| `MiddlewareUpToDate` | Middleware is on the latest version (or intentionally skipped)        |
| `ServiceReady` | Underlying service of the function is ready to serve traffic          |

## Uninstallation

Remove the operator and CRDs:

```bash
# Undeploy operator
make undeploy

# Uninstall CRDs
make uninstall
```