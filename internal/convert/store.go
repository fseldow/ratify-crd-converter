package convert

import (
	v1 "github.com/fseldow/ratify-crd-converter/internal/apis/v1beta1"
	v2 "github.com/fseldow/ratify-crd-converter/internal/apis/v2beta1"
	"github.com/fseldow/ratify-crd-converter/internal/report"
)

// storeTypeAlias maps v1 Store.spec.name to the v2 store type where they differ.
var storeTypeAlias = map[string]string{
	"oras": "registry-store",
}

// StoreAlias returns the v2 store type for a v1 store name.
func StoreAlias(v1Name string) string {
	if t, ok := storeTypeAlias[v1Name]; ok {
		return t
	}
	return v1Name
}

// convertStore maps a v1 Store to a v2 StoreOptions. address/source are dropped
// with a warning only when non-empty (built-in plugins leave them empty).
func convertStore(s *v1.Store, rep *report.Reporter) (*v2.StoreOptions, error) {
	res := resourceID(v1.KindStore, s.Namespace, s.Name)
	if s.Spec.Address != "" {
		rep.Warnf(res, "spec.address=%q dropped: v2 has no external-plugin path; port the plugin to the v2 Go model", s.Spec.Address)
	}
	if s.Spec.Source != nil {
		rep.Warnf(res, "spec.source (dynamic plugin %q) dropped: v2 has no OCI plugin download; port the plugin to the v2 Go model", s.Spec.Source.Artifact)
	}
	return &v2.StoreOptions{
		Type:       StoreAlias(s.Spec.Name),
		Parameters: s.Spec.Parameters,
	}, nil
}
