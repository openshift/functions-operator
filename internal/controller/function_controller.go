/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/functions-dev/func-operator/internal/funccli"
	fn "github.com/functions-dev/func-operator/internal/function"
	"github.com/functions-dev/func-operator/internal/git"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"
	funcfn "knative.dev/func/pkg/functions"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/functions-dev/func-operator/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

const (
	deployFunctionRoleName = "func-operator-deploy-function"
	controllerConfigName   = "func-operator-controller-config"

	funcAnnotationPrefix       = "functions.knative.dev/"
	funcAnnotationLastDeployed = funcAnnotationPrefix + "last-deployed"
)

// FunctionReconciler reconciles a Function object
type FunctionReconciler struct {
	client.Client
	Scheme            *runtime.Scheme
	Recorder          events.EventRecorder
	FuncCliManager    funccli.Manager
	GitManager        git.Manager
	OperatorNamespace string
}

// +kubebuilder:rbac:groups=functions.dev,resources=functions,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=functions.dev,resources=functions/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=functions.dev,resources=functions/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=pods;pods/attach;secrets;services;persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch
// +kubebuilder:rbac:groups="apps",resources=deployments;replicasets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="serving.knative.dev",resources=services;routes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="eventing.knative.dev",resources=triggers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=tekton.dev,resources=pipelines;pipelineruns,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=tekton.dev,resources=taskruns,verbs=get;list;watch
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings;roles,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=http.keda.sh,resources=httpscaledobjects,verbs=get;list;watch;create;update;patch;delete

// Reconcile a Function with status update
func (r *FunctionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("function", fmt.Sprintf("%s/%s", req.Namespace, req.Name))

	// add logger with values to context back
	ctx = log.IntoContext(ctx, logger)

	original := &v1alpha1.Function{}
	err := r.Get(ctx, req.NamespacedName, original)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// If the custom resource is not found then it usually means that it was deleted or not created
			// In this way, we will stop the reconciliation
			logger.Info("function resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		logger.Error(err, "Failed to get function")
		return ctrl.Result{}, err
	}

	function := original.DeepCopy()
	function.SetDefaults(ctx)

	// Create tracker and add to context
	statusTracker := NewStatusTracker(r.Client, function)
	ctx = WithStatusTracker(ctx, statusTracker)

	reconcileErr := r.reconcile(ctx, function)

	// Final flush at the end (handles ready condition calculation)
	if err := statusTracker.Flush(ctx, function); err != nil {
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

func (r *FunctionReconciler) reconcile(ctx context.Context, function *v1alpha1.Function) error {
	// Initialize conditions to start fresh each reconcile
	function.InitializeConditions()

	repo, metadata, err := r.prepareSource(ctx, function)
	if err != nil {
		return fmt.Errorf("prepare source failed: %w", err)
	}
	defer repo.Cleanup()

	function.Status.Name = metadata.Name

	if err := r.reconcileDeployment(ctx, function, repo, metadata); err != nil {
		return fmt.Errorf("deploying function failed: %w", err)
	}

	if err := r.removeFuncAnnotations(ctx, function); err != nil {
		return fmt.Errorf("failed to remove func annotations: %w", err)
	}

	return nil
}

func applyLastDeployedAnnotation(ctx context.Context, function *v1alpha1.Function) {
	if val, ok := function.Annotations[funcAnnotationLastDeployed]; ok {
		t, err := time.Parse(time.RFC3339, val)
		if err != nil {
			// log a warning, but don't return error, as this can't resolve on its own
			log.FromContext(ctx).Info("could not parse "+funcAnnotationLastDeployed+" annotation", "error", err)
		} else {
			function.Status.Deployment.ImageBuilt = metav1.NewTime(t)
			function.RecordHistoryEvent("Function was deployed/redeployed", v1alpha1.WithHistoryEventTime(t))
		}
	}
}

func (r *FunctionReconciler) removeFuncAnnotations(ctx context.Context, function *v1alpha1.Function) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &v1alpha1.Function{}
		if err := r.Get(ctx, types.NamespacedName{Name: function.Name, Namespace: function.Namespace}, latest); err != nil {
			return err
		}

		if !hasFuncAnnotations(latest) {
			return nil
		}

		annotations := latest.GetAnnotations()
		for key := range annotations {
			if strings.HasPrefix(key, funcAnnotationPrefix) {
				delete(annotations, key)
			}
		}

		latest.SetAnnotations(annotations)
		return r.Update(ctx, latest)
	})
}

