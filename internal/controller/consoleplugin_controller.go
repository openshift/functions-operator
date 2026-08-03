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

	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	consolePluginName       = "console-functions-plugin"
	consolePluginConfigKey  = "consolePluginEnabled"
	consolePluginPort       = int64(9443)
	consolePluginPortInt32  = int32(9443)
	consolePluginBasePath   = "/"
	consolePluginAPIVersion = "console.openshift.io/v1"
	consolePluginKind       = "ConsolePlugin"
	consolePluginCertSecret = "faas-console-plugin-cert"
	consolePluginCertMount  = "/var/cert"

	infrastructureAPIVersion = "config.openshift.io/v1"
	infrastructureKind       = "Infrastructure"
	infrastructureName       = "cluster"
)

// ConsolePluginReconciler reconciles the faas-console-plugin resources
// (ConsolePlugin, Deployment, Service, ServiceAccount) based on the
// operator's controller-config ConfigMap.
type ConsolePluginReconciler struct {
	client.Client
	OperatorNamespace  string
	ConsolePluginImage string
}

func (r *ConsolePluginReconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	enabled, err := r.isConsolePluginEnabled(ctx)
	if err != nil {
		logger.Error(err, "Failed to read console plugin config")
		return ctrl.Result{}, err
	}

	if enabled {
		apiServerURL, err := r.getAPIServerURL(ctx)
		if err != nil {
			logger.Error(err, "Failed to get API server URL from Infrastructure CR")
			return ctrl.Result{}, err
		}

		if err := r.ensureServiceAccount(ctx); err != nil {
			logger.Error(err, "Failed to ensure ServiceAccount")
			return ctrl.Result{}, err
		}
		if err := r.ensureService(ctx); err != nil {
			logger.Error(err, "Failed to ensure Service")
			return ctrl.Result{}, err
		}
		if err := r.ensureDeployment(ctx, apiServerURL); err != nil {
			logger.Error(err, "Failed to ensure Deployment")
			return ctrl.Result{}, err
		}
		if err := r.ensureConsolePlugin(ctx); err != nil {
			logger.Error(err, "Failed to ensure ConsolePlugin")
			return ctrl.Result{}, err
		}
		logger.Info("ConsolePlugin resources are reconciled")
	} else {
		if err := r.deleteConsolePlugin(ctx); err != nil {
			logger.Error(err, "Failed to delete ConsolePlugin")
			return ctrl.Result{}, err
		}
		if err := r.deleteDeployment(ctx); err != nil {
			logger.Error(err, "Failed to delete Deployment")
			return ctrl.Result{}, err
		}
		if err := r.deleteService(ctx); err != nil {
			logger.Error(err, "Failed to delete Service")
			return ctrl.Result{}, err
		}
		if err := r.deleteServiceAccount(ctx); err != nil {
			logger.Error(err, "Failed to delete ServiceAccount")
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

func (r *ConsolePluginReconciler) getAPIServerURL(ctx context.Context) (string, error) {
	infra := &unstructured.Unstructured{}
	infra.SetAPIVersion(infrastructureAPIVersion)
	infra.SetKind(infrastructureKind)

	err := r.Get(ctx, types.NamespacedName{Name: infrastructureName}, infra)
	if err != nil {
		return "", fmt.Errorf("failed to get Infrastructure CR: %w", err)
	}

	status, ok := infra.Object["status"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("Infrastructure CR has no status")
	}

	apiServerURL, ok := status["apiServerURL"].(string)
	if !ok || apiServerURL == "" {
		return "", fmt.Errorf("Infrastructure CR has no apiServerURL in status")
	}

	return apiServerURL, nil
}

func consolePluginLabels() map[string]string {
	return map[string]string{
		"app":                          consolePluginName,
		"app.kubernetes.io/name":       consolePluginName,
		"app.kubernetes.io/part-of":    consolePluginName,
		"app.kubernetes.io/managed-by": "func-operator",
	}
}

func consolePluginSelectorLabels() map[string]string {
	return map[string]string{
		"app":                    consolePluginName,
		"app.kubernetes.io/name": consolePluginName,
	}
}

func (r *ConsolePluginReconciler) buildServiceAccount() *v1.ServiceAccount {
	return &v1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      consolePluginName,
			Namespace: r.OperatorNamespace,
			Labels:    consolePluginLabels(),
		},
	}
}

func (r *ConsolePluginReconciler) buildService() *v1.Service {
	return &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      consolePluginName,
			Namespace: r.OperatorNamespace,
			Labels:    consolePluginLabels(),
			Annotations: map[string]string{
				"service.alpha.openshift.io/serving-cert-secret-name": consolePluginCertSecret,
			},
		},
		Spec: v1.ServiceSpec{
			Type:     v1.ServiceTypeClusterIP,
			Selector: consolePluginSelectorLabels(),
			Ports: []v1.ServicePort{
				{
					Name:       "9443-tcp",
					Protocol:   v1.ProtocolTCP,
					Port:       consolePluginPortInt32,
					TargetPort: intstr.FromInt32(consolePluginPortInt32),
				},
			},
		},
	}
}

