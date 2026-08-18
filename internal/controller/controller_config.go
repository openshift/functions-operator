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

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// defaultAutoUpdateMiddleware mirrors the value shipped in
// config/manager/manager.yaml. It is used when creating the controller-config
// ConfigMap for deployments that do not provide one (e.g. OLM bundles that omit
// the default ConfigMap).
const defaultAutoUpdateMiddleware = "true"

// EnsureDefaultConfigMap creates the controller-config ConfigMap with default
// values if it does not already exist. It is a no-op when the ConfigMap is
// already present. This allows the operator to be deployed via OLM, where the
// default ConfigMap is not part of the bundle.
func EnsureDefaultConfigMap(ctx context.Context, namespace string) error {
	clientset, err := kubernetes.NewForConfig(ctrl.GetConfigOrDie())
	if err != nil {
		return fmt.Errorf("creating kubernetes clientset: %w", err)
	}

	_, err = clientset.CoreV1().ConfigMaps(namespace).Get(ctx, controllerConfigName, metav1.GetOptions{})
	if err == nil {
		log.Log.Info("controller-config ConfigMap already exists, not creating",
			"name", controllerConfigName, "namespace", namespace)
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("checking for ConfigMap %s/%s: %w", namespace, controllerConfigName, err)
	}

	cm := &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      controllerConfigName,
			Namespace: namespace,
		},
		Data: map[string]string{
			"autoUpdateMiddleware": defaultAutoUpdateMiddleware,
			consolePluginConfigKey: "true",
		},
	}

	if _, err := clientset.CoreV1().ConfigMaps(namespace).Create(ctx, cm, metav1.CreateOptions{}); err != nil {
		if apierrors.IsAlreadyExists(err) {
			// Another replica created it concurrently; treat as success.
			log.Log.Info("controller-config ConfigMap already created concurrently",
				"name", controllerConfigName, "namespace", namespace)
			return nil
		}
		return fmt.Errorf("creating default ConfigMap %s/%s: %w", namespace, controllerConfigName, err)
	}

	log.Log.Info("created default controller-config ConfigMap",
		"name", controllerConfigName, "namespace", namespace)
	return nil
}
