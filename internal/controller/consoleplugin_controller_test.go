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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ = Describe("ConsolePlugin Controller", func() {
	const operatorNamespace = "test-consoleplugin-ns"

	var (
		reconciler *ConsolePluginReconciler
		ctx        context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()

		ns := &v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: operatorNamespace},
		}
		err := k8sClient.Create(ctx, ns)
		if err != nil && !apierrors.IsAlreadyExists(err) {
			Expect(err).NotTo(HaveOccurred())
		}

		reconciler = &ConsolePluginReconciler{
			Client:            k8sClient,
			OperatorNamespace: operatorNamespace,
		}
	})

	AfterEach(func() {
		cm := &v1.ConfigMap{}
		err := k8sClient.Get(ctx, types.NamespacedName{
			Name:      controllerConfigName,
			Namespace: operatorNamespace,
		}, cm)
		if err == nil {
			_ = k8sClient.Delete(ctx, cm)
		}

		cp := &unstructured.Unstructured{}
		cp.SetAPIVersion(consolePluginAPIVersion)
		cp.SetKind(consolePluginKind)
		cp.SetName(consolePluginName)
		_ = k8sClient.Delete(ctx, cp)
	})

	createConfigMap := func(data map[string]string) {
		cm := &v1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      controllerConfigName,
				Namespace: operatorNamespace,
			},
			Data: data,
		}
		Expect(k8sClient.Create(ctx, cm)).To(Succeed())
	}

	getConsolePlugin := func() (*unstructured.Unstructured, error) {
		cp := &unstructured.Unstructured{}
		cp.SetAPIVersion(consolePluginAPIVersion)
		cp.SetKind(consolePluginKind)
		err := k8sClient.Get(ctx, types.NamespacedName{Name: consolePluginName}, cp)
		return cp, err
	}

	Context("isConsolePluginEnabled", func() {
		It("should return false when key is missing from configmap", func() {
			createConfigMap(map[string]string{"autoUpdateMiddleware": "true"})
			enabled, err := reconciler.isConsolePluginEnabled(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(enabled).To(BeFalse())
		})

		It("should return true when consolePluginEnabled is true", func() {
			createConfigMap(map[string]string{consolePluginConfigKey: "true"})
			enabled, err := reconciler.isConsolePluginEnabled(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(enabled).To(BeTrue())
		})

		It("should return false when consolePluginEnabled is false", func() {
			createConfigMap(map[string]string{consolePluginConfigKey: "false"})
			enabled, err := reconciler.isConsolePluginEnabled(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(enabled).To(BeFalse())
		})

		It("should return error when configmap does not exist", func() {
			_, err := reconciler.isConsolePluginEnabled(ctx)
			Expect(err).To(HaveOccurred())
		})

		It("should return error when value is not a valid bool", func() {
			createConfigMap(map[string]string{consolePluginConfigKey: "notabool"})
			_, err := reconciler.isConsolePluginEnabled(ctx)
			Expect(err).To(HaveOccurred())
		})
	})

	Context("buildConsolePlugin", func() {
		It("should build a ConsolePlugin with correct structure", func() {
			cp := reconciler.buildConsolePlugin()
			Expect(cp.GetAPIVersion()).To(Equal(consolePluginAPIVersion))
			Expect(cp.GetKind()).To(Equal(consolePluginKind))
			Expect(cp.GetName()).To(Equal(consolePluginName))

			labels := cp.GetLabels()
			Expect(labels["app"]).To(Equal(consolePluginName))
			Expect(labels["app.kubernetes.io/managed-by"]).To(Equal("func-operator"))

			spec, ok := cp.Object["spec"].(map[string]interface{})
			Expect(ok).To(BeTrue())

			backend, ok := spec["backend"].(map[string]interface{})
			Expect(ok).To(BeTrue())
			Expect(backend["type"]).To(Equal("Service"))

			svc, ok := backend["service"].(map[string]interface{})
			Expect(ok).To(BeTrue())
			Expect(svc["name"]).To(Equal(consolePluginName))
			Expect(svc["namespace"]).To(Equal(operatorNamespace))
			Expect(svc["port"]).To(Equal(consolePluginPort))
		})
	})

	Context("Reconcile", func() {
		It("should create ConsolePlugin when enabled", func() {
			createConfigMap(map[string]string{consolePluginConfigKey: "true"})

			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: consolePluginName},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			cp, err := getConsolePlugin()
			Expect(err).NotTo(HaveOccurred())
			Expect(cp.GetName()).To(Equal(consolePluginName))
		})

		It("should delete ConsolePlugin when disabled", func() {
			createConfigMap(map[string]string{consolePluginConfigKey: "true"})

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: consolePluginName},
			})
			Expect(err).NotTo(HaveOccurred())

			_, err = getConsolePlugin()
			Expect(err).NotTo(HaveOccurred())

			cm := &v1.ConfigMap{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      controllerConfigName,
				Namespace: operatorNamespace,
			}, cm)).To(Succeed())
			cm.Data[consolePluginConfigKey] = "false"
			Expect(k8sClient.Update(ctx, cm)).To(Succeed())

			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: consolePluginName},
			})
			Expect(err).NotTo(HaveOccurred())

			_, err = getConsolePlugin()
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})

		It("should not error when deleting non-existent ConsolePlugin", func() {
			createConfigMap(map[string]string{consolePluginConfigKey: "false"})

			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: consolePluginName},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))
		})

		It("should update existing ConsolePlugin when reconciled again", func() {
			createConfigMap(map[string]string{consolePluginConfigKey: "true"})

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: consolePluginName},
			})
			Expect(err).NotTo(HaveOccurred())

			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: consolePluginName},
			})
			Expect(err).NotTo(HaveOccurred())

			cp, err := getConsolePlugin()
			Expect(err).NotTo(HaveOccurred())
			Expect(cp.GetName()).To(Equal(consolePluginName))
		})

		It("should not create ConsolePlugin when key is absent", func() {
			createConfigMap(map[string]string{"autoUpdateMiddleware": "true"})

			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: consolePluginName},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			_, err = getConsolePlugin()
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})
	})
})
