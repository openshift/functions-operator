# Status Tracker for Immediate Status Updates

**Date:** 2026-04-10  
**Status:** Approved

## Problem

The Function controller currently only updates status at the end of reconciliation (lines 98-103 in `function_controller.go`). This causes delayed status updates during long operations like redeployment, leaving users without visibility into what's happening.

## Solution

Introduce a `StatusTracker` that:
1. Captures the original function state at reconciliation start
2. Lives in the context for easy access
3. Automatically flushes status updates when changes occur
4. Reduces unnecessary API calls by comparing before updating

## Design

### StatusTracker Structure

```go
type StatusTracker struct {
    client client.Client
    original *v1alpha1.Function
}

func NewStatusTracker(client client.Client, function *v1alpha1.Function) *StatusTracker {
    return &StatusTracker{
        client: client,
        original: function.DeepCopy(), // snapshot current state
    }
}

func (t *StatusTracker) Flush(ctx context.Context, current *v1alpha1.Function) error {
    // Always calculate ready condition before comparing
    current.CalculateReadyCondition()
    
    // Compare and update if changed
    if !equality.Semantic.DeepEqual(t.original.Status, current.Status) {
        if err := t.client.Status().Update(ctx, current); err != nil {
            return err
        }
        // Update our snapshot to the new state
        t.original = current.DeepCopy()
    }
    return nil
}
```

### Context Integration

```go
type statusTrackerKey struct{}

// WithStatusTracker adds a status tracker to the context
func WithStatusTracker(ctx context.Context, tracker *StatusTracker) context.Context {
    return context.WithValue(ctx, statusTrackerKey{}, tracker)
}

// GetStatusTracker retrieves the tracker from context
func GetStatusTracker(ctx context.Context) *StatusTracker {
    tracker, ok := ctx.Value(statusTrackerKey{}).(*StatusTracker)
    if !ok {
        return nil
    }
    return tracker
}

// FlushStatus is a convenience helper that gets tracker from context and flushes
func FlushStatus(ctx context.Context, function *v1alpha1.Function) error {
    tracker := GetStatusTracker(ctx)
    if tracker == nil {
        return nil // gracefully handle missing tracker
    }
    return tracker.Flush(ctx, function)
}
```

### Reconciler Integration

```go
func (r *FunctionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    logger := log.FromContext(ctx).WithValues("function", fmt.Sprintf("%s/%s", req.Namespace, req.Name))
    ctx = log.IntoContext(ctx, logger)

    original := &v1alpha1.Function{}
    err := r.Get(ctx, req.NamespacedName, original)
    if err != nil {
        if apierrors.IsNotFound(err) {
            logger.Info("function resource not found. Ignoring since object must be deleted")
            return ctrl.Result{}, nil
        }
        logger.Error(err, "Failed to get function")
        return ctrl.Result{}, err
    }

    function := original.DeepCopy()
    
    // Create tracker and add to context
    tracker := NewStatusTracker(r.Client, function)
    ctx = WithStatusTracker(ctx, tracker)
    
    reconcileErr := r.reconcile(ctx, function)
    
    // Final flush at the end (handles ready condition calculation)
    if err := tracker.Flush(ctx, function); err != nil {
        logger.Error(err, "Unable to update Function status")
        return ctrl.Result{}, err
    }

    if reconcileErr != nil {
        logger.Error(reconcileErr, "Failed to reconcile Function")
        return ctrl.Result{}, reconcileErr
    }

    logger.Info("Reconciliation complete")
    return ctrl.Result{}, nil
}
```

### Checkpoint Placement Strategy

**Flush on phase completions, not on errors** (final flush catches errors anyway):

1. **After source preparation** - Users see git clone succeeded, commit info available
2. **Before long operations** - Users see "about to deploy" before redeployment starts
3. **Final flush** - Always happens in `Reconcile()` regardless of success/failure

```go
func (r *FunctionReconciler) reconcile(ctx context.Context, function *v1alpha1.Function) error {
    function.InitializeConditions()

    repo, metadata, err := r.prepareSource(ctx, function)
    if err != nil {
        return fmt.Errorf("prepare source failed: %w", err)
    }
    defer repo.Cleanup()

    r.updateFunctionStatusGit(function, repo)
    FlushStatus(ctx, function) // Checkpoint 1: Source ready

    if err := r.ensureDeployment(ctx, function, repo, metadata); err != nil {
        return fmt.Errorf("deploying function failed: %w", err)
    }

    r.updateFunctionStatus(function, metadata)
    return nil
}

func (r *FunctionReconciler) handleMiddlewareUpdate(ctx context.Context, function *v1alpha1.Function, repo *git.Repository, metadata *funcfn.Function) error {
    isOnLatestMiddleware, err := r.isMiddlewareLatest(ctx, metadata, function.Namespace)
    if err != nil {
        function.MarkMiddlewareNotUpToDate("MiddlewareCheckFailed", "Failed to check middleware version: %s", err.Error())
        return fmt.Errorf("failed to check if function is using latest middleware: %w", err)
    }

    if !isOnLatestMiddleware {
        logger.Info("Function is not on latest middleware. Will redeploy")
        function.MarkMiddlewareNotUpToDate("MiddlewareOutdated", "Middleware is outdated, redeploying")
        FlushStatus(ctx, function) // Checkpoint 2: Before long deploy operation
        
        if err := r.deploy(ctx, function, repo); err != nil {
            function.MarkDeployNotReady("DeployFailed", "Redeployment failed: %s", err.Error())
            return fmt.Errorf("failed to redeploy function: %w", err)
        }
    }
    
    function.MarkMiddlewareUpToDate()
    function.MarkDeployReady()
    return nil
}
```

## Benefits

1. **Immediate visibility** - Users see status updates as phases complete
2. **No signature changes** - Context pattern avoids passing tracker through every function
3. **Automatic deduplication** - Tracker prevents unnecessary API calls when status unchanged
4. **Error handling** - Final flush ensures errors are captured even without intermediate flushes
5. **Encapsulation** - All comparison/update logic lives in one place

## Future Enhancements

- Add retry logic with `retry.RetryOnConflict` for handling conflicts during intermediate updates
- Add metrics for tracking flush frequency and update patterns