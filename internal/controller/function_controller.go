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
	"os"
	"strconv"
	"strings"

	"github.com/functions-dev/func-operator/internal/funccli"
	fn "github.com/functions-dev/func-operator/internal/function"
	"github.com/functions-dev/func-operator/internal/git"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
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
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

const (
	deployFunctionRoleName = "func-operator-deploy-function"
	controllerConfigName   = "func-operator-controller-config"
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

	if err := r.ensureDeployment(ctx, function, repo, metadata); err != nil {
		return fmt.Errorf("deploying function failed: %w", err)
	}

	if err := FlushStatus(ctx, function); err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}

	return nil
}

// prepareSource clones the git repository and retrieves function metadata
func (r *FunctionReconciler) prepareSource(ctx context.Context, function *v1alpha1.Function) (*git.Repository, *funcfn.Function, error) {
	branchReference := "main"
	if function.Spec.Repository.Branch != "" {
		branchReference = function.Spec.Repository.Branch
	}

	gitAuthSecret := v1.Secret{}
	if function.Spec.Repository.AuthSecretRef != nil {
		if err := r.Get(ctx, types.NamespacedName{Namespace: function.Namespace, Name: function.Spec.Repository.AuthSecretRef.Name}, &gitAuthSecret); err != nil {
			function.MarkSourceNotReady("AuthSecretNotFound", "Auth secret not found: %s", err.Error())
			return nil, nil, err
		}
	}

	repo, err := r.GitManager.CloneRepository(ctx, function.Spec.Repository.URL, function.Spec.Repository.Path, branchReference, gitAuthSecret.Data)
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

// ensureDeployment ensures the function is deployed and up-to-date
func (r *FunctionReconciler) ensureDeployment(ctx context.Context, function *v1alpha1.Function, repo *git.Repository, metadata *funcfn.Function) error {
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

	// Function is deployed - check middleware version
	return r.handleMiddlewareUpdate(ctx, function, repo, metadata)
}

// handleMiddlewareUpdate checks if the function is using the latest middleware and redeploys if needed
func (r *FunctionReconciler) handleMiddlewareUpdate(ctx context.Context, function *v1alpha1.Function, repo *git.Repository, metadata *funcfn.Function) error {
	logger := log.FromContext(ctx)

	isOnLatestMiddleware, err := r.isMiddlewareLatest(ctx, metadata, function.Namespace)
	if err != nil {
		function.MarkMiddlewareNotUpToDate("MiddlewareCheckFailed", "Failed to check middleware version: %s", err.Error())
		return fmt.Errorf("failed to check if function is using latest middleware: %w", err)
	}

	if !isOnLatestMiddleware {
		isMiddlewareUpdateEnabled, source, err := r.isMiddlewareUpdateEnabled(ctx, function)
		if err != nil {
			function.MarkMiddlewareNotUpToDate("MiddlewareCheckFailed", "Failed to check if middleware should be updated: %s", err)
			return fmt.Errorf("failed to check if middleware should be updated: %w", err)
		}

		if !isMiddlewareUpdateEnabled {
			logger.Info("Skipping middleware update, as middleware update is disabled")
			function.MarkMiddlewareNotUpToDateIntentionally("SkipMiddlewareUpdate", "Skipping middleware update as update is disabled (source: %s)", source)
			// Don't return - continue to update deployment status
		} else {
			logger.Info("Function is not on latest middleware and middleware update is enabled. Will redeploy")
			function.MarkMiddlewareNotUpToDate("MiddlewareOutdated", "Middleware is outdated, redeploying")

			// update function image in status before long redeploy operation
			functionDescribe, err := r.FuncCliManager.Describe(ctx, metadata.Name, function.Namespace)
			if err != nil {
				return fmt.Errorf("failed to describe function to get image details: %w", err)
			}
			function.Status.Deployment.Image = functionDescribe.Image

			// Flush status before long deploy operation
			if err := FlushStatus(ctx, function); err != nil {
				logger.Error(err, "Failed to update status before redeployment")
			}

			if err := r.deploy(ctx, function, repo); err != nil {
				function.MarkDeployNotReady("DeployFailed", "Redeployment failed: %s", err.Error())
				return fmt.Errorf("failed to redeploy function: %w", err)
			}

			// After successful deployment, middleware is now up-to-date
			function.MarkMiddlewareUpToDate()
		}
	} else {
		logger.Info("Function is deployed with latest middleware. No need to redeploy")
		function.MarkMiddlewareUpToDate()
	}

	// Update deployment status
	functionDescribe, err := r.FuncCliManager.Describe(ctx, metadata.Name, function.Namespace)
	if err != nil {
		return fmt.Errorf("failed to describe function to get image details: %w", err)
	}
	function.Status.Deployment.Image = functionDescribe.Image

	function.MarkDeployReady()
	return nil
}

func (r *FunctionReconciler) setupPipelineRBAC(ctx context.Context, function *v1alpha1.Function) error {
	if err := r.ensureDeployFunctionRole(ctx, function.Namespace); err != nil {
		return fmt.Errorf("failed to ensure deploy-function role: %w", err)
	}

	if err := r.ensureDeployFunctionRoleBinding(ctx, function); err != nil {
		return fmt.Errorf("failed to ensure deploy-function role binding: %w", err)
	}

	return nil
}

// ensureDeployFunctionRole ensures the deploy-function Role exists in the namespace and is up-to-date.
// This is a namespace-scoped Role so multiple operator instances won't conflict.
func (r *FunctionReconciler) ensureDeployFunctionRole(ctx context.Context, namespace string) error {
	logger := log.FromContext(ctx)

	// TODO: only add the rules which are needed for the functions deployer
	expectedRole := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deployFunctionRoleName,
			Namespace: namespace,
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"serving.knative.dev"},
				Resources: []string{"services", "routes"},
				Verbs:     []string{"create", "delete", "get", "list", "patch", "update", "watch"},
			}, {
				APIGroups: []string{"eventing.knative.dev"},
				Resources: []string{"triggers"},
				Verbs:     []string{"create", "delete", "get", "list", "patch", "update", "watch"},
			}, {
				APIGroups: []string{"apps"},
				Resources: []string{"deployments", "replicasets"},
				Verbs:     []string{"create", "delete", "get", "list", "patch", "update", "watch"},
			}, {
				APIGroups: []string{""},
				Resources: []string{"services", "pods"},
				Verbs:     []string{"create", "delete", "get", "list", "patch", "update", "watch"},
			}, {
				APIGroups: []string{"http.keda.sh"},
				Resources: []string{"httpscaledobjects"},
				Verbs:     []string{"create", "delete", "get", "list", "patch", "update", "watch"},
			},
		},
	}

	foundRole := &rbacv1.Role{}
	err := r.Get(ctx, types.NamespacedName{Name: expectedRole.Name, Namespace: expectedRole.Namespace}, foundRole)
	if err != nil {
		if apierrors.IsNotFound(err) {
			if err := r.Create(ctx, expectedRole); err != nil {
				return fmt.Errorf("failed to create role: %w", err)
			}
			logger.Info("Created deploy-function role")
			return nil
		}
		return fmt.Errorf("failed to get role: %w", err)
	}

	// Role exists - update if needed
	if !equality.Semantic.DeepEqual(expectedRole.Rules, foundRole.Rules) {
		foundRole.Rules = expectedRole.Rules
		if err := r.Update(ctx, foundRole); err != nil {
			return fmt.Errorf("failed to update role: %w", err)
		}
		logger.Info("Updated deploy-function role")
	} else {
		logger.Info("Deploy-function role already up to date")
	}

	return nil
}

