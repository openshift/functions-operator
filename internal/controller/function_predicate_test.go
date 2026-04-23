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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/functions-dev/func-operator/api/v1alpha1"
)

var _ = Describe("FuncAnnotationChangedPredicate", func() {
	var p FuncAnnotationChangedPredicate

	BeforeEach(func() {
		p = FuncAnnotationChangedPredicate{}
	})

	Context("Update", func() {
		It("should trigger when func annotation is present", func() {
			result := p.Update(event.UpdateEvent{
				ObjectOld: &v1alpha1.Function{
					ObjectMeta: metav1.ObjectMeta{Generation: 1},
				},
				ObjectNew: &v1alpha1.Function{
					ObjectMeta: metav1.ObjectMeta{
						Generation: 1,
						Annotations: map[string]string{
							"functions.knative.dev/rebuild": "true",
						},
					},
				},
			})
			Expect(result).To(BeTrue())
		})

		It("should trigger when multiple func annotations are present", func() {
			result := p.Update(event.UpdateEvent{
				ObjectOld: &v1alpha1.Function{
					ObjectMeta: metav1.ObjectMeta{Generation: 1},
				},
				ObjectNew: &v1alpha1.Function{
					ObjectMeta: metav1.ObjectMeta{
						Generation: 1,
						Annotations: map[string]string{
							"functions.knative.dev/rebuild": "true",
							"functions.knative.dev/reason":  "config-change",
						},
					},
				},
			})
			Expect(result).To(BeTrue())
		})

		It("should not trigger on non-func annotation change", func() {
			result := p.Update(event.UpdateEvent{
				ObjectOld: &v1alpha1.Function{
					ObjectMeta: metav1.ObjectMeta{Generation: 1},
				},
				ObjectNew: &v1alpha1.Function{
					ObjectMeta: metav1.ObjectMeta{
						Generation: 1,
						Annotations: map[string]string{
							"some-other-annotation": "value",
						},
					},
				},
			})
			Expect(result).To(BeFalse())
		})

		It("should not trigger when nothing changed", func() {
			result := p.Update(event.UpdateEvent{
				ObjectOld: &v1alpha1.Function{
					ObjectMeta: metav1.ObjectMeta{Generation: 1},
				},
				ObjectNew: &v1alpha1.Function{
					ObjectMeta: metav1.ObjectMeta{Generation: 1},
				},
			})
			Expect(result).To(BeFalse())
		})

		It("should not trigger when new object is nil", func() {
			result := p.Update(event.UpdateEvent{
				ObjectOld: &v1alpha1.Function{
					ObjectMeta: metav1.ObjectMeta{Generation: 1},
				},
				ObjectNew: nil,
			})
			Expect(result).To(BeFalse())
		})
	})

	Context("Create", func() {
		It("should trigger on create", func() {
			result := p.Create(event.CreateEvent{
				Object: &v1alpha1.Function{
					ObjectMeta: metav1.ObjectMeta{Generation: 1},
				},
			})
			Expect(result).To(BeTrue())
		})
	})

	Context("Delete", func() {
		It("should trigger on delete", func() {
			result := p.Delete(event.DeleteEvent{
				Object: &v1alpha1.Function{
					ObjectMeta: metav1.ObjectMeta{Generation: 1},
				},
			})
			Expect(result).To(BeTrue())
		})
	})
})
