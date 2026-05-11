# Reconcile State Design

Centralize all status and condition updates in the function controller by introducing a `reconcileState` struct and a single `syncStatus` function.

## Problem

Conditions (`Mark*` calls) and status field writes are scattered across 6 methods in 2 files. It is hard to reason about what state the Function ends up in after reconciliation. The `reconcileError` type wraps condition info into errors, coupling error handling with status logic.

## Design

Inspired by the Kubernetes Job controller's `syncJobCtx` pattern: a state struct accumulates data during reconciliation, and one function translates it into status writes and conditions.

### Structs

```go
type reconcileState struct {
    source     *sourceState
    deployment *deploymentState
    middleware *middlewareState
}

type sourceState struct {
    name        string
    branch      string
    commit      string
    failReason  string
    failMessage string
}

type deploymentState struct {
    deployed    bool
    deployer    string
    runtime     string
    image       string
    ready       string // "true"/"false"/unknown from describe
    failReason  string
    failMessage string
}

type middlewareState struct {
    updateEnabled  bool
    updateSource   string
    isLatest       bool
    currentVersion string
    latestVersion  string
    pendingRebuild bool
    redeployed     bool
    lastRebuild    metav1.Time
    failReason     string
    failMessage    string
}
```

Each phase struct is nil until that phase runs. Nil means conditions stay Unknown (from `InitializeConditions`).

### `syncStatus`

Single function that translates state into `Mark*` calls and `function.Status` field writes. It is the only place in the controller that calls `Mark*` or writes to `function.Status`.

Each section follows the same shape:
1. Guard: if phase struct is nil, return
2. Check failure: if `failReason` is set, set the failure condition, return
3. Set success condition
4. Write status fields

```go
func syncStatus(function *v1alpha1.Function, state *reconcileState) {
    // --- Source ---
    if state.source == nil {
        return
    }
    if state.source.failReason != "" {
        function.MarkSourceNotReady(state.source.failReason, "%s", state.source.failMessage)
        return
    }
    function.MarkSourceReady()
    function.Status.Name = state.source.name
    function.Status.Git.ResolvedBranch = state.source.branch
    function.Status.Git.ObservedCommit = state.source.commit
    function.Status.Git.LastChecked = metav1.Now()

    // --- Deployment ---
    if state.deployment == nil {
        return
    }
    if state.deployment.failReason != "" {
        function.MarkDeployNotReady(state.deployment.failReason, "%s", state.deployment.failMessage)
        return
    }
    if !state.deployment.deployed {
        function.MarkDeployNotReady("NotDeployed", "Function not deployed yet")
        return
    }
    function.MarkDeployReady()
    function.Status.Deployment.Deployer = state.deployment.deployer
    function.Status.Deployment.Runtime = state.deployment.runtime
    function.Status.Deployment.Image = state.deployment.image
    markServiceStatus(state.deployment.ready, function)

    // --- Middleware ---
    if state.middleware == nil {
        return
    }
    if state.middleware.failReason != "" {
        function.MarkMiddlewareNotUpToDate(state.middleware.failReason, "%s", state.middleware.failMessage)
        return
    }
    switch {
    case state.middleware.isLatest:
        function.MarkMiddlewareUpToDate()
    case !state.middleware.updateEnabled:
        function.Status.Middleware.Available = ptr.To(state.middleware.latestVersion)
        function.MarkMiddlewareNotUpToDateIntentionally("SkipMiddlewareUpdate",
            "Skipping middleware update as update is disabled (source: %s)", state.middleware.updateSource)
    case state.middleware.redeployed:
        function.Status.Middleware.Available = nil
        function.Status.Middleware.LastRebuild = state.middleware.lastRebuild
        function.Status.Deployment.ImageBuilt = state.middleware.lastRebuild
        function.MarkMiddlewareUpToDate()
        function.MarkDeployReady()
    }
    function.Status.Middleware.AutoUpdate.Enabled = state.middleware.updateEnabled
    function.Status.Middleware.AutoUpdate.Source = state.middleware.updateSource
    function.Status.Middleware.Current = state.middleware.currentVersion
    function.Status.Middleware.PendingRebuild = state.middleware.pendingRebuild
}
```

### `Reconcile` and `reconcile` flow

`Reconcile()` calls `syncStatus` once before flushing, so `reconcile()` never thinks about status:

```go
func (r *FunctionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    // ... get function, deep copy ...
    state := &reconcileState{}
    statusTracker := NewStatusTracker(r.Client, function)
    ctx = WithStatusTracker(ctx, statusTracker)

    reconcileErr := r.reconcile(ctx, function, state)
    syncStatus(function, state)

    if err := statusTracker.Flush(ctx, function); err != nil {
        return ctrl.Result{}, err
    }
    // ...
}

func (r *FunctionReconciler) reconcile(ctx context.Context, function *v1alpha1.Function, state *reconcileState) error {
    function.InitializeConditions()

    repo, err := r.prepareSource(ctx, function, state)
    if err != nil {
        return fmt.Errorf("prepare source failed: %w", err)
    }
    defer repo.Cleanup()

    applyLastDeployedAnnotation(ctx, function, state)

    if err := r.ensureDeployment(ctx, function, repo, state); err != nil {
        return fmt.Errorf("deploying function failed: %w", err)
    }
    return nil
}
```

### Mid-reconcile flush

For long-running deploys, `redeployMiddleware` updates the state struct and explicitly flushes:

```go
state.middleware.pendingRebuild = true
state.middleware.failReason = "MiddlewareOutdated"
state.middleware.failMessage = fmt.Sprintf("Middleware is outdated (%s available), redeploying...", state.middleware.latestVersion)

syncStatus(function, state)
FlushStatus(ctx, function)

// ... deploy happens ...

state.middleware.pendingRebuild = false
state.middleware.redeployed = true
state.middleware.failReason = ""
state.middleware.failMessage = ""
```

### Helpers

Helpers populate the state struct and return plain errors. They never call `Mark*` or write to `function.Status`.

`reconcileError` is removed.

### Files

| File | Change |
|---|---|
| `function_status.go` | New: `reconcileState`, `sourceState`, `deploymentState` structs, `syncStatus` function |
| `function_controller.go` | `Reconcile()` calls `syncStatus`, `reconcile()` passes state, `reconcileError` removed, `prepareSource`/`ensureDeployment` rewritten |
| `function_middleware.go` | All methods take state, no `Mark*` calls, `middlewareState` extended |
| `function_deploy.go` | Unchanged |
| `function_rbac.go` | Unchanged |
| `status_tracker.go` | Unchanged |
| `function_lifecycle.go` | Unchanged |
| `function_controller_test.go` | Behavior-compatible, no structural changes expected |

### Tradeoffs

- **Pro**: All condition logic in one place, easy to read the full status story
- **Pro**: Helpers are pure data gatherers, easy to test
- **Pro**: Mid-reconcile flushes use the same mechanism
- **Con**: State struct and `syncStatus` must be kept in sync with helpers — two places to update when adding new status fields