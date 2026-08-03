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

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	consolePluginName       = "faas-console-plugin"
	consolePluginConfigKey  = "consolePluginEnabled"
	consolePluginPort       = int64(9443)
	consolePluginBasePath   = "/"
	consolePluginAPIVersion = "console.openshift.io/v1"
	consolePluginKind       = "ConsolePlugin"
)

var consolePluginGVR = schema.GroupVersionResource{
	Group:    "console.openshift.io",
	Version:  "v1",
	Resource: "consoleplugins",
}

// ConsolePluginReconciler reconciles the faas-console-plugin ConsolePlugin resource
// based on the operator's controller-config ConfigMap.
type ConsolePluginReconciler struct {
	client.Client
	OperatorNamespace string
}

func (r *ConsolePluginReconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	enabled, err := r.isConsolePluginEnabled(ctx)
	if err != nil {
		logger.Error(err, "Failed to read console plugin config")
		return ctrl.Result{}, err
	}

	if enabled {
		if err := r.ensureConsolePlugin(ctx); err != nil {
			logger.Error(err, "Failed to ensure ConsolePlugin")
			return ctrl.Result{}, err
		}
		logger.Info("ConsolePlugin is reconciled")
	} else {
		if err := r.deleteConsolePlugin(ctx); err != nil {
			logger.Error(err, "Failed to delete ConsolePlugin")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *ConsolePluginReconciler) isConsolePluginEnabled(ctx context.Context) (bool, error) {
	cm := &v1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Namespace: r.OperatorNamespace, Name: controllerConfigName}, cm)
	if err != nil {
		return false, fmt.Errorf("failed to get operator config configmap: %w", err)
	}

	val, ok := cm.Data[consolePluginConfigKey]
	if !ok {
		return false, nil
	}

	boolVal, err := strconv.ParseBool(val)
	if err != nil {
		return false, fmt.Errorf("failed to parse %s value from configmap: %w", consolePluginConfigKey, err)
	}

	return boolVal, nil
}

func (r *ConsolePluginReconciler) buildConsolePlugin() *unstructured.Unstructured {
	cp := &unstructured.Unstructured{}
	cp.SetAPIVersion(consolePluginAPIVersion)
	cp.SetKind(consolePluginKind)
	cp.SetName(consolePluginName)
	cp.SetLabels(map[string]string{
		"app":                          consolePluginName,
		"app.kubernetes.io/name":       consolePluginName,
		"app.kubernetes.io/part-of":    consolePluginName,
		"app.kubernetes.io/managed-by": "func-operator",
	})

	cp.Object["spec"] = map[string]interface{}{
		"displayName": "OpenShift Serverless Functions Console Plugin",
		"i18n": map[string]interface{}{
			"loadType": "Preload",
		},
		"backend": map[string]interface{}{
			"type": "Service",
			"service": map[string]interface{}{
				"name":      consolePluginName,
				"namespace": r.OperatorNamespace,
				"port":      consolePluginPort,
				"basePath":  consolePluginBasePath,
			},
		},
		"proxy": []interface{}{
			map[string]interface{}{
				"alias": "backend",
				"endpoint": map[string]interface{}{
					"type": "Service",
					"service": map[string]interface{}{
						"name":      consolePluginName,
						"namespace": r.OperatorNamespace,
						"port":      consolePluginPort,
					},
				},
			},
		},
	}

	return cp
}

func (r *ConsolePluginReconciler) ensureConsolePlugin(ctx context.Context) error {
	logger := log.FromContext(ctx)
	desired := r.buildConsolePlugin()

	existing := &unstructured.Unstructured{}
	existing.SetAPIVersion(consolePluginAPIVersion)
	existing.SetKind(consolePluginKind)

	err := r.Get(ctx, types.NamespacedName{Name: consolePluginName}, existing)
	if apierrors.IsNotFound(err) {
		logger.Info("Creating ConsolePlugin", "name", consolePluginName)
		return r.Create(ctx, desired)
	}
	if err != nil {
		return fmt.Errorf("failed to get ConsolePlugin: %w", err)
	}

	existing.Object["spec"] = desired.Object["spec"]
	existing.SetLabels(desired.GetLabels())
	logger.Info("Updating ConsolePlugin", "name", consolePluginName)
	return r.Update(ctx, existing)
}

func (r *ConsolePluginReconciler) deleteConsolePlugin(ctx context.Context) error {
	logger := log.FromContext(ctx)

	cp := &unstructured.Unstructured{}
	cp.SetAPIVersion(consolePluginAPIVersion)
	cp.SetKind(consolePluginKind)
	cp.SetName(consolePluginName)

	err := r.Delete(ctx, cp)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to delete ConsolePlugin: %w", err)
	}

	logger.Info("Deleted ConsolePlugin", "name", consolePluginName)
	return nil
}

func (r *ConsolePluginReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Check if the ConsolePlugin CRD exists before setting up the controller
	_, err := mgr.GetRESTMapper().RESTMapping(
		schema.GroupKind{Group: "console.openshift.io", Kind: consolePluginKind},
		"v1",
	)
	if err != nil {
		log.Log.Info("ConsolePlugin CRD not found, skipping console plugin controller setup")
		return nil
	}

	return ctrl.NewControllerManagedBy(mgr).
		Named("consoleplugin").
		WatchesMetadata(
			&metav1.PartialObjectMetadata{
				TypeMeta: metav1.TypeMeta{
					APIVersion: consolePluginAPIVersion,
					Kind:       consolePluginKind,
				},
			},
			handler.EnqueueRequestsFromMapFunc(r.mapConsolePluginToRequest),
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				return obj.GetName() == consolePluginName
			})),
		).
		Watches(
			&v1.ConfigMap{},
			handler.EnqueueRequestsFromMapFunc(r.mapConfigMapToRequest),
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				return obj.GetName() == controllerConfigName && obj.GetNamespace() == r.OperatorNamespace
			})),
		).
		Complete(r)
}

func (r *ConsolePluginReconciler) mapConsolePluginToRequest(_ context.Context, obj client.Object) []reconcile.Request {
	if obj.GetName() != consolePluginName {
		return nil
	}
	return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: consolePluginName}}}
}

func (r *ConsolePluginReconciler) mapConfigMapToRequest(_ context.Context, _ client.Object) []reconcile.Request {
	return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: consolePluginName}}}
}
