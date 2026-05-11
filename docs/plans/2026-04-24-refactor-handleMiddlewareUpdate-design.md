# Refactor `handleMiddlewareUpdate`

## Problem

`handleMiddlewareUpdate` (lines 251-332 in `function_controller.go`) interleaves three concerns:

1. **Querying state** - two `Describe` calls, `isMiddlewareUpdateEnabled`, `isMiddlewareLatest`, `GetLatestMiddlewareVersion`
2. **Decision logic** - should we redeploy or not?
3. **Status bookkeeping** - ~15 status field assignments scattered throughout

This makes the method hard to follow. There's also a redundant `Describe` call — the second one (line 321) re-fetches data that only changes after a redeploy.

## Design

### Sum-type pattern for middleware check results

Introduce a sealed interface with two concrete types, separating "what we observed" from "what we should do":

```go
type middlewareCheck interface {
    middlewareCheck() // sealed marker
}

type middlewareUpToDate struct {
    currentImage   string
    serviceReady   string
    currentVersion string
    autoUpdate     autoUpdateStatus
}

type middlewareOutdated struct {
    currentImage     string
    serviceReady     string
    currentVersion   string
    availableVersion string
    autoUpdate       autoUpdateStatus
}

type autoUpdateStatus struct {
    enabled bool
    source  string
}
```

- `middlewareUpToDate`: deployed version == latest. No `availableVersion` field needed.
- `middlewareOutdated`: deployed version != latest. Carries `availableVersion` and `autoUpdate.enabled` to distinguish "needs update" from "update disabled."

### `checkMiddlewareState` method

Consolidates all querying into one method. Uses a single `Describe` call (instead of two) since we already have the current version from `Describe` and only need `GetLatestMiddlewareVersion` to compare:

```go
func (r *FunctionReconciler) checkMiddlewareState(
    ctx context.Context,
    function *v1alpha1.Function,
    metadata *funcfn.Function,
) (middlewareCheck, error)
```

### Refactored `handleMiddlewareUpdate`

Becomes a thin orchestrator with a type switch:

- `middlewareUpToDate`: set status fields, mark middleware up-to-date
- `middlewareOutdated` + `autoUpdate.enabled == false`: set status fields, mark intentionally not up-to-date
- `middlewareOutdated` + `autoUpdate.enabled == true`: flush status, deploy, re-describe, update status, record history event

The second `Describe` call only happens inside the deploy branch (where it's actually needed).

### Cleanup

`isMiddlewareLatest` is no longer needed — the version comparison happens inside `checkMiddlewareState` using data from the single `Describe` call.

## Decisions

- **Keep status field duplication across switch cases** — each case is self-contained and readable top-to-bottom
- **`autoUpdate` lives as a field on `middlewareOutdated`** rather than being a third type — the two outdated cases share the same data
- **All types are unexported** — this is controller-internal