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

// v1 oras store parameter keys that have no equivalent on the v2 registry-store
// and must not be passed through (the v2 options struct rejects unknown fields
// only loosely, but keeping them is misleading).
var droppedStoreParams = []string{"cacheEnabled", "cosignEnabled", "ttl", "useHttp"}

// convertStore maps a v1 Store to a v2 StoreOptions.
//
//   - address/source are dropped (warned only when non-empty; built-in plugins
//     leave them empty).
//   - The v2 registry-store REQUIRES a credential provider
//     (parameters.credential.provider); v1 oras had no such field, so we inject
//     `credential: {provider: static}` (anonymous/ambient auth) unless the v1
//     parameters already carry a credential block.
//   - v1-only oras knobs (cacheEnabled/cosignEnabled/ttl) are dropped since the
//     v2 registry-store does not model them.
func convertStore(s *v1.Store, rep *report.Reporter) (*v2.StoreOptions, error) {
	res := resourceID(v1.KindStore, s.Namespace, s.Name)
	if s.Spec.Address != "" {
		rep.Warnf(res, "spec.address=%q dropped: v2 has no external-plugin path; port the plugin to the v2 Go model", s.Spec.Address)
	}
	if s.Spec.Source != nil {
		rep.Warnf(res, "spec.source (dynamic plugin %q) dropped: v2 has no OCI plugin download; port the plugin to the v2 Go model", s.Spec.Source.Artifact)
	}

	params, err := rawToMap(s.Spec.Parameters)
	if err != nil {
		return nil, err
	}

	for _, k := range droppedStoreParams {
		if _, ok := params[k]; ok {
			delete(params, k)
			rep.Infof(res, "parameters.%s dropped: not modeled by the v2 registry-store", k)
		}
	}

	if _, ok := params["credential"]; !ok {
		params["credential"] = map[string]any{"provider": "static"}
		rep.Infof(res, "injected credential.provider=static (v1 store had no credential config; v2 registry-store requires one)")
	}

	raw, err := mapToRaw(params)
	if err != nil {
		return nil, err
	}
	return &v2.StoreOptions{
		Type:       StoreAlias(s.Spec.Name),
		Parameters: raw,
	}, nil
}