func (r *ConsolePluginReconciler) buildDeployment(apiServerURL string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      consolePluginName,
			Namespace: r.OperatorNamespace,
			Labels:    consolePluginLabels(),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(int32(2)),
			Selector: &metav1.LabelSelector{
				MatchLabels: consolePluginSelectorLabels(),
			},
			Strategy: appsv1.DeploymentStrategy{
				Type: appsv1.RollingUpdateDeploymentStrategyType,
				RollingUpdate: &appsv1.RollingUpdateDeployment{
					MaxUnavailable: ptr.To(intstr.FromString("25%")),
					MaxSurge:       ptr.To(intstr.FromString("25%")),
				},
			},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: consolePluginLabels(),
				},
				Spec: v1.PodSpec{
					ServiceAccountName: consolePluginName,
					RestartPolicy:      v1.RestartPolicyAlways,
					DNSPolicy:          v1.DNSClusterFirst,
					SecurityContext: &v1.PodSecurityContext{
						RunAsNonRoot: ptr.To(true),
						SeccompProfile: &v1.SeccompProfile{
							Type: v1.SeccompProfileTypeRuntimeDefault,
						},
					},
					Containers: []v1.Container{
						{
							Name:            consolePluginName,
							Image:           r.ConsolePluginImage,
							ImagePullPolicy: v1.PullIfNotPresent,
							Args: []string{
								"--https-port=9443",
								fmt.Sprintf("--external-api-server-url=%s", apiServerURL),
							},
							Ports: []v1.ContainerPort{
								{
									ContainerPort: consolePluginPortInt32,
									Protocol:      v1.ProtocolTCP,
								},
							},
							Resources: v1.ResourceRequirements{
								Requests: v1.ResourceList{
									v1.ResourceCPU:    *parseQuantity("10m"),
									v1.ResourceMemory: *parseQuantity("50Mi"),
								},
							},
							SecurityContext: &v1.SecurityContext{
								AllowPrivilegeEscalation: ptr.To(false),
								Capabilities: &v1.Capabilities{
									Drop: []v1.Capability{"ALL"},
								},
							},
							VolumeMounts: []v1.VolumeMount{
								{
									Name:      consolePluginCertSecret,
									ReadOnly:  true,
									MountPath: consolePluginCertMount,
								},
							},
						},
					},
					Volumes: []v1.Volume{
						{
							Name: consolePluginCertSecret,
							VolumeSource: v1.VolumeSource{
								Secret: &v1.SecretVolumeSource{
									SecretName:  consolePluginCertSecret,
									DefaultMode: ptr.To(int32(420)),
								},
							},
						},
					},
				},
			},
		},
	}
}

