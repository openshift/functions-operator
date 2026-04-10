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

	"github.com/functions-dev/func-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// StatusTracker manages incremental status updates during reconciliation
type StatusTracker struct {
	k8sClient client.Client
	original  *v1alpha1.Function
}

// NewStatusTracker creates a new status tracker with a snapshot of the current function state
func NewStatusTracker(k8sClient client.Client, function *v1alpha1.Function) *StatusTracker {
	return &StatusTracker{
		k8sClient: k8sClient,
		original:  function.DeepCopy(),
	}
}

// Flush updates the function status if it has changed since the last flush
func (t *StatusTracker) Flush(ctx context.Context, current *v1alpha1.Function) error {
	// Always calculate ready condition before comparing
	current.CalculateReadyCondition()

	// Compare and update if changed
	if !equality.Semantic.DeepEqual(t.original.Status, current.Status) {
		// Retry on conflict with exponential backoff
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			// Get the latest version to ensure we have the most recent resourceVersion
			latest := &v1alpha1.Function{}
			if err := t.k8sClient.Get(ctx, types.NamespacedName{
				Name:      current.Name,
				Namespace: current.Namespace,
			}, latest); err != nil {
				return err
			}

			// Apply our status changes to the latest version
			latest.Status = current.Status

			// Attempt the update
			return t.k8sClient.Status().Update(ctx, latest)
		})

		if err != nil {
			return fmt.Errorf("failed to update status: %w", err)
		}

		// Update our snapshot to the new state
		t.original = current.DeepCopy()
	}

	return nil
}

// statusTrackerKey is the context key for the status tracker
type statusTrackerKey struct{}

// WithStatusTracker adds a status tracker to the context
func WithStatusTracker(ctx context.Context, tracker *StatusTracker) context.Context {
	return context.WithValue(ctx, statusTrackerKey{}, tracker)
}

// GetStatusTracker retrieves the tracker from context
func GetStatusTracker(ctx context.Context) *StatusTracker {
	tracker, ok := ctx.Value(statusTrackerKey{}).(*StatusTracker)
	if !ok {
		return nil
	}
	return tracker
}

// FlushStatus is a convenience helper that gets tracker from context and flushes
func FlushStatus(ctx context.Context, function *v1alpha1.Function) error {
	tracker := GetStatusTracker(ctx)
	if tracker == nil {
		return nil // gracefully handle missing tracker
	}
	return tracker.Flush(ctx, function)
}