// prepareSource clones the git repository and retrieves function metadata
func (r *FunctionReconciler) prepareSource(ctx context.Context, function *v1alpha1.Function) (*git.Repository, *funcfn.Function, error) {
	gitAuthSecret := v1.Secret{}
	if function.Spec.Repository.AuthSecretRef != nil {
		if err := r.Get(ctx, types.NamespacedName{Namespace: function.Namespace, Name: function.Spec.Repository.AuthSecretRef.Name}, &gitAuthSecret); err != nil {
			function.MarkSourceNotReady("AuthSecretNotFound", "Auth secret not found: %s", err.Error())
			return nil, nil, err
		}
	}

	repo, err := r.GitManager.CloneRepository(ctx, function.Spec.Repository.URL, function.Spec.Repository.Path, function.Spec.Repository.Branch, gitAuthSecret.Data)
	if err != nil {
		function.MarkSourceNotReady("GitCloneFailed", "Failed to clone repository: %s", err.Error())
		return nil, nil, fmt.Errorf("failed to setup git repository: %w", err)
	}

	metadata, err := fn.Metadata(repo.Path())
	if err != nil {
		function.MarkSourceNotReady("MetadataReadFailed", "Failed to read function metadata: %s", err.Error())
		return nil, nil, fmt.Errorf("failed to get function metadata: %w", err)
	}

	// Source is ready - git clone and metadata read succeeded
	function.MarkSourceReady()

	function.Status.Git.ResolvedBranch = repo.Branch
	function.Status.Git.ObservedCommit = repo.Commit
	function.Status.Git.LastChecked = metav1.Now()

	return repo, &metadata, nil
}

func (r *FunctionReconciler) reconcileDeployment(ctx context.Context, function *v1alpha1.Function, repo *git.Repository, metadata *funcfn.Function) error {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling Function")

	deployed, err := r.isDeployed(ctx, metadata.Name, function.Namespace)
	if err != nil {
		function.MarkDeployNotReady("DeployFailed", "Failed to check deployment status: %s", err.Error())
		return fmt.Errorf("failed to check if function is already deployed: %w", err)
	}

	if !deployed {
		logger.Info("Function is not deployed")
		function.MarkDeployNotReady("NotDeployed", "Function not deployed yet")
		return nil
	}

	// function is deployed -> update status with metadata information
	deployer := metadata.Deploy.Deployer
	if deployer == "" {
		// knative is default deployer
		deployer = "knative"
	}
	function.Status.Deployment.Deployer = deployer
	function.Status.Deployment.Runtime = metadata.Runtime
	applyLastDeployedAnnotation(ctx, function)

	// Function is deployed - check middleware version
	return r.handleMiddlewareUpdate(ctx, function, repo, metadata)
}

// middlewareCheck is a sealed interface representing the result of inspecting a function's
// middleware state. Implementations (middlewareUpToDate, middlewareOutdated) carry only the
// fields relevant to their case, so the caller can type-switch without inspecting irrelevant data.
type middlewareCheck interface {
	middlewareCheck()
}

type middlewareUpToDate struct {
	currentImage    string
	currentRevision string
	serviceReady    string
	currentVersion  string
	serviceUrl      string
	autoUpdate      autoUpdateStatus
}

func (middlewareUpToDate) middlewareCheck() {}

type middlewareOutdated struct {
	currentImage     string
	currentRevision  string
	serviceReady     string
	currentVersion   string
	availableVersion string
	serviceUrl       string
	autoUpdate       autoUpdateStatus
}

func (middlewareOutdated) middlewareCheck() {}

type autoUpdateStatus struct {
	enabled bool
	source  string // "function" or "operator"
}

func (r *FunctionReconciler) checkMiddlewareState(ctx context.Context, function *v1alpha1.Function, metadata *funcfn.Function) (middlewareCheck, error) {
	desc, err := r.FuncCliManager.Describe(ctx, metadata.Name, function.Namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to describe function: %w", err)
	}

	autoUpdate, err := r.getAutoUpdateStatus(ctx, function)
	if err != nil {
		return nil, fmt.Errorf("failed to check middleware update setting: %w", err)
	}

	latestVersion, err := r.FuncCliManager.GetLatestMiddlewareVersion(ctx, metadata.Runtime, metadata.Invoke)
	if err != nil {
		return nil, fmt.Errorf("failed to get latest middleware version: %w", err)
	}

	if latestVersion == desc.Middleware.Version {
		return middlewareUpToDate{
			currentImage:    desc.Image,
			currentRevision: desc.Revision,
			serviceReady:    desc.Ready,
			serviceUrl:      desc.Route,
			currentVersion:  desc.Middleware.Version,
			autoUpdate:      autoUpdate,
		}, nil
	}

	return middlewareOutdated{
		currentImage:     desc.Image,
		currentRevision:  desc.Revision,
		serviceReady:     desc.Ready,
		serviceUrl:       desc.Route,
		currentVersion:   desc.Middleware.Version,
		availableVersion: latestVersion,
		autoUpdate:       autoUpdate,
	}, nil
}

