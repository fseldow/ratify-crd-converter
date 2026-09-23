// Package loader reads v1 Ratify manifests (single files, multi-document YAML,
// or directories) and decodes them into a typed Bundle grouped by scope.
package loader

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	v1 "github.com/fseldow/ratify-crd-converter/internal/apis/v1beta1"
)

// Bundle holds all decoded v1 resources. Namespace on each item's ObjectMeta is
// used later to group cluster vs namespaced executors.
type Bundle struct {
	Stores     []*v1.Store
	Verifiers  []*v1.Verifier
	Policies   []*v1.Policy
	KMPs       []*v1.KeyManagementProvider
	CertStores []*v1.CertificateStore
}

// LoadPaths loads every YAML file under the given files/directories.
func LoadPaths(paths []string) (*Bundle, error) {
	b := &Bundle{}
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, fmt.Errorf("stat %s: %w", p, err)
		}
		if info.IsDir() {
			err = filepath.WalkDir(p, func(path string, d os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() || !isYAML(path) {
					return nil
				}
				return b.loadFile(path)
			})
			if err != nil {
				return nil, err
			}
			continue
		}
		if err := b.loadFile(p); err != nil {
			return nil, err
		}
	}
	return b, nil
}

func isYAML(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".yaml" || ext == ".yml"
}

func (b *Bundle) loadFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	for i, doc := range splitYAML(data) {
		if len(bytes.TrimSpace(doc)) == 0 {
			continue
		}
		if err := b.decodeDoc(doc); err != nil {
			return fmt.Errorf("%s[doc %d]: %w", path, i, err)
		}
	}
	return nil
}

// splitYAML splits a multi-document YAML byte slice on "---" separators.
func splitYAML(data []byte) [][]byte {
	parts := bytes.Split(data, []byte("\n---"))
	out := make([][]byte, 0, len(parts))
	for _, p := range parts {
		out = append(out, bytes.TrimPrefix(p, []byte("---")))
	}
	return out
}

func (b *Bundle) decodeDoc(doc []byte) error {
	var tm metav1.TypeMeta
	if err := yaml.Unmarshal(doc, &tm); err != nil {
		return fmt.Errorf("decode typemeta: %w", err)
	}
	if tm.APIVersion != v1.GroupVersion && !strings.HasPrefix(tm.APIVersion, v1.Group+"/") {
		// Not a Ratify v1 resource; skip silently.
		return nil
	}
	switch tm.Kind {
	case v1.KindStore, v1.KindNamespacedStore:
		var o v1.Store
		if err := yaml.Unmarshal(doc, &o); err != nil {
			return err
		}
		b.Stores = append(b.Stores, &o)
	case v1.KindVerifier, v1.KindNamespacedVerifier:
		var o v1.Verifier
		if err := yaml.Unmarshal(doc, &o); err != nil {
			return err
		}
		b.Verifiers = append(b.Verifiers, &o)
	case v1.KindPolicy, v1.KindNamespacedPolicy:
		var o v1.Policy
		if err := yaml.Unmarshal(doc, &o); err != nil {
			return err
		}
		b.Policies = append(b.Policies, &o)
	case v1.KindKMP, v1.KindNamespacedKMP:
		var o v1.KeyManagementProvider
		if err := yaml.Unmarshal(doc, &o); err != nil {
			return err
		}
		b.KMPs = append(b.KMPs, &o)
	case v1.KindCertificateStore:
		var o v1.CertificateStore
		if err := yaml.Unmarshal(doc, &o); err != nil {
			return err
		}
		b.CertStores = append(b.CertStores, &o)
	default:
		return fmt.Errorf("unknown kind %q", tm.Kind)
	}
	return nil
}
