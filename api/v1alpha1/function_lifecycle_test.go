package v1alpha1

import (
	"fmt"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestRecordHistoryEvent(t *testing.T) {
	tests := []struct {
		name            string
		existingHistory []FunctionStatusHistoryEntry
		newMessage      string
		expectedLen     int
		expectedFirst   string
		expectedLast    string
	}{
		{
			name:            "adds event to empty history",
			existingHistory: nil,
			newMessage:      "Function deployed",
			expectedLen:     1,
			expectedFirst:   "Function deployed",
			expectedLast:    "Function deployed",
		},
		{
			name: "prepends event to existing history",
			existingHistory: []FunctionStatusHistoryEntry{
				{Time: metav1.Now(), Message: "Older event"},
			},
			newMessage:    "Newer event",
			expectedLen:   2,
			expectedFirst: "Newer event",
			expectedLast:  "Older event",
		},
		{
			name: "trims oldest entries when exceeding max",
			existingHistory: func() []FunctionStatusHistoryEntry {
				entries := make([]FunctionStatusHistoryEntry, MaxHistoryEntries)
				for i := range entries {
					entries[i] = FunctionStatusHistoryEntry{
						Time:    metav1.Now(),
						Message: fmt.Sprintf("Event %d", i),
					}
				}
				return entries
			}(),
			newMessage:    "Overflow event",
			expectedLen:   MaxHistoryEntries,
			expectedFirst: "Overflow event",
			expectedLast:  fmt.Sprintf("Event %d", MaxHistoryEntries-2),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &Function{}
			f.Status.History = tt.existingHistory

			f.RecordHistoryEvent(tt.newMessage)

			if len(f.Status.History) != tt.expectedLen {
				t.Errorf("expected %d entries, got %d", tt.expectedLen, len(f.Status.History))
			}
			if f.Status.History[0].Message != tt.expectedFirst {
				t.Errorf("expected first message %q, got %q", tt.expectedFirst, f.Status.History[0].Message)
			}
			if f.Status.History[len(f.Status.History)-1].Message != tt.expectedLast {
				t.Errorf("expected last message %q, got %q", tt.expectedLast, f.Status.History[len(f.Status.History)-1].Message)
			}
		})
	}
}

func TestRecordHistoryEventFIFOOrder(t *testing.T) {
	f := &Function{}

	for i := 0; i < MaxHistoryEntries+5; i++ {
		f.RecordHistoryEvent(fmt.Sprintf("Event %d", i))
	}

	if len(f.Status.History) != MaxHistoryEntries {
		t.Fatalf("expected %d entries, got %d", MaxHistoryEntries, len(f.Status.History))
	}

	for i, entry := range f.Status.History {
		expected := fmt.Sprintf("Event %d", MaxHistoryEntries+4-i)
		if entry.Message != expected {
			t.Errorf("entry %d: expected message %q, got %q", i, expected, entry.Message)
		}
	}
}

func TestRecordHistoryEventSetsTime(t *testing.T) {
	f := &Function{}
	f.RecordHistoryEvent("test event")

	if f.Status.History[0].Time.IsZero() {
		t.Error("expected non-zero time")
	}
}

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
				{Type: TypeServiceReady, Status: metav1.ConditionTrue, Reason: "ServiceReady"},
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
				{Type: TypeServiceReady, Status: metav1.ConditionTrue, Reason: "ServiceReady"},
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
				{Type: TypeServiceReady, Status: metav1.ConditionTrue, Reason: "ServiceReady"},
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
				{Type: TypeServiceReady, Status: metav1.ConditionTrue, Reason: "ServiceReady"},
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
				{Type: TypeServiceReady, Status: metav1.ConditionTrue, Reason: "ServiceReady"},
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
				{Type: TypeServiceReady, Status: metav1.ConditionUnknown, Reason: "unknown", Message: ""},
			},
			expectedStatus: metav1.ConditionFalse,
			expectedReason: "unknown",
		},
		{
			name: "service not ready makes overall not ready",
			conditions: []metav1.Condition{
				{Type: TypeSourceReady, Status: metav1.ConditionTrue, Reason: "CloneSucceeded"},
				{Type: TypeDeployed, Status: metav1.ConditionTrue, Reason: "DeploySucceeded"},
				{Type: TypeMiddlewareUpToDate, Status: metav1.ConditionTrue, Reason: "UpToDate"},
				{Type: TypeServiceReady, Status: metav1.ConditionFalse, Reason: "ServiceNotReady", Message: "Underlying service is not ready"},
			},
			expectedStatus:  metav1.ConditionFalse,
			expectedReason:  "ServiceNotReady",
			expectedMessage: "Underlying service is not ready",
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
				return
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
		return
	}
	if readyCond.Status != metav1.ConditionUnknown {
		t.Errorf("Ready condition: expected status Unknown, got %v", readyCond.Status)
	}
}