func (r *FunctionReconciler) getAutoUpdateStatus(ctx context.Context, function *v1alpha1.Function) (autoUpdateStatus, error) {
	enabled, source, err := r.isMiddlewareUpdateEnabled(ctx, function)
	if err != nil {
		return autoUpdateStatus{}, err
	}
	return autoUpdateStatus{enabled: enabled, source: source}, nil
}

// handleMiddlewareUpdate checks if the function is using the latest middleware and redeploys if needed
func (r *FunctionReconciler) handleMiddlewareUpdate(ctx context.Context, function *v1alpha1.Function, repo *git.Repository, metadata *funcfn.Function) error {
	logger := log.FromContext(ctx)

	check, err := r.checkMiddlewareState(ctx, function, metadata)
	if err != nil {
		function.MarkMiddlewareNotUpToDate("MiddlewareCheckFailed", "Failed to check middleware: %s", err)
		return err
	}

	switch check := check.(type) {
	case middlewareUpToDate:
		logger.Info("Function is on latest middleware. No redeploy needed", "version", check.currentVersion)
		function.Status.Deployment.Image = check.currentImage
		function.Status.Deployment.Revision = check.currentRevision
		function.Status.Middleware.Current = check.currentVersion
		function.Status.Middleware.AutoUpdate.Enabled = check.autoUpdate.enabled
		function.Status.Middleware.AutoUpdate.Source = check.autoUpdate.source
		function.Status.Middleware.PendingRebuild = false
		function.Status.Service.URL = check.serviceUrl
		function.Status.Service.Ready = check.serviceReady
		markServiceStatus(check.serviceReady, function)
		function.MarkMiddlewareUpToDate()

	case middlewareOutdated:
		function.Status.Deployment.Image = check.currentImage
		function.Status.Deployment.Revision = check.currentRevision
		function.Status.Middleware.Current = check.currentVersion
		function.Status.Middleware.AutoUpdate.Enabled = check.autoUpdate.enabled
		function.Status.Middleware.AutoUpdate.Source = check.autoUpdate.source
		function.Status.Middleware.Available = ptr.To(check.availableVersion)
		function.Status.Middleware.PendingRebuild = false
		function.Status.Service.URL = check.serviceUrl
		function.Status.Service.Ready = check.serviceReady
		markServiceStatus(check.serviceReady, function)

		if !check.autoUpdate.enabled {
			logger.Info("Skipping middleware update, as middleware update is disabled")
			function.MarkMiddlewareNotUpToDateIntentionally("SkipMiddlewareUpdate", "Skipping middleware update as update is disabled (source: %s)", check.autoUpdate.source)
		} else {
			logger.Info("Middleware outdated, redeploying", "current", check.currentVersion, "available", check.availableVersion)
			function.MarkMiddlewareNotUpToDate("MiddlewareOutdated", "Middleware is outdated (%s available), redeploying...", check.availableVersion)
			function.Status.Middleware.PendingRebuild = true

			if err := FlushStatus(ctx, function); err != nil {
				logger.Error(err, "Failed to update status before redeployment")
			}

			if err := r.deploy(ctx, function, repo); err != nil {
				function.MarkDeployNotReady("DeployFailed", "Redeployment failed: %s", err.Error())
				return fmt.Errorf("failed to redeploy function: %w", err)
			}

			desc, err := r.FuncCliManager.Describe(ctx, metadata.Name, function.Namespace)
			if err != nil {
				return fmt.Errorf("failed to describe function after deploy: %w", err)
			}
			function.Status.Deployment.Image = desc.Image
			function.Status.Middleware.Current = desc.Middleware.Version
			function.Status.Middleware.PendingRebuild = false
			function.Status.Middleware.LastRebuild = metav1.Now()
			function.Status.Deployment.ImageBuilt = metav1.Now()
			function.Status.Middleware.Available = nil
			function.Status.Service.URL = desc.Route
			function.Status.Service.Ready = desc.Ready
			markServiceStatus(desc.Ready, function)

			function.RecordHistoryEvent(fmt.Sprintf("Middleware updated from %q to %q", check.currentVersion, check.availableVersion))
			function.MarkMiddlewareUpToDate()
		}
	}

	function.MarkDeployReady()
	return nil
}

