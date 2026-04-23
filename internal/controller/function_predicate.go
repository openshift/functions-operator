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
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const funcAnnotationPrefix = "functions.knative.dev/"

// FuncAnnotationChangedPredicate triggers reconciliation when annotations
// with the "functions.knative.dev/" prefix are added or changed.
type FuncAnnotationChangedPredicate struct {
	predicate.Funcs
}

func (FuncAnnotationChangedPredicate) Update(e event.UpdateEvent) bool {
	if e.ObjectNew == nil {
		return false
	}
	return hasFuncAnnotations(e.ObjectNew)
}

func hasFuncAnnotations(obj client.Object) bool {
	for key := range obj.GetAnnotations() {
		if strings.HasPrefix(key, funcAnnotationPrefix) {
			return true
		}
	}
	return false
}