func (r *ConsolePluginReconciler) buildConsolePlugin() *unstructured.Unstructured {
	cp := &unstructured.Unstructured{}
	cp.SetAPIVersion(consolePluginAPIVersion)
	cp.SetKind(consolePluginKind)
	cp.SetName(consolePluginName)
	cp.SetLabels(consolePluginLabels())

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

func (r *ConsolePluginReconciler) ensureServiceAccount(ctx context.Context) error {
	logger := log.FromContext(ctx)
	desired := r.buildServiceAccount()

	existing := &v1.ServiceAccount{}
	err := r.Get(ctx, types.NamespacedName{Name: consolePluginName, Namespace: r.OperatorNamespace}, existing)
	if apierrors.IsNotFound(err) {
		logger.Info("Creating ServiceAccount", "name", consolePluginName)
		return r.Create(ctx, desired)
	}
	if err != nil {
		return fmt.Errorf("failed to get ServiceAccount: %w", err)
	}

	existing.Labels = desired.Labels
	logger.Info("Updating ServiceAccount", "name", consolePluginName)
	return r.Update(ctx, existing)
}

func (r *ConsolePluginReconciler) ensureService(ctx context.Context) error {
	logger := log.FromContext(ctx)
	desired := r.buildService()

	existing := &v1.Service{}
	err := r.Get(ctx, types.NamespacedName{Name: consolePluginName, Namespace: r.OperatorNamespace}, existing)
	if apierrors.IsNotFound(err) {
		logger.Info("Creating Service", "name", consolePluginName)
		return r.Create(ctx, desired)
	}
	if err != nil {
		return fmt.Errorf("failed to get Service: %w", err)
	}

	existing.Labels = desired.Labels
	existing.Annotations = desired.Annotations
	existing.Spec.Ports = desired.Spec.Ports
	existing.Spec.Selector = desired.Spec.Selector
	existing.Spec.Type = desired.Spec.Type
	logger.Info("Updating Service", "name", consolePluginName)
	return r.Update(ctx, existing)
}

func (r *ConsolePluginReconciler) ensureDeployment(ctx context.Context, apiServerURL string) error {
	logger := log.FromContext(ctx)
	desired := r.buildDeployment(apiServerURL)

	existing := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: consolePluginName, Namespace: r.OperatorNamespace}, existing)
	if apierrors.IsNotFound(err) {
		logger.Info("Creating Deployment", "name", consolePluginName)
		return r.Create(ctx, desired)
	}
	if err != nil {
		return fmt.Errorf("failed to get Deployment: %w", err)
	}

	existing.Labels = desired.Labels
	existing.Spec = desired.Spec
	logger.Info("Updating Deployment", "name", consolePluginName)
	return r.Update(ctx, existing)
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

func (r *ConsolePluginReconciler) deleteDeployment(ctx context.Context) error {
	logger := log.FromContext(ctx)

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      consolePluginName,
			Namespace: r.OperatorNamespace,
		},
	}

	err := r.Delete(ctx, deploy)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to delete Deployment: %w", err)
	}

	logger.Info("Deleted Deployment", "name", consolePluginName)
	return nil
}

func (r *ConsolePluginReconciler) deleteService(ctx context.Context) error {
	logger := log.FromContext(ctx)

	svc := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      consolePluginName,
			Namespace: r.OperatorNamespace,
		},
	}

	err := r.Delete(ctx, svc)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to delete Service: %w", err)
	}

	logger.Info("Deleted Service", "name", consolePluginName)
	return nil
}

func (r *ConsolePluginReconciler) deleteServiceAccount(ctx context.Context) error {
	logger := log.FromContext(ctx)

	sa := &v1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      consolePluginName,
			Namespace: r.OperatorNamespace,
		},
	}

	err := r.Delete(ctx, sa)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to delete ServiceAccount: %w", err)
	}

	logger.Info("Deleted ServiceAccount", "name", consolePluginName)
	return nil
}

func (r *ConsolePluginReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.ConsolePluginImage == "" {
		log.Log.Info("CONSOLE_PLUGIN_IMAGE not set, skipping console plugin controller setup")
		return nil
	}

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
			handler.EnqueueRequestsFromMapFunc(r.mapToRequest),
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				return obj.GetName() == consolePluginName
			})),
		).
		Watches(
			&v1.ConfigMap{},
			handler.EnqueueRequestsFromMapFunc(r.mapToRequest),
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				return obj.GetName() == controllerConfigName && obj.GetNamespace() == r.OperatorNamespace
			})),
		).
		Watches(
			&appsv1.Deployment{},
			handler.EnqueueRequestsFromMapFunc(r.mapToRequest),
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				return obj.GetName() == consolePluginName && obj.GetNamespace() == r.OperatorNamespace
			})),
		).
		Watches(
			&v1.Service{},
			handler.EnqueueRequestsFromMapFunc(r.mapToRequest),
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				return obj.GetName() == consolePluginName && obj.GetNamespace() == r.OperatorNamespace
			})),
		).
		Watches(
			&v1.ServiceAccount{},
			handler.EnqueueRequestsFromMapFunc(r.mapToRequest),
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				return obj.GetName() == consolePluginName && obj.GetNamespace() == r.OperatorNamespace
			})),
		).
		Complete(r)
}

func (r *ConsolePluginReconciler) mapToRequest(_ context.Context, _ client.Object) []reconcile.Request {
	return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: consolePluginName}}}
}

func parseQuantity(s string) *resource.Quantity {
	q := resource.MustParse(s)
	return &q
}
