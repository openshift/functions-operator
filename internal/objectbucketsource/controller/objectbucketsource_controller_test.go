/*
Copyright 2026.

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
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	sourcesv1alpha1 "github.com/functions-dev/func-operator/api/sources/v1alpha1"
	"github.com/functions-dev/func-operator/internal/objectbucketsource/config"
)

var _ = Describe("ObjectBucketSource Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}
		objectBucketSource := &sourcesv1alpha1.ObjectBucketSource{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind ObjectBucketSource")
			err := k8sClient.Get(ctx, typeNamespacedName, objectBucketSource)
			if err != nil && errors.IsNotFound(err) {
				resource := &sourcesv1alpha1.ObjectBucketSource{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: sourcesv1alpha1.ObjectBucketSourceSpec{
						ObjectBucketClaim: sourcesv1alpha1.OBCReference{
							Name: "test-obc",
						},
						Events: []string{"s3:ObjectCreated:*"},
						Sink: sourcesv1alpha1.SinkSpec{
							URI: "http://localhost:8080",
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &sourcesv1alpha1.ObjectBucketSource{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance ObjectBucketSource")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &ObjectBucketSourceReconciler{
				Client:         k8sClient,
				Scheme:         k8sClient.Scheme(),
				ConfigProvider: config.NewMockProvider(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
