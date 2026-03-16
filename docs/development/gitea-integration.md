# Gitea Integration for E2E Tests

## Overview

The e2e test suite uses an in-cluster Gitea instance to provide git repository functionality. This eliminates external dependencies on GitHub and provides complete isolation for testing.

## Architecture

- **Gitea Installation**: Deployed via Helm during cluster setup
- **Network Access**: NodePort on port 30000 (HTTP) and 30022 (SSH)
- **Service Discovery**: ConfigMap in kube-public namespace contains endpoint
- **Authentication**: Admin user (giteaadmin/giteapass) for test operations
- **Provider Abstraction**: Tests use `RepositoryProvider` interface, allowing easy switching between Gitea/GitHub/GitLab

## Using Repository Provider in Tests

### Basic Pattern

```go
var (
    repoURL    string
    repoDir    string
)

BeforeEach(func() {
    var err error

    // Create repository provider resources with automatic cleanup
    username, password, _, cleanup, err := repoProvider.CreateRandomUser()
    Expect(err).NotTo(HaveOccurred())
    DeferCleanup(cleanup)

    _, repoURL, cleanup, err = repoProvider.CreateRandomRepo(username, false)
    Expect(err).NotTo(HaveOccurred())
    DeferCleanup(cleanup)

    // Initialize with function code
    repoDir, err = InitializeRepoWithFunction(repoURL, username, password, "go")
    Expect(err).NotTo(HaveOccurred())
    DeferCleanup(os.RemoveAll, repoDir)
})
```

### Available Helper Methods

**RepositoryProvider Interface:**
- `CreateUser(username, password, email string) (cleanup func(), err error)` - Create user with cleanup function
- `CreateRandomUser() (username, password, email string, cleanup func(), err error)` - Create user with random credentials
- `CreateRepo(owner, name string, private bool) (url string, cleanup func(), err error)` - Create repository
- `CreateRandomRepo(owner string, private bool) (name, url string, cleanup func(), err error)` - Create repo with random name
- `CreateAccessToken(username, password, tokenName string) (string, error)` - Generate access token

**E2E Helper Functions:**
- `InitializeRepoWithFunction(url, user, pass, lang)` - Clone, init function, push
- `CommitAndPush(repoDir, msg, file, ...otherFiles)` - Commit and push files

### DeferCleanup Pattern

All Create methods return cleanup functions that can be used with Ginkgo's `DeferCleanup`:

```go
username, password, _, cleanup, err := repoProvider.CreateRandomUser()
Expect(err).NotTo(HaveOccurred())
DeferCleanup(cleanup)  // Automatically deletes user after test
```

This ensures resources are cleaned up even if tests fail.

## Accessing Gitea UI

During development, you can access the Gitea web UI:

1. Get the Gitea endpoint:
   ```bash
   kubectl get configmap gitea-endpoint -n kube-public -o jsonpath='{.data.http}'
   ```

2. Open in browser and login:
   - Username: `giteaadmin`
   - Password: `giteapass`

## Testing Private Repositories

```go
// Create private repo
_, repoURL, cleanup, err := repoProvider.CreateRandomRepo(username, true)
Expect(err).NotTo(HaveOccurred())
DeferCleanup(cleanup)

// Create access token
token, err := repoProvider.CreateAccessToken(username, password, "test-token")

// Use token in Function spec
Spec: functionsdevv1alpha1.FunctionSpec{
    Source: functionsdevv1alpha1.FunctionSpecSource{
        RepositoryURL: repoURL,
        Credentials: &functionsdevv1alpha1.Credentials{
            Token: token,
        },
    },
}
```

## Bundle Test Pattern

Bundle tests use the `TestNamespace` struct for better organization:

```go
type TestNamespace struct {
    Name    string
    RepoURL string
}

func createNamespaceAndDeployFunction() TestNamespace {
    // Creates namespace and deploys function
    // Returns TestNamespace with both namespace and repo URL
}

// Usage
testNs := createNamespaceAndDeployFunction()
CreateFunctionAndWaitForReady(testNs)
```

## Troubleshooting

### Gitea not accessible

```bash
kubectl get pods -n gitea
kubectl logs -n gitea deployment/gitea
```

### ConfigMap missing

```bash
kubectl get configmap -n kube-public
./hack/create-kind-cluster.sh  # Recreate cluster
```

### Test failures with git operations

- Check Gitea pod is running
- Verify network connectivity to NodePort:
  ```bash
  GITEA_IP=$(kubectl get configmap gitea-endpoint -n kube-public -o jsonpath='{.data.http}')
  curl $GITEA_IP
  ```
- Check git credentials in test

### Repository cleanup issues

If tests leave behind repositories, you can clean them manually:

```bash
# List all users
curl -u giteaadmin:giteapass http://<gitea-endpoint>/api/v1/admin/users

# Delete user (includes their repos)
curl -X DELETE -u giteaadmin:giteapass http://<gitea-endpoint>/api/v1/admin/users/<username>
```

## Implementation Details

### Cluster Setup

Gitea is installed during cluster creation in `hack/create-kind-cluster.sh`:

```bash
helm install gitea gitea-charts/gitea --namespace gitea --create-namespace \
  --set service.http.type=NodePort \
  --set service.http.nodePort=30000 \
  --set gitea.admin.username=giteaadmin \
  --set gitea.admin.password=giteapass \
  --set persistence.enabled=false
```

### Service Discovery

The cluster setup creates a ConfigMap with the Gitea endpoint:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: gitea-endpoint
  namespace: kube-public
data:
  http: "http://<node-ip>:30000"
  ssh: "<node-ip>:30022"
```

Tests read this ConfigMap to discover where Gitea is running.

### Networking

- **Kind Node IP**: Retrieved via Docker inspect of control-plane node
- **NodePort**: Fixed ports 30000 (HTTP) and 30022 (SSH)
- **Accessibility**: Node IP is reachable from both host machine and cluster pods

This allows the same repository URL to work in both test code (running on host) and Function resources (running in cluster).