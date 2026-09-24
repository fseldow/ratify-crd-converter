// Package cluster reads Ratify v1 CRs directly from a Kubernetes cluster and
// applies generated v2 Executor resources back, so the converter can migrate
// in place without YAML files as an intermediate step.
package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"

	v1 "github.com/fseldow/ratify-crd-converter/internal/apis/v1beta1"
	v2 "github.com/fseldow/ratify-crd-converter/internal/apis/v2beta1"
	"github.com/fseldow/ratify-crd-converter/internal/loader"
)

// Client wraps a dynamic Kubernetes client.
type Client struct {
	dyn dynamic.Interface
}

func v1gvr(resource string) schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: v1.Group, Version: v1.Version, Resource: resource}
}

var (
	execGVR   = schema.GroupVersionResource{Group: v2.Group, Version: v2.Version, Resource: "executors"}
	nsExecGVR = schema.GroupVersionResource{Group: v2.Group, Version: v2.Version, Resource: "namespacedexecutors"}
)

// New builds a Client from the given kubeconfig path (empty = default loading
// rules: $KUBECONFIG then ~/.kube/config).
func New(kubeconfig string) (*Client, error) {
	if kubeconfig == "" {
		if h := homedir.HomeDir(); h != "" {
			kubeconfig = filepath.Join(h, ".kube", "config")
		}
	}
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfig != "" {
		loadingRules.ExplicitPath = kubeconfig
	}
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loadingRules, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("build dynamic client: %w", err)
	}
	return &Client{dyn: dyn}, nil
}

// Load lists every Ratify v1 CR (cluster-scoped and namespaced) into a Bundle.
// Missing CRDs are tolerated so partial installs still convert.
func (c *Client) Load(ctx context.Context) (*loader.Bundle, error) {
	b := &loader.Bundle{}

	stores := func(raw []byte) error {
		var o v1.Store
		if err := json.Unmarshal(raw, &o); err != nil {
			return err
		}
		b.Stores = append(b.Stores, &o)
		return nil
	}
	verifiers := func(raw []byte) error {
		var o v1.Verifier
		if err := json.Unmarshal(raw, &o); err != nil {
			return err
		}
		b.Verifiers = append(b.Verifiers, &o)
		return nil
	}
	policies := func(raw []byte) error {
		var o v1.Policy
		if err := json.Unmarshal(raw, &o); err != nil {
			return err
		}
		b.Policies = append(b.Policies, &o)
		return nil
	}
	kmps := func(raw []byte) error {
		var o v1.KeyManagementProvider
		if err := json.Unmarshal(raw, &o); err != nil {
			return err
		}
		b.KMPs = append(b.KMPs, &o)
		return nil
	}
	certStores := func(raw []byte) error {
		var o v1.CertificateStore
		if err := json.Unmarshal(raw, &o); err != nil {
			return err
		}
		b.CertStores = append(b.CertStores, &o)
		return nil
	}

	jobs := []struct {
		resource string
		fn       func([]byte) error
	}{
		{"stores", stores},
		{"namespacedstores", stores},
		{"verifiers", verifiers},
		{"namespacedverifiers", verifiers},
		{"policies", policies},
		{"namespacedpolicies", policies},
		{"keymanagementproviders", kmps},
		{"namespacedkeymanagementproviders", kmps},
		{"certificatestores", certStores},
	}
	for _, j := range jobs {
		if err := listInto(ctx, c.dyn, v1gvr(j.resource), j.fn); err != nil {
			return nil, err
		}
	}
	return b, nil
}

// listInto lists all objects of a GVR (across all namespaces for namespaced
// resources) and feeds each item's JSON to fn. A missing CRD (NotFound) is
// treated as an empty list.
func listInto(ctx context.Context, dyn dynamic.Interface, gvr schema.GroupVersionResource, fn func([]byte) error) error {
	list, err := dyn.Resource(gvr).List(ctx, metav1.ListOptions{})
	if err != nil {
		if isNotFound(err) {
			return nil
		}
		return fmt.Errorf("list %s: %w", gvr.Resource, err)
	}
	for i := range list.Items {
		raw, err := list.Items[i].MarshalJSON()
		if err != nil {
			return err
		}
		if err := fn(raw); err != nil {
			return fmt.Errorf("%s/%s: %w", gvr.Resource, list.Items[i].GetName(), err)
		}
	}
	return nil
}

// Apply server-side applies every generated executor. dryRun uses server-side
// dry-run so nothing is persisted.
func (c *Client) Apply(ctx context.Context, executors []*v2.Executor, nsExecutors []*v2.NamespacedExecutor, dryRun bool) error {
	opts := metav1.ApplyOptions{FieldManager: "ratify-convert", Force: true}
	if dryRun {
		opts.DryRun = []string{metav1.DryRunAll}
	}
	for _, e := range executors {
		obj, err := toUnstructured(e)
		if err != nil {
			return err
		}
		if _, err := c.dyn.Resource(execGVR).Apply(ctx, e.Name, obj, opts); err != nil {
			return fmt.Errorf("apply executor/%s: %w", e.Name, err)
		}
	}
	for _, e := range nsExecutors {
		obj, err := toUnstructured(e)
		if err != nil {
			return err
		}
		if _, err := c.dyn.Resource(nsExecGVR).Namespace(e.Namespace).Apply(ctx, e.Name, obj, opts); err != nil {
			return fmt.Errorf("apply namespacedexecutor/%s/%s: %w", e.Namespace, e.Name, err)
		}
	}
	return nil
}

func toUnstructured(v any) (*unstructured.Unstructured, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	m := map[string]any{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return &unstructured.Unstructured{Object: m}, nil
}

func isNotFound(err error) bool {
	type statusErr interface{ Status() metav1.Status }
	if se, ok := err.(statusErr); ok {
		return se.Status().Reason == metav1.StatusReasonNotFound
	}
	return false
}
