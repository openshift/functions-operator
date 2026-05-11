# Gitea E2E Integration Design

**Date:** 2026-03-13
**Status:** Approved

## Overview

This design integrates Gitea into our e2e test infrastructure to eliminate dependency on GitHub for test function repositories. Tests will use an in-cluster Gitea instance for all git operations, providing complete isolation and enabling testing of private repositories and various authentication methods.

## Goals

- Remove GitHub dependency from e2e tests
- Enable per-test isolation with dedicated git repositories
- Support testing private repositories with token authentication
- Provide infrastructure for future SSH authentication testing
- Keep test code focused on operator logic, not git infrastructure

## Architecture

The integration consists of three layers:

### 1. Infrastructure Layer
Cluster setup with Gitea installation and configuration

### 2. Utilities Layer
Go client and helpers for managing Gitea resources

### 3. Test Integration Layer
Updates to existing e2e tests to use Gitea instead of GitHub

## Infrastructure Layer

### Gitea Installation (`hack/create-kind-cluster.sh`)

Update the existing but disabled `install_gitea()` function:

```bash
function install_gitea() {
  header_text "Installing Gitea"

  helm repo add gitea-charts https://dl.gitea.com/charts/
  helm install gitea gitea-charts/gitea --namespace gitea --create-namespace \
    --set service.http.type=NodePort \
    --set service.http.nodePort=30000 \
    --set service.ssh.type=NodePort \
    --set service.ssh.nodePort=30022 \
    --set gitea.admin.username=giteaadmin \
    --set gitea.admin.password=giteapass \
    --set gitea.admin.email=admin@gitea.local \
    --set persistence.enabled=false

  header_text "Waiting for Gitea to become ready"
  kubectl wait deployment --all --timeout=-1s --for=condition=Available --namespace gitea

  # Get Gitea endpoint for tests
  GITEA_NODE_IP=$(docker inspect kind-control-plane --format '{{.NetworkSettings.Networks.kind.IPAddress}}')

  # Create ConfigMap with Gitea endpoint info
  kubectl apply -f - <<EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: gitea-endpoint
  namespace: kube-public
data:
  http: "http://${GITEA_NODE_IP}:30000"
  ssh: "${GITEA_NODE_IP}:30022"
EOF
}
```

Enable in main flow by adding `install_gitea` call after `install_prometheus`.

### Networking

- **Service Type:** NodePort on fixed ports (HTTP: 30000, SSH: 30022)
- **Access Method:** Use Kind control-plane node IP from Docker network
- **URL Format:** `http://<node-ip>:30000`
- **Why this works:**
  - Kind node IP is reachable from host (via Docker networking)
  - Same IP is reachable from pods inside cluster (node IPs)
  - No port-forwarding or DNS tricks needed
  - Single consistent address for tests and operator

## Utilities Layer

### Dependencies

Add to `go.mod`:
```
code.gitea.io/sdk/gitea v0.x.x
```

### GiteaClient Structure (`test/utils/gitea.go`)

```go
package utils

import (
    "fmt"
    "os/exec"

    "code.gitea.io/sdk/gitea"
    "k8s.io/apimachinery/pkg/util/rand"
)

type GiteaClient struct {
    client    *gitea.Client
    baseURL   string  // http://172.18.0.2:30000
    adminUser string
    adminPass string
}

// NewGiteaClient discovers Gitea endpoint from ConfigMap and creates client
func NewGiteaClient() (*GiteaClient, error) {
    // Read gitea-endpoint ConfigMap from kube-public namespace
    // Extract baseURL from data.http
    // Create Gitea SDK client with admin credentials
    // Return initialized GiteaClient
}

// User management
func (g *GiteaClient) CreateUser(username, password, email string) error

func (g *GiteaClient) CreateRandomUser() (username, password, email string, err error) {
    username = "user-" + rand.String(8)
    password = "pass-" + rand.String(8)
    email = username + "@test.local"

    err = g.CreateUser(username, password, email)
    return username, password, email, err
}

func (g *GiteaClient) DeleteUser(username string) error

// Repository management
func (g *GiteaClient) CreateRepo(owner, name string, private bool) (string, error) {
    // Returns repository URL: http://172.18.0.2:30000/owner/name.git
}

func (g *GiteaClient) CreateRandomRepo(owner string, private bool) (name, url string, err error) {
    name = "repo-" + rand.String(8)
    url, err = g.CreateRepo(owner, name, private)
    return name, url, err
}

func (g *GiteaClient) DeleteRepo(owner, name string) error

// Token authentication
func (g *GiteaClient) CreateAccessToken(username, password, tokenName string) (string, error)
```

### Helper Functions (`test/e2e/gitea_helpers.go`)