// ensureDeployFunctionRoleBinding ensures the RoleBinding for the deploy-function role exists and is up-to-date.
func (r *FunctionReconciler) ensureDeployFunctionRoleBinding(ctx context.Context, function *v1alpha1.Function) error {
	logger := log.FromContext(ctx)

	expectedRoleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "deploy-function-default",
			Namespace: function.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: v1alpha1.GroupVersion.String(),
					Kind:       "Function",
					Name:       function.Name,
					UID:        function.UID,
					Controller: ptr.To(true),
				},
			},
		},
		Subjects: []rbacv1.Subject{{
			Kind:      "ServiceAccount",
			Name:      "default",
			Namespace: function.Namespace,
		}},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     deployFunctionRoleName,
		},
	}

	foundRoleBinding := &rbacv1.RoleBinding{}
	err := r.Get(ctx, types.NamespacedName{Name: expectedRoleBinding.Name, Namespace: expectedRoleBinding.Namespace}, foundRoleBinding)
	if err != nil {
		if apierrors.IsNotFound(err) {
			if err := r.Create(ctx, expectedRoleBinding); err != nil {
				return fmt.Errorf("failed to create role binding: %w", err)
			}
			logger.Info("Created deploy-function role binding")
			return nil
		}
		return fmt.Errorf("failed to get role binding: %w", err)
	}

	// Update if needed
	if !equality.Semantic.DeepDerivative(expectedRoleBinding, foundRoleBinding) {
		foundRoleBinding.Subjects = expectedRoleBinding.Subjects
		foundRoleBinding.RoleRef = expectedRoleBinding.RoleRef
		foundRoleBinding.OwnerReferences = expectedRoleBinding.OwnerReferences

		if err := r.Update(ctx, foundRoleBinding); err != nil {
			return fmt.Errorf("failed to update role binding: %w", err)
		}
		logger.Info("Updated deploy-function role binding")
	} else {
		logger.Info("Deploy-function role binding already up to date")
	}

	return nil
}

