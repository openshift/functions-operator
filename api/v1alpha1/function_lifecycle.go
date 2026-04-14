package v1alpha1

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Condition types
const (
	// TypeReady indicates overall readiness (summary condition)
	TypeReady = "Ready"

	// TypeSourceReady indicates git source was cloned successfully
	TypeSourceReady = "SourceReady"

	// TypeDeployed indicates function is deployed
	TypeDeployed = "Deployed"

	// TypeMiddlewareUpToDate indicates middleware is current
	TypeMiddlewareUpToDate = "MiddlewareUpToDate"
)

var FunctionsConditions = []string{
	TypeSourceReady,
	TypeDeployed,
	TypeMiddlewareUpToDate,
}

// InitializeConditions resets all conditions to ensure a fresh start for each reconcile.
// This prevents stale conditions from previous reconciles from persisting.
func (f *Function) InitializeConditions() {
	f.Status.Conditions = []metav1.Condition{}
	for _, condition := range FunctionsConditions {
		meta.SetStatusCondition(&f.Status.Conditions, metav1.Condition{
			Type:               condition,
			Status:             metav1.ConditionUnknown,
			Reason:             "unknown",
			ObservedGeneration: f.Generation,
		})
	}

	meta.SetStatusCondition(&f.Status.Conditions, metav1.Condition{
		Type:   TypeReady,
		Status: metav1.ConditionUnknown,
	})
}

func (f *Function) CalculateReadyCondition() {
	allReady := true
	reason := ""
	message := ""
	for _, condition := range f.Status.Conditions {
		if condition.Type != TypeReady {
			if condition.Status == metav1.ConditionFalse {
				allReady = false
				reason = condition.Reason
				message = condition.Message
				continue
			} else if condition.Status == metav1.ConditionUnknown {
				allReady = false

				// override reason & message only if not set already
				// (e.g. if set by a ConditionFalse as this takes preference)
				if reason == "" {
					reason = condition.Reason
				}
				if message == "" {
					message = condition.Message
				}
				continue
			}
		}
	}

	if allReady {
		f.MarkReady()
	} else {
		f.MarkNotReady(reason, "%s", message)
	}
}

// Ready condition helpers

func (f *Function) MarkReady() bool {
	return meta.SetStatusCondition(&f.Status.Conditions, metav1.Condition{
		Type:               TypeReady,
		Status:             metav1.ConditionTrue,
		Reason:             "ReconcileSucceeded",
		ObservedGeneration: f.Generation,
	})
}

func (f *Function) MarkNotReady(reason, messageFormat string, messageA ...interface{}) bool {
	return meta.SetStatusCondition(&f.Status.Conditions, metav1.Condition{
		Type:               TypeReady,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            fmt.Sprintf(messageFormat, messageA...),
		ObservedGeneration: f.Generation,
	})
}

func (f *Function) MarkTerminating() bool {
	return meta.SetStatusCondition(&f.Status.Conditions, metav1.Condition{
		Type:               TypeReady,
		Status:             metav1.ConditionFalse,
		Reason:             "FinalizerOperations",
		Message:            "Performing cleanup operations before deletion",
		ObservedGeneration: f.Generation,
	})
}

func (f *Function) MarkFinalizeFailed(err error) bool {
	return meta.SetStatusCondition(&f.Status.Conditions, metav1.Condition{
		Type:               TypeReady,
		Status:             metav1.ConditionFalse,
		Reason:             "FinalizeFailed",
		Message:            fmt.Sprintf("Failed to finalize: %s", err.Error()),
		ObservedGeneration: f.Generation,
	})
}

// Source condition helpers

func (f *Function) MarkSourceReady() bool {
	return meta.SetStatusCondition(&f.Status.Conditions, metav1.Condition{
		Type:               TypeSourceReady,
		Status:             metav1.ConditionTrue,
		Reason:             "CloneSucceeded",
		ObservedGeneration: f.Generation,
	})
}

func (f *Function) MarkSourceNotReady(reason, messageFormat string, messageA ...interface{}) bool {
	return meta.SetStatusCondition(&f.Status.Conditions, metav1.Condition{
		Type:               TypeSourceReady,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            fmt.Sprintf(messageFormat, messageA...),
		ObservedGeneration: f.Generation,
	})
}

// Deployment condition helpers

func (f *Function) MarkDeployReady() bool {
	return meta.SetStatusCondition(&f.Status.Conditions, metav1.Condition{
		Type:               TypeDeployed,
		Status:             metav1.ConditionTrue,
		Reason:             "DeploySucceeded",
		ObservedGeneration: f.Generation,
	})
}

func (f *Function) MarkDeployNotReady(reason, messageFormat string, messageA ...interface{}) bool {
	return meta.SetStatusCondition(&f.Status.Conditions, metav1.Condition{
		Type:               TypeDeployed,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            fmt.Sprintf(messageFormat, messageA...),
		ObservedGeneration: f.Generation,
	})
}

// Middleware condition helpers

func (f *Function) MarkMiddlewareUpToDate() bool {
	return meta.SetStatusCondition(&f.Status.Conditions, metav1.Condition{
		Type:               TypeMiddlewareUpToDate,
		Status:             metav1.ConditionTrue,
		Reason:             "UpToDate",
		ObservedGeneration: f.Generation,
	})
}

func (f *Function) MarkMiddlewareNotUpToDate(reason, messageFormat string, messageA ...interface{}) bool {
	return meta.SetStatusCondition(&f.Status.Conditions, metav1.Condition{
		Type:               TypeMiddlewareUpToDate,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            fmt.Sprintf(messageFormat, messageA...),
		ObservedGeneration: f.Generation,
	})
}

func (f *Function) MarkMiddlewareNotUpToDateIntentionally(reason, messageFormat string, messageA ...interface{}) bool {
	return meta.SetStatusCondition(&f.Status.Conditions, metav1.Condition{
		Type:               TypeMiddlewareUpToDate,
		Status:             metav1.ConditionTrue,
		Reason:             reason,
		Message:            fmt.Sprintf(messageFormat, messageA...),
		ObservedGeneration: f.Generation,
	})
}
