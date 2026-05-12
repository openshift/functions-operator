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
- `CreateUser(username, password, email string) (cleanup func(), err error)` — Create user with cleanup function
- `DeleteUser(username string) error` — Delete a user and all their data
- `CreateRandomUser() (username, password, email string, cleanup func(), err error)` — Create user with random credentials
- `CreateRepo(owner, name string, private bool) (url string, cleanup func(), err error)` — Create repository
- `DeleteRepo(owner, name string) error` — Delete a repository
- `CreateRandomRepo(owner string, private bool) (name, url string, cleanup func(), err error)` — Create repo with random name
- `CreateAccessToken(username, password, tokenName string) (string, error)` — Generate access token
- `CreateSSHKey(username, password, title, publicKey string) error` — Register SSH public key for user
- `SSHRepoURL(owner, repo string) (string, error)` — Get SSH URL for a repository

Note: `CreateUser`, `CreateRepo`, and `CreateRandomRepo` return cleanup functions that call `DeleteUser`/`DeleteRepo` internally. Prefer `DeferCleanup(cleanup)` over calling the delete methods directly.

**Git Helper Functions:**
- `InitializeRepoWithFunction(url, user, pass, lang string, opts ...RepoOption) (repoDir string, err error)` — Clone, init function, push
- `CommitAndPush(repoDir, msg, file string, otherFiles ...string) error` — Commit and push files

`InitializeRepoWithFunction` accepts functional options:
- `WithSubDir(subDir string)` — Place the function in a subdirectory (for monorepo testing)
- `WithCliVersion(version string)` — Use a specific func CLI version to initialize the function

**func CLI Helper Functions:**
- `RunFunc(command string, args ...string) (string, error)` — Run the current/latest func CLI
- `RunFuncWithVersion(version, command string, args ...string) (string, error)` — Run a specific func CLI version (downloads and caches automatically)
- `RunFuncDeploy(functionDir string, opts ...FuncDeployOption) (string, error)` — Deploy a function with retry logic

`RunFuncDeploy` accepts functional options:
- `WithNamespace(namespace string)` — Target namespace
- `WithBuilder(builder string)` — Builder to use (e.g. `pack`, `s2i`)
- `WithDeployer(deployer string)` — Deployer to use (e.g. `knative`, `keda`)
- `WithDeployCliVersion(version string)` — Use a specific func CLI version
- `WithEnvVars(envVars map[string]string)` — Set environment variables for the deploy command

Defaults for `RunFuncDeploy` are read from environment variables: `REGISTRY` (or `REGISTRY_URL`), `REGISTRY_INSECURE`, `DEFAULT_BUILDER`, `DEFAULT_DEPLOYER`.

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

// Create auth secret with token
secret := &v1.Secret{
    ObjectMeta: metav1.ObjectMeta{
        GenerateName: "git-auth-",
        Namespace:    functionNamespace,
    },
    Data: map[string][]byte{
        "token": []byte(token),
    },
}

// Use in Function spec
Spec: functionsdevv1alpha1.FunctionSpec{
    Repository: functionsdevv1alpha1.FunctionSpecRepository{
        URL: repoURL,
        AuthSecretRef: &v1.LocalObjectReference{
            Name: secret.Name,
        },
    },
}
```

## Testing SSH Repositories

```go
BeforeEach(func() {
    // Create user and HTTP repo as usual
    username, password, _, cleanup, err := repoProvider.CreateRandomUser()
    Expect(err).NotTo(HaveOccurred())
    DeferCleanup(cleanup)

    repoName, repoURL, cleanup, err := repoProvider.CreateRandomRepo(username, false)
    Expect(err).NotTo(HaveOccurred())
    DeferCleanup(cleanup)

    // Generate SSH keypair and register with Gitea
    keyDir, err := os.MkdirTemp("", "ssh-e2e-*")
    Expect(err).NotTo(HaveOccurred())
    DeferCleanup(os.RemoveAll, keyDir)

    sshKeyPath := filepath.Join(keyDir, "id_ed25519")
    cmd := exec.Command("ssh-keygen", "-t", "ed25519", "-f", sshKeyPath, "-N", "", "-q")
    _, err = utils.Run(cmd)
    Expect(err).NotTo(HaveOccurred())

    pubKeyBytes, err := os.ReadFile(sshKeyPath + ".pub")
    Expect(err).NotTo(HaveOccurred())

    err = repoProvider.CreateSSHKey(username, password, "e2e-key", string(pubKeyBytes))
    Expect(err).NotTo(HaveOccurred())

    sshRepoURL, err := repoProvider.SSHRepoURL(username, repoName)
    Expect(err).NotTo(HaveOccurred())

    // Initialize repo via HTTP (SSH is for the operator, not test setup)
    repoDir, err = utils.InitializeRepoWithFunction(repoURL, username, password, "go")
    Expect(err).NotTo(HaveOccurred())
    DeferCleanup(os.RemoveAll, repoDir)
})
```

### SSH Auth Secret

The operator authenticates SSH connections using a Kubernetes Secret with an `sshPrivateKey` field:

```go
secret := &v1.Secret{
    ObjectMeta: metav1.ObjectMeta{
        GenerateName: "git-ssh-auth-",
        Namespace:    functionNamespace,
    },
    Data: map[string][]byte{
        "sshPrivateKey": privateKeyBytes,
    },
}

// Use in Function spec
Spec: functionsdevv1alpha1.FunctionSpec{
    Repository: functionsdevv1alpha1.FunctionSpecRepository{
        URL: sshRepoURL,
        AuthSecretRef: &v1.LocalObjectReference{
            Name: secret.Name,
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