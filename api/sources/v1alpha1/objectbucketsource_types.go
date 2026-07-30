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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	ConditionOBCCredentialsAvailable = "OBCCredentialsAvailable"
	ConditionBucketNotificationSet   = "BucketNotificationSet"
	ConditionTestEventReceived       = "TestEventReceived"

	FinalizerName = "sources.functions.dev/objectbucketsource"
)

// ObjectBucketSourceSpec defines the desired state of ObjectBucketSource.
type ObjectBucketSourceSpec struct {
	// ObjectBucketClaim is a reference to the ObjectBucketClaim in the same namespace.
	ObjectBucketClaim OBCReference `json:"objectBucketClaim"`
	// Events is the list of S3 event types to subscribe to (e.g. "s3:ObjectCreated:*").
	Events []string `json:"events"`
	// Sink is the endpoint to dispatch matching CloudEvents to.
	Sink SinkSpec `json:"sink"`
}

type OBCReference struct {
	// Name of the ObjectBucketClaim in the same namespace.
	Name string `json:"name"`
}

// SinkSpec defines the destination for CloudEvents. URI can be an HTTP URL
// (e.g. "http://foo.bar.svc.cluster.local") or a Kafka topic reference
// (e.g. "kafka:my-topic").
type SinkSpec struct {
	// URI is the destination to send CloudEvents to. Use an HTTP URL for
	// HTTP delivery, or "kafka:<topic>" for Kafka delivery.
	URI string `json:"uri"`
}

// ObjectBucketSourceStatus defines the observed state of ObjectBucketSource.
type ObjectBucketSourceStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ObjectBucketSource is the Schema for the objectbucketsources API.
type ObjectBucketSource struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ObjectBucketSourceSpec   `json:"spec,omitempty"`
	Status ObjectBucketSourceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ObjectBucketSourceList contains a list of ObjectBucketSource.
type ObjectBucketSourceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ObjectBucketSource `json:"items"`
}
