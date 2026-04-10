package v1alpha1

import (
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestCalculateReadyCondition(t *testing.T) {
	tests := []struct {
		name            string
		conditions      []metav1.Condition
		expectedStatus  metav1.ConditionStatus
		expectedReason  string
		expectedMessage string
	}{
		{
			name: "all conditions true",
			conditions: []metav1.Condition{
				{Type: TypeSourceReady, Status: metav1.ConditionTrue, Reason: "CloneSucceeded"},
				{Type: TypeDeployed, Status: metav1.ConditionTrue, Reason: "DeploySucceeded"},
				{Type: TypeMiddlewareUpToDate, Status: metav1.ConditionTrue, Reason: "UpToDate"},
			},
			expectedStatus: metav1.ConditionTrue,
			expectedReason: "ReconcileSucceeded",
		},
		{
			name: "one condition false",
			conditions: []metav1.Condition{
				{Type: TypeSourceReady, Status: metav1.ConditionTrue, Reason: "CloneSucceeded"},
				{Type: TypeDeployed, Status: metav1.ConditionFalse, Reason: "DeployFailed", Message: "deployment failed"},
				{Type: TypeMiddlewareUpToDate, Status: metav1.ConditionTrue, Reason: "UpToDate"},
			},
			expectedStatus:  metav1.ConditionFalse,
			expectedReason:  "DeployFailed",
			expectedMessage: "deployment failed",
		},
		{
			name: "one condition unknown",
			conditions: []metav1.Condition{
				{Type: TypeSourceReady, Status: metav1.ConditionTrue, Reason: "CloneSucceeded"},
				{Type: TypeDeployed, Status: metav1.ConditionUnknown, Reason: "NotChecked", Message: "deployment not checked yet"},
				{Type: TypeMiddlewareUpToDate, Status: metav1.ConditionTrue, Reason: "UpToDate"},
			},
			expectedStatus:  metav1.ConditionFalse,
			expectedReason:  "NotChecked",
			expectedMessage: "deployment not checked yet",
		},
		{
			name: "multiple conditions unknown",
			conditions: []metav1.Condition{
				{Type: TypeSourceReady, Status: metav1.ConditionUnknown, Reason: "NotCloned", Message: "source not cloned yet"},
				{Type: TypeDeployed, Status: metav1.ConditionUnknown, Reason: "NotDeployed", Message: "not deployed yet"},
				{Type: TypeMiddlewareUpToDate, Status: metav1.ConditionTrue, Reason: "UpToDate"},
			},
			expectedStatus:  metav1.ConditionFalse,
			expectedReason:  "NotCloned",
			expectedMessage: "source not cloned yet",
		},
		{
			name: "false takes precedence over unknown",
			conditions: []metav1.Condition{
				{Type: TypeSourceReady, Status: metav1.ConditionUnknown, Reason: "NotCloned", Message: "source not cloned yet"},
				{Type: TypeDeployed, Status: metav1.ConditionFalse, Reason: "DeployFailed", Message: "deployment failed"},
				{Type: TypeMiddlewareUpToDate, Status: metav1.ConditionTrue, Reason: "UpToDate"},
			},
			expectedStatus:  metav1.ConditionFalse,
			expectedReason:  "DeployFailed",
			expectedMessage: "deployment failed",
		},
		{
			name: "all conditions unknown",
			conditions: []metav1.Condition{
				{Type: TypeSourceReady, Status: metav1.ConditionUnknown, Reason: "unknown", Message: ""},
				{Type: TypeDeployed, Status: metav1.ConditionUnknown, Reason: "unknown", Message: ""},
				{Type: TypeMiddlewareUpToDate, Status: metav1.ConditionUnknown, Reason: "unknown", Message: ""},
			},
			expectedStatus: metav1.ConditionFalse,
			expectedReason: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &Function{
				ObjectMeta: metav1.ObjectMeta{
					Generation: 1,
				},
			}
			f.Status.Conditions = tt.conditions

			f.CalculateReadyCondition()

			readyCondition := meta.FindStatusCondition(f.Status.Conditions, TypeReady)
			if readyCondition == nil {
				t.Fatal("Ready condition not found")
			}

			if readyCondition.Status != tt.expectedStatus {
				t.Errorf("expected status %v, got %v", tt.expectedStatus, readyCondition.Status)
			}

			if readyCondition.Reason != tt.expectedReason {
				t.Errorf("expected reason %q, got %q", tt.expectedReason, readyCondition.Reason)
			}

			if tt.expectedMessage != "" && readyCondition.Message != tt.expectedMessage {
				t.Errorf("expected message %q, got %q", tt.expectedMessage, readyCondition.Message)
			}
		})
	}
}

func TestInitializeConditions(t *testing.T) {
	f := &Function{
		ObjectMeta: metav1.ObjectMeta{
			Generation: 1,
		},
	}

	// Set some existing conditions
	f.Status.Conditions = []metav1.Condition{
		{Type: TypeSourceReady, Status: metav1.ConditionTrue, Reason: "CloneSucceeded"},
		{Type: TypeReady, Status: metav1.ConditionTrue, Reason: "ReconcileSucceeded"},
	}

	f.InitializeConditions()

	// Verify all conditions are reset to Unknown
	for _, condType := range FunctionsConditions {
		cond := meta.FindStatusCondition(f.Status.Conditions, condType)
		if cond == nil {
			t.Errorf("condition %s not found", condType)
			continue
		}
		if cond.Status != metav1.ConditionUnknown {
			t.Errorf("condition %s: expected status Unknown, got %v", condType, cond.Status)
		}
		if cond.Reason != "unknown" {
			t.Errorf("condition %s: expected reason 'unknown', got %q", condType, cond.Reason)
		}
		if cond.ObservedGeneration != f.Generation {
			t.Errorf("condition %s: expected generation %d, got %d", condType, f.Generation, cond.ObservedGeneration)
		}
	}

	// Verify Ready condition is also set to Unknown
	readyCond := meta.FindStatusCondition(f.Status.Conditions, TypeReady)
	if readyCond == nil {
		t.Fatal("Ready condition not found")
	}
	if readyCond.Status != metav1.ConditionUnknown {
		t.Errorf("Ready condition: expected status Unknown, got %v", readyCond.Status)
	}
}
