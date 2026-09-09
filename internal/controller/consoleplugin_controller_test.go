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
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ = Describe("ConsolePlugin Controller", func() {
	const (
		operatorNamespace = "test-consoleplugin-ns"
		testImage         = "quay.io/test/faas-console-plugin:latest"
		testAPIServerURL  = "https://api.test-cluster.example.com:6443"
	)

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
			Client:             k8sClient,
			OperatorNamespace:  operatorNamespace,
			ConsolePluginImage: testImage,
		}

		// Create Infrastructure CR for API server URL
		infra := &unstructured.Unstructured{}
		infra.SetAPIVersion(infrastructureAPIVersion)
		infra.SetKind(infrastructureKind)
		infra.SetName(infrastructureName)
		infra.Object["status"] = map[string]interface{}{
			"apiServerURL": testAPIServerURL,
		}
		err = k8sClient.Create(ctx, infra)
		if err != nil && !apierrors.IsAlreadyExists(err) {
			Expect(err).NotTo(HaveOccurred())
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

		deploy := &appsv1.Deployment{}
		if k8sClient.Get(ctx, types.NamespacedName{Name: consolePluginName, Namespace: operatorNamespace}, deploy) == nil {
			_ = k8sClient.Delete(ctx, deploy)
		}

		svc := &v1.Service{}
		if k8sClient.Get(ctx, types.NamespacedName{Name: consolePluginName, Namespace: operatorNamespace}, svc) == nil {
			_ = k8sClient.Delete(ctx, svc)
		}

		sa := &v1.ServiceAccount{}
		if k8sClient.Get(ctx, types.NamespacedName{Name: consolePluginName, Namespace: operatorNamespace}, sa) == nil {
			_ = k8sClient.Delete(ctx, sa)
		}
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

	getDeployment := func() (*appsv1.Deployment, error) {
		deploy := &appsv1.Deployment{}
		err := k8sClient.Get(ctx, types.NamespacedName{Name: consolePluginName, Namespace: operatorNamespace}, deploy)
		return deploy, err
	}

	getService := func() (*v1.Service, error) {
		svc := &v1.Service{}
		err := k8sClient.Get(ctx, types.NamespacedName{Name: consolePluginName, Namespace: operatorNamespace}, svc)
		return svc, err
	}

	getServiceAccount := func() (*v1.ServiceAccount, error) {
		sa := &v1.ServiceAccount{}
		err := k8sClient.Get(ctx, types.NamespacedName{Name: consolePluginName, Namespace: operatorNamespace}, sa)
		return sa, err
	}

	doReconcile := func() (reconcile.Result, error) {
		return reconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: consolePluginName},
		})
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

	Context("getAPIServerURL", func() {
		It("should return the API server URL from Infrastructure CR", func() {
			url, err := reconciler.getAPIServerURL(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(url).To(Equal(testAPIServerURL))
		})
	})

	Context("getGHApiURL", func() {
		It("should return ghApiUrl from ConfigMap when set", func() {
			createConfigMap(map[string]string{
				consolePluginConfigKey: "true",
				"ghApiUrl":             "https://github.example.com/api/v3",
			})
			url := reconciler.getGHApiURL(ctx)
			Expect(url).To(Equal("https://github.example.com/api/v3"))
		})

		It("should fall back to GH_API_URL env var when not in ConfigMap", func() {
			createConfigMap(map[string]string{consolePluginConfigKey: "true"})
			GinkgoT().Setenv("GH_API_URL", "https://env-gh.example.com/api")
			url := reconciler.getGHApiURL(ctx)
			Expect(url).To(Equal("https://env-gh.example.com/api"))
		})

		It("should return empty string when not in ConfigMap and env var is unset", func() {
			createConfigMap(map[string]string{consolePluginConfigKey: "true"})
			GinkgoT().Setenv("GH_API_URL", "")
			url := reconciler.getGHApiURL(ctx)
			Expect(url).To(BeEmpty())
		})

		It("should return empty string when ConfigMap does not exist", func() {
			GinkgoT().Setenv("GH_API_URL", "")
			url := reconciler.getGHApiURL(ctx)
			Expect(url).To(BeEmpty())
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

		It("should include authorization UserToken in proxy", func() {
			cp := reconciler.buildConsolePlugin()
			spec, ok := cp.Object["spec"].(map[string]interface{})
			Expect(ok).To(BeTrue())

			proxy, ok := spec["proxy"].([]interface{})
			Expect(ok).To(BeTrue())
			Expect(proxy).To(HaveLen(1))

			proxyEntry, ok := proxy[0].(map[string]interface{})
			Expect(ok).To(BeTrue())
			Expect(proxyEntry["alias"]).To(Equal("backend"))
			Expect(proxyEntry["authorization"]).To(Equal("UserToken"))
		})
	})

	Context("buildDeployment", func() {
		It("should build a Deployment with the correct image and args", func() {
			deploy := reconciler.buildDeployment(testAPIServerURL, "")
			Expect(deploy.Name).To(Equal(consolePluginName))
			Expect(deploy.Namespace).To(Equal(operatorNamespace))
			Expect(*deploy.Spec.Replicas).To(Equal(int32(2)))

			container := deploy.Spec.Template.Spec.Containers[0]
			Expect(container.Name).To(Equal(consolePluginName))
			Expect(container.Image).To(Equal(testImage))
			Expect(container.Args).To(ContainElement("--https-port=9443"))
			Expect(container.Args).To(ContainElement("--external-api-server-url=" + testAPIServerURL))
			Expect(container.Args).NotTo(ContainElement(ContainSubstring("--gh-api-url")))

			Expect(deploy.Spec.Template.Spec.ServiceAccountName).To(Equal(consolePluginName))
			Expect(deploy.Spec.Template.Spec.Volumes[0].Secret.SecretName).To(Equal(consolePluginCertSecret))
		})

		It("should include liveness and readiness probes", func() {
			deploy := reconciler.buildDeployment(testAPIServerURL, "")
			container := deploy.Spec.Template.Spec.Containers[0]

			expectedPort := intstr.FromInt32(consolePluginPortInt32)

			Expect(container.LivenessProbe).NotTo(BeNil())
			Expect(container.LivenessProbe.HTTPGet).NotTo(BeNil())
			Expect(container.LivenessProbe.HTTPGet.Path).To(Equal("/healthz"))
			Expect(container.LivenessProbe.HTTPGet.Port).To(Equal(expectedPort))
			Expect(container.LivenessProbe.HTTPGet.Scheme).To(Equal(v1.URISchemeHTTPS))
			Expect(container.LivenessProbe.InitialDelaySeconds).To(Equal(int32(5)))
			Expect(container.LivenessProbe.PeriodSeconds).To(Equal(int32(10)))

			Expect(container.ReadinessProbe).NotTo(BeNil())
			Expect(container.ReadinessProbe.HTTPGet).NotTo(BeNil())
			Expect(container.ReadinessProbe.HTTPGet.Path).To(Equal("/healthz"))
			Expect(container.ReadinessProbe.HTTPGet.Port).To(Equal(expectedPort))
			Expect(container.ReadinessProbe.HTTPGet.Scheme).To(Equal(v1.URISchemeHTTPS))
			Expect(container.ReadinessProbe.InitialDelaySeconds).To(Equal(int32(5)))
			Expect(container.ReadinessProbe.PeriodSeconds).To(Equal(int32(10)))
		})

		It("should include --gh-api-url arg when ghApiURL is provided", func() {
			ghURL := "https://github.example.com/api/v3"
			deploy := reconciler.buildDeployment(testAPIServerURL, ghURL)
			container := deploy.Spec.Template.Spec.Containers[0]
			Expect(container.Args).To(ContainElement("--gh-api-url=" + ghURL))
		})

		It("should not include --gh-api-url arg when ghApiURL is empty", func() {
			deploy := reconciler.buildDeployment(testAPIServerURL, "")
			container := deploy.Spec.Template.Spec.Containers[0]
			Expect(container.Args).NotTo(ContainElement(ContainSubstring("--gh-api-url")))
		})
	})

	Context("buildService", func() {
		It("should build a Service with serving-cert annotation", func() {
			svc := reconciler.buildService()
			Expect(svc.Name).To(Equal(consolePluginName))
			Expect(svc.Namespace).To(Equal(operatorNamespace))
			Expect(svc.Annotations["service.alpha.openshift.io/serving-cert-secret-name"]).To(Equal(consolePluginCertSecret))
			Expect(svc.Spec.Ports[0].Port).To(Equal(consolePluginPortInt32))
			Expect(svc.Spec.Type).To(Equal(v1.ServiceTypeClusterIP))
		})
	})

	Context("buildServiceAccount", func() {
		It("should build a ServiceAccount with correct labels", func() {
			sa := reconciler.buildServiceAccount()
			Expect(sa.Name).To(Equal(consolePluginName))
			Expect(sa.Namespace).To(Equal(operatorNamespace))
			Expect(sa.Labels["app.kubernetes.io/managed-by"]).To(Equal("func-operator"))
		})
	})

	Context("Reconcile", func() {
		It("should create all resources when enabled", func() {
			createConfigMap(map[string]string{consolePluginConfigKey: "true"})

			result, err := doReconcile()
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			_, err = getConsolePlugin()
			Expect(err).NotTo(HaveOccurred())

			deploy, err := getDeployment()
			Expect(err).NotTo(HaveOccurred())
			Expect(deploy.Spec.Template.Spec.Containers[0].Image).To(Equal(testImage))
			Expect(deploy.Spec.Template.Spec.Containers[0].Args).To(ContainElement("--external-api-server-url=" + testAPIServerURL))

			svc, err := getService()
			Expect(err).NotTo(HaveOccurred())
			Expect(svc.Annotations["service.alpha.openshift.io/serving-cert-secret-name"]).To(Equal(consolePluginCertSecret))

			_, err = getServiceAccount()
			Expect(err).NotTo(HaveOccurred())
		})

		It("should delete all resources when disabled", func() {
			createConfigMap(map[string]string{consolePluginConfigKey: "true"})

			_, err := doReconcile()
			Expect(err).NotTo(HaveOccurred())

			cm := &v1.ConfigMap{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      controllerConfigName,
				Namespace: operatorNamespace,
			}, cm)).To(Succeed())
			cm.Data[consolePluginConfigKey] = "false"
			Expect(k8sClient.Update(ctx, cm)).To(Succeed())

			_, err = doReconcile()
			Expect(err).NotTo(HaveOccurred())

			_, err = getConsolePlugin()
			Expect(apierrors.IsNotFound(err)).To(BeTrue())

			_, err = getDeployment()
			Expect(apierrors.IsNotFound(err)).To(BeTrue())

			_, err = getService()
			Expect(apierrors.IsNotFound(err)).To(BeTrue())

			_, err = getServiceAccount()
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})

		It("should not error when deleting non-existent resources", func() {
			createConfigMap(map[string]string{consolePluginConfigKey: "false"})

			result, err := doReconcile()
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))
		})

		It("should update existing resources when reconciled again", func() {
			createConfigMap(map[string]string{consolePluginConfigKey: "true"})

			_, err := doReconcile()
			Expect(err).NotTo(HaveOccurred())

			_, err = doReconcile()
			Expect(err).NotTo(HaveOccurred())

			cp, err := getConsolePlugin()
			Expect(err).NotTo(HaveOccurred())
			Expect(cp.GetName()).To(Equal(consolePluginName))

			deploy, err := getDeployment()
			Expect(err).NotTo(HaveOccurred())
			Expect(deploy.Spec.Template.Spec.Containers[0].Image).To(Equal(testImage))
		})

		It("should pass --gh-api-url to deployment when ghApiUrl is in ConfigMap", func() {
			createConfigMap(map[string]string{
				consolePluginConfigKey: "true",
				"ghApiUrl":             "https://gh.example.com/api/v3",
			})

			result, err := doReconcile()
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			deploy, err := getDeployment()
			Expect(err).NotTo(HaveOccurred())
			Expect(deploy.Spec.Template.Spec.Containers[0].Args).To(ContainElement("--gh-api-url=https://gh.example.com/api/v3"))
		})

		It("should not pass --gh-api-url when ghApiUrl is not set", func() {
			createConfigMap(map[string]string{consolePluginConfigKey: "true"})
			GinkgoT().Setenv("GH_API_URL", "")

			result, err := doReconcile()
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			deploy, err := getDeployment()
			Expect(err).NotTo(HaveOccurred())
			Expect(deploy.Spec.Template.Spec.Containers[0].Args).NotTo(ContainElement(ContainSubstring("--gh-api-url")))
		})

		It("should not create resources when key is absent", func() {
			createConfigMap(map[string]string{"autoUpdateMiddleware": "true"})

			result, err := doReconcile()
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(reconcile.Result{}))

			_, err = getConsolePlugin()
			Expect(apierrors.IsNotFound(err)).To(BeTrue())

			_, err = getDeployment()
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})
	})
})
