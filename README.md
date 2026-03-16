# func-operator

A Kubernetes operator for managing serverless functions using the `func` CLI. This operator automates the deployment and lifecycle management of functions from Git repositories to Kubernetes clusters with Knative.

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

### Create a Function

Create a `Function` custom resource to deploy a function from a Git repository:

```yaml
apiVersion: functions.dev/v1alpha1
kind: Function
metadata:
  name: my-function
  namespace: default
spec:
  source:
    repositoryUrl: https://github.com/your-org/your-function.git
    authSecretRef:
      name: git-credentials
  registry:
    path: quay.io/your-username/my-function
    authSecretRef:
      name: registry-credentials
```

Apply the resource:

```bash
kubectl apply -f function.yaml
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

Then reference it in the Function under `.spec.source.authSecretRef.name`

```yaml
apiVersion: functions.dev/v1alpha1
kind: Function
metadata:
  name: my-function
  namespace: default
spec:
  source:
    repositoryUrl: https://github.com/your-org/your-function.git
    authSecretRef:
      name: git-credentials
```

### Check Function Status

View the status of your function:

```bash
kubectl get function my-function -o yaml
```

The status will include:
- Function name and runtime
- Deployment conditions

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

| Field                    | Type    | Required | Description                                            |
|--------------------------|---------|----------|--------------------------------------------------------|
| `source.repositoryUrl`   | string  | Yes      | Git repository URL containing the function source code |
| `source.authSecretRef`   | object  | No       | Reference to Git repository authentication secret      |
| `registry.path`          | string  | Yes      | Container registry path for the function image         |
| `registry.insecure`      | boolean | No       | Allow insecure registry connections                    |
| `registry.authSecretRef` | object  | No       | Reference to registry authentication secret            |

### Function Status

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Function name from metadata |
| `runtime` | string | Detected function runtime |
| `conditions` | array | Status conditions |

## Uninstallation

Remove the operator and CRDs:

```bash
# Undeploy operator
make undeploy

# Uninstall CRDs
make uninstall
```