func (r *FunctionReconciler) persistRegistryAuthSecret(ctx context.Context, function *v1alpha1.Function) (string, error) {
	logger := log.FromContext(ctx)

	logger.Info("Persist registry auth secret temporarily")

	authSecret := &v1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Name: function.Spec.Registry.AuthSecretRef.Name, Namespace: function.Namespace}, authSecret)
	if err != nil {
		logger.Error(err, "Failed to get registry auth secret", "secret", function.Spec.Registry.AuthSecretRef.Name, "namespace", function.Namespace)
		return "", fmt.Errorf("failed to get registry auth secret: %w", err)
	}

	if authSecret.Type != v1.SecretTypeDockerConfigJson {
		return "", fmt.Errorf("invalid registry auth secret type, must be of type %s", v1.SecretTypeDockerConfigJson)
	}

	if authSecret.Data[v1.DockerConfigJsonKey] == nil {
		return "", fmt.Errorf("invalid registry auth secret data, must contain key %s", v1.DockerConfigJsonKey)
	}

	// persist secret temporarily
	authFile, err := os.CreateTemp("", "auth-file-*.json")
	if err != nil {
		logger.Error(err, "Failed to create temp auth file")
		return "", fmt.Errorf("failed to create temp auth file: %w", err)
	}
	defer authFile.Close()

	_, err = authFile.Write(authSecret.Data[v1.DockerConfigJsonKey])
	if err != nil {
		logger.Error(err, "Failed to write temp auth file")
		return "", fmt.Errorf("failed to write temp auth file: %w", err)
	}

	return authFile.Name(), nil
}

func (r *FunctionReconciler) deploy(ctx context.Context, function *v1alpha1.Function, repo *git.Repository) error {
	logger := log.FromContext(ctx)

	if err := r.setupPipelineRBAC(ctx, function); err != nil {
		return fmt.Errorf("failed to setup pipeline RBAC: %w", err)
	}

	// deploy function
	deployOptions := funccli.DeployOptions{}

	if function.Spec.Registry.AuthSecretRef != nil && function.Spec.Registry.AuthSecretRef.Name != "" {
		// we have a registry auth secret referenced -> use this for func deploy
		authFile, err := r.persistRegistryAuthSecret(ctx, function)
		if err != nil {
			return fmt.Errorf("failed to persist registry auth secret temporarily: %w", err)
		}

		defer os.Remove(authFile)

		deployOptions.RegistryAuthFile = authFile
	}

	logger.Info("Deploying function", "deployOptions", deployOptions)
	err := r.FuncCliManager.Deploy(ctx, repo.Path(), function.Namespace, deployOptions)
	if err != nil {
		return fmt.Errorf("failed to deploy function: %w", err)
	}

	logger.Info("function deployed successfully")

	return nil
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
		// Only reconcile Functions when their spec changes (not on status updates).
		// This predicate is applied to For() instead of WithEventFilter() to ensure
		// it doesn't filter out ConfigMap-triggered reconciliations.
		For(&v1alpha1.Function{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
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

func (r *FunctionReconciler) isMiddlewareLatest(ctx context.Context, metadata *funcfn.Function, namespace string) (bool, error) {
	latestMiddleware, err := r.FuncCliManager.GetLatestMiddlewareVersion(ctx, metadata.Runtime, metadata.Invoke)
	if err != nil {
		return false, fmt.Errorf("failed to get latest available middleware version: %w", err)
	}

	functionMiddleware, err := r.FuncCliManager.GetMiddlewareVersion(ctx, metadata.Name, namespace)
	if err != nil {
		return false, fmt.Errorf("failed to get middleware version of function: %w", err)
	}

	return latestMiddleware == functionMiddleware, nil
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