func markServiceStatus(ready string, function *v1alpha1.Function) {
	switch strings.ToLower(ready) {
	case "true":
		function.MarkServiceReady()
	case "false":
		function.MarkServiceNotReady("ServiceNotReady", "Underlying service is not ready")
	default:
		function.MarkServiceNotReady("ServiceReadyUnknown", "Underlying service readiness is unknown")
	}
}

func (r *FunctionReconciler) isDeployed(ctx context.Context, name, namespace string) (bool, error) {
	_, err := r.FuncCliManager.Describe(ctx, name, namespace)
	if err != nil {
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "no describe function") {
			return false, nil
		}

		return false, fmt.Errorf("failed to describe function: %w", err)
	}

	return true, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *FunctionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		// Reconcile Functions on spec changes (generation change) or when
		// "functions.knative.dev/" annotations are present. This predicate is applied
		// to For() instead of WithEventFilter() to ensure it doesn't filter out
		// ConfigMap-triggered reconciliations.
		For(&v1alpha1.Function{}, builder.WithPredicates(predicate.Or(predicate.GenerationChangedPredicate{}, FuncAnnotationChangedPredicate{}))).
		Watches(
			&v1.ConfigMap{},
			handler.EnqueueRequestsFromMapFunc(r.findFunctionsForConfigMap),
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				// Only watch the controller-config ConfigMap in the operator namespace
				return obj.GetName() == controllerConfigName && obj.GetNamespace() == r.OperatorNamespace
			})),
		).
		Named("function").
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 100, // TODO: find a good value
		}).
		Complete(r)
}

// findFunctionsForConfigMap returns reconcile requests for all Functions that should be
// reconciled when the controller-config ConfigMap changes. This triggers reconciliation
// for Functions that rely on the operator-wide default (i.e., those without an explicit
// autoUpdateMiddleware setting).
//
// Note: This function is safe for multi-controller setups. The List() call uses the manager's
// cached client, which is already scoped to the namespaces this controller is watching
// (via WATCH_NAMESPACE env var). Each controller instance only reconciles Functions in its
// own watched namespaces.
func (r *FunctionReconciler) findFunctionsForConfigMap(ctx context.Context, _ client.Object) []reconcile.Request {
	logger := log.FromContext(ctx)

	// List all Functions in the watched namespaces (scoped by the manager's cache)
	functionList := &v1alpha1.FunctionList{}
	if err := r.List(ctx, functionList); err != nil {
		logger.Error(err, "Failed to list Functions for ConfigMap watch")
		return []reconcile.Request{}
	}

	requests := make([]reconcile.Request, 0, len(functionList.Items))
	for _, function := range functionList.Items {
		// Only enqueue Functions that rely on the operator default
		// (i.e., those without an explicit autoUpdateMiddleware setting)
		if function.Spec.AutoUpdateMiddleware == nil {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      function.Name,
					Namespace: function.Namespace,
				},
			})
		}
	}

	logger.Info("Enqueueing Functions for reconciliation due to ConfigMap change", "count", len(requests))
	return requests
}

// isMiddlewareUpdateEnabled returns if the middleware should be updated given by the functions spec or the operators
// default.
func (r *FunctionReconciler) isMiddlewareUpdateEnabled(ctx context.Context, function *v1alpha1.Function) (bool, string, error) {
	logger := log.FromContext(ctx)

	// setting from function overrides operator default
	if function.Spec.AutoUpdateMiddleware != nil {
		return *function.Spec.AutoUpdateMiddleware, "function", nil
	}

	// nothing defined in function spec --> check operator config
	cm := &v1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Namespace: r.OperatorNamespace, Name: controllerConfigName}, cm)
	if err != nil {
		return false, "", fmt.Errorf("failed to get operator config configmap: %w", err)
	}

	val, ok := cm.Data["autoUpdateMiddleware"]
	if !ok {
		logger.Info("No autoUpdateMiddleware field in configmap found. Fallback to hardcoded autoUpdateMiddleware=true")
		// TODO: check if returning an error would be better here
		return true, "operator", nil
	}

	boolVal, err := strconv.ParseBool(val)
	if err != nil {
		return false, "", fmt.Errorf("failed to parse autoUpdateMiddleware value from configmap: %w", err)
	}

	return boolVal, "operator", nil
}
