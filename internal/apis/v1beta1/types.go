// Package v1beta1 contains minimal Ratify v1 (config.ratify.deislabs.io/v1beta1)
// API types needed for conversion. Only the fields relevant to migration are
// modeled; parameters are kept as raw JSON for lossless passthrough.
package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	Group        = "config.ratify.deislabs.io"
	Version      = "v1beta1"
	GroupVersion = Group + "/" + Version

	KindStore              = "Store"
	KindNamespacedStore    = "NamespacedStore"
	KindVerifier           = "Verifier"
	KindNamespacedVerifier = "NamespacedVerifier"
	KindPolicy             = "Policy"
	KindNamespacedPolicy   = "NamespacedPolicy"
	KindKMP                = "KeyManagementProvider"
	KindNamespacedKMP      = "NamespacedKeyManagementProvider"
	KindCertificateStore   = "CertificateStore"
)

// PluginSource describes where to download an external plugin binary from.
// It has no v2 equivalent (v2 uses built-in/Go plugins).
type PluginSource struct {
	Artifact     string               `json:"artifact,omitempty"`
	AuthProvider runtime.RawExtension `json:"authProvider,omitempty"`
}

type Store struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              StoreSpec `json:"spec,omitempty"`
}

type StoreSpec struct {
	Name       string               `json:"name,omitempty"`
	Version    string               `json:"version,omitempty"`
	Address    string               `json:"address,omitempty"`
	Source     *PluginSource        `json:"source,omitempty"`
	Parameters runtime.RawExtension `json:"parameters,omitempty"`
}

type Verifier struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              VerifierSpec `json:"spec,omitempty"`
}

type VerifierSpec struct {
	Name          string               `json:"name,omitempty"`
	Version       string               `json:"version,omitempty"`
	ArtifactTypes string               `json:"artifactTypes,omitempty"`
	Address       string               `json:"address,omitempty"`
	Source        *PluginSource        `json:"source,omitempty"`
	Parameters    runtime.RawExtension `json:"parameters,omitempty"`
}

type Policy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              PolicySpec `json:"spec,omitempty"`
}

type PolicySpec struct {
	Type       string               `json:"type,omitempty"`
	Parameters runtime.RawExtension `json:"parameters,omitempty"`
}

type KeyManagementProvider struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              KeyManagementProviderSpec `json:"spec,omitempty"`
}

type KeyManagementProviderSpec struct {
	Type            string               `json:"type,omitempty"`
	RefreshInterval string               `json:"refreshInterval,omitempty"`
	Parameters      runtime.RawExtension `json:"parameters,omitempty"`
}

type CertificateStore struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              CertificateStoreSpec `json:"spec,omitempty"`
}

type CertificateStoreSpec struct {
	Provider   string               `json:"provider,omitempty"`
	Parameters runtime.RawExtension `json:"parameters,omitempty"`
}
