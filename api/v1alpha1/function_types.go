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

package v1alpha1

import (
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=func
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type==\"Ready\")].status",description="Ready status"
// +kubebuilder:printcolumn:name="Reason",type="string",JSONPath=".status.conditions[?(@.type==\"Ready\")].reason",description="Ready reason"
// +kubebuilder:printcolumn:name="Middleware",type="string",JSONPath=".status.middleware.current",description="Current deployed Middleware Version"
// +kubebuilder:printcolumn:name="Pending Rebuild",type="string",JSONPath=".status.middleware.pendingRebuild",description="Pending Rebuild"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// Function is the Schema for the functions API.
type Function struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   FunctionSpec   `json:"spec,omitempty"`
	Status FunctionStatus `json:"status,omitempty"`
}

// FunctionSpec defines the desired state of Function.
type FunctionSpec struct {
	Repository FunctionSpecRepository `json:"repository,omitempty"`
	Registry   FunctionSpecRegistry   `json:"registry,omitempty"`

	// AutoUpdateMiddleware defines if the operator should rebuild the function when an outdated middleware is detected.
	// Defaults to the global operator config.
	AutoUpdateMiddleware *bool `json:"autoUpdateMiddleware,omitempty"`
}

type FunctionSpecRepository struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// URL of the Git repository containing the function
	URL string `json:"url"`

	// +kubebuilder:validation:Optional
	// Revision of the repository to build: a branch name or a full ref such as refs/tags/v1.0.0.
	// Defaults to the repository's default branch.
	Revision string `json:"revision,omitempty"`

	// AuthSecretRef defines the reference to the auth secret in case the repository is private and needs authentication
	AuthSecretRef *v1.LocalObjectReference `json:"authSecretRef,omitempty"`

	// +kubebuilder:validation:Optional
	// Dir is the directory within the repository that holds the function (its func.yaml).
	// Defaults to the repository root.
	Dir string `json:"dir,omitempty"`
}

type FunctionSpecRegistry struct {
	// AuthSecretRef is the reference to the secret containing the credentials for the registry authentication
	AuthSecretRef *v1.LocalObjectReference `json:"authSecretRef,omitempty"`
}

// FunctionStatus defines the observed state of Function.
type FunctionStatus struct {
	Name string `json:"name"`

	Git        FunctionStatusGit        `json:"git,omitempty"`
	Deployment FunctionStatusDeployment `json:"deployment,omitempty"`
	Middleware FunctionStatusMiddleware `json:"middleware,omitempty"`
	Service    FunctionServiceStatus    `json:"service,omitempty"`

	History []FunctionStatusHistoryEntry `json:"history,omitempty"`

	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

type FunctionStatusHistoryEntry struct {
	Time    metav1.Time `json:"time"`
	Message string      `json:"message"`
}

type FunctionStatusGit struct {
	// ResolvedRevision is the revision that was checked out: spec.repository.revision,
	// or the name of the default branch when that is empty.
	ResolvedRevision string      `json:"resolvedRevision,omitempty"`
	ObservedCommit   string      `json:"observedCommit,omitempty"`
	LastChecked      metav1.Time `json:"lastChecked,omitempty"`
}

type FunctionStatusDeployment struct {
	Image      string      `json:"image,omitempty"`
	ImageBuilt metav1.Time `json:"imageBuilt,omitempty"`
	Revision   string      `json:"revision,omitempty"`
	Deployer   string      `json:"deployer,omitempty"`
	Runtime    string      `json:"runtime,omitempty"`
}

type FunctionStatusMiddleware struct {
	Current        string                             `json:"current,omitempty"`
	Available      *string                            `json:"available,omitempty"`
	AutoUpdate     FunctionStatusMiddlewareAutoUpdate `json:"autoUpdate"`
	PendingRebuild bool                               `json:"pendingRebuild"` // no omitempty to have it always shown
	LastRebuild    metav1.Time                        `json:"lastRebuild,omitempty"`
}

type FunctionServiceStatus struct {
	URL   string `json:"url,omitempty"`
	Ready string `json:"ready,omitempty"` // string, as it can be UNKNOWN too
}

type FunctionStatusMiddlewareAutoUpdate struct {
	Enabled bool   `json:"enabled"` // no omitempty to have it always shown
	Source  string `json:"source,omitempty"`
}

// +kubebuilder:object:root=true

// FunctionList contains a list of Function.
type FunctionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Function `json:"items"`
}