```go
package e2e

import (
    "fmt"
    "os"
    "os/exec"
    "strings"

    "github.com/functions-dev/func-operator/test/utils"
    "k8s.io/apimachinery/pkg/util/rand"
)

// buildAuthURL embeds credentials into git URL
func buildAuthURL(repoURL, username, password string) string {
    return strings.Replace(repoURL, "http://",
        fmt.Sprintf("http://%s:%s@", username, password), 1)
}

// InitializeRepoWithFunction clones an empty Gitea repo, initializes a function, and pushes it
func InitializeRepoWithFunction(repoURL, username, password, language string) (repoDir string, err error) {
    repoDir = fmt.Sprintf("%s/func-test-%s", os.TempDir(), rand.String(10))

    // Build authenticated URL
    authURL := buildAuthURL(repoURL, username, password)

    // Clone empty repo
    cmd := exec.Command("git", "clone", authURL, repoDir)
    if _, err = utils.Run(cmd); err != nil {
        return "", err
    }

    // Initialize function
    cmd = exec.Command("func", "init", "-l", language)
    cmd.Dir = repoDir
    if _, err = utils.Run(cmd); err != nil {
        return "", err
    }

    // Commit and push
    exec.Command("git", "-C", repoDir, "add", ".").Run()
    exec.Command("git", "-C", repoDir, "commit", "-m", "Initial function").Run()
    exec.Command("git", "-C", repoDir, "push").Run()

    return repoDir, nil
}

// CommitAndPushFuncYaml commits and pushes func.yaml changes after deployment
func CommitAndPushFuncYaml(repoDir string) error {
    exec.Command("git", "-C", repoDir, "add", "func.yaml").Run()
    exec.Command("git", "-C", repoDir, "commit", "-m", "Update func.yaml after deploy").Run()
    return exec.Command("git", "-C", repoDir, "push").Run()
}
```

## Test Integration Layer

### Suite Setup (`test/e2e/e2e_suite_test.go`)

Add package-level variable:
```go
var (
    k8sClient client.Client
    ctx       context.Context

    registry         string
    registryInsecure bool

    giteaClient *utils.GiteaClient  // NEW
)
```

Update BeforeSuite:
```go
var _ = BeforeSuite(func() {
    ctx = context.Background()

    // ... existing k8sClient setup ...

    // ... existing registry setup ...

    // Initialize Gitea client
    var err error
    giteaClient, err = utils.NewGiteaClient()
    Expect(err).NotTo(HaveOccurred())
    Expect(giteaClient).NotTo(BeNil())
})
```

### Test Pattern

Standard pattern for tests using Gitea:

```go
var _ = Describe("Test Suite", func() {
    var (
        username   string
        password   string
        repoName   string
        repoURL    string
        repoDir    string
    )

    BeforeEach(func() {
        var err error

        // Create random user
        username, password, _, err = giteaClient.CreateRandomUser()
        Expect(err).NotTo(HaveOccurred())

        // Create random repo
        repoName, repoURL, err = giteaClient.CreateRandomRepo(username, false)
        Expect(err).NotTo(HaveOccurred())

        // Initialize with function code
        repoDir, err = InitializeRepoWithFunction(repoURL, username, password, "go")
        Expect(err).NotTo(HaveOccurred())

        // Deploy function (example)
        cmd := exec.Command("func", "deploy",
            "--path", repoDir,
            "--registry", registry,
            "--registry-insecure", strconv.FormatBool(registryInsecure))
        out, err := utils.Run(cmd)
        Expect(err).NotTo(HaveOccurred())

        // Commit func.yaml changes
        CommitAndPushFuncYaml(repoDir)
    })

    AfterEach(func() {
        // Cleanup
        os.RemoveAll(repoDir)
        giteaClient.DeleteRepo(username, repoName)
        giteaClient.DeleteUser(username)
    })

    It("should do something", func() {
        // Test uses repoURL in Function spec
        function := &functionsdevv1alpha1.Function{
            Spec: functionsdevv1alpha1.FunctionSpec{
                Source: functionsdevv1alpha1.FunctionSpecSource{
                    RepositoryURL: repoURL,
                },
                // ...
            },
        }
        // ...
    })
})
```

### Updates to Existing Tests

**`test/e2e/func_deploy_test.go`:**
- Replace `git clone https://github.com/creydr/func-go-hello-world` with Gitea workflow
- Replace hardcoded `RepositoryURL: "https://github.com/..."` with `repoURL`
- Add cleanup for Gitea resources in AfterEach

**`test/e2e/bundle_test.go`:**
- Update `CreateFunctionAndWaitForReady()` to accept `repoURL` parameter
- Update `createNamespaceAndDeployFunction()` to use Gitea workflow
- Replace GitHub URLs throughout

## Implementation Steps

1. **Update cluster setup script**
   - Enable `install_gitea()` function
   - Add call to main flow
   - Test cluster creation end-to-end

2. **Create utilities layer**
   - Add Gitea SDK dependency
   - Implement `test/utils/gitea.go`
   - Implement `test/e2e/gitea_helpers.go`

3. **Update test suite**
   - Add giteaClient initialization in BeforeSuite
   - Test basic connectivity and operations

4. **Migrate tests**
   - Update `func_deploy_test.go`
   - Update `bundle_test.go`
   - Run e2e tests to verify

5. **Documentation**
   - Update README with Gitea information
   - Document how to access Gitea UI during development

## Benefits

1. **No External Dependencies:** Tests run without internet or GitHub access
2. **Complete Isolation:** Each test gets fresh user and repository
3. **Faster Tests:** No network latency to external services
4. **Reproducible:** Identical Gitea state on every test run
5. **Extended Testing:** Ready for private repos, SSH, multiple auth methods
6. **Self-Contained:** All test infrastructure in the cluster

## Future Enhancements

Once basic integration is complete, we can add:

1. **SSH Key Authentication**
   - `GenerateSSHKeyPair()` method
   - `AddSSHKey()` method
   - SSH URL helper functions

2. **Advanced Git Scenarios**
   - Multiple branches
   - Tags and releases
   - Submodules
   - Webhooks

3. **Performance Testing**
   - Test with large repositories
   - Concurrent git operations

4. **Failure Scenarios**
   - Invalid credentials
   - Repository permissions
   - Network failures