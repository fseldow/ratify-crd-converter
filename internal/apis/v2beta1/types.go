// Package v2beta1 contains minimal Ratify v2 (config.ratify.sh/v2beta1)
// API types produced by the converter.
package v2beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	Group        = "config.ratify.sh"
	Version      = "v2beta1"
	GroupVersion = Group + "/" + Version

	KindExecutor           = "Executor"
	KindNamespacedExecutor = "NamespacedExecutor"
)

type StoreOptions struct {
	Type       string               `json:"type"`
	Parameters runtime.RawExtension `json:"parameters,omitempty"`
}

type VerifierOptions struct {
	Name       string               `json:"name"`
	Type       string               `json:"type"`
	Parameters runtime.RawExtension `json:"parameters,omitempty"`
}

type PolicyEnforcerOptions struct {
	Type       string               `json:"type"`
	Parameters runtime.RawExtension `json:"parameters,omitempty"`
}

type ExecutorSpec struct {
	Scopes         []string               `json:"scopes"`
	Stores         []*StoreOptions        `json:"stores"`
	Verifiers      []*VerifierOptions     `json:"verifiers"`
	PolicyEnforcer *PolicyEnforcerOptions `json:"policyEnforcer,omitempty"`
	Concurrency    int                    `json:"concurrency,omitempty"`
}

type Executor struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ExecutorSpec `json:"spec"`
}

type NamespacedExecutor struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ExecutorSpec `json:"spec"`
}

// NewExecutor returns a cluster-scoped Executor with TypeMeta populated.
func NewExecutor(name string) *Executor {
	e := &Executor{Spec: ExecutorSpec{}}
	e.APIVersion = GroupVersion
	e.Kind = KindExecutor
	e.Name = name
	return e
}

// NewNamespacedExecutor returns a NamespacedExecutor with TypeMeta populated.
func NewNamespacedExecutor(name, namespace string) *NamespacedExecutor {
	e := &NamespacedExecutor{Spec: ExecutorSpec{}}
	e.APIVersion = GroupVersion
	e.Kind = KindNamespacedExecutor
	e.Name = name
	e.Namespace = namespace
	return e
}
