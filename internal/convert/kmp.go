package convert

import (
	"fmt"

	v1 "github.com/fseldow/ratify-crd-converter/internal/apis/v1beta1"
	"github.com/fseldow/ratify-crd-converter/internal/report"
)

// certIndex maps a KMP/CertificateStore name to its converted v2 certificate
// entry, and tracks which entries have been referenced by a verifier.
type certIndex struct {
	certs map[string]map[string]any
	used  map[string]bool
}

func buildCertIndex(kmps []*v1.KeyManagementProvider, certStores []*v1.CertificateStore, rep *report.Reporter) *certIndex {
	idx := &certIndex{certs: map[string]map[string]any{}, used: map[string]bool{}}
	for _, k := range kmps {
		res := resourceID(v1.KindKMP, k.Namespace, k.Name)
		cert, err := kmpToCertificate(k.Spec.Type, k.Spec.Parameters.Raw, res, rep)
		if err != nil {
			rep.Warnf(res, "skipped: %v", err)
			continue
		}
		idx.certs[k.Name] = cert
	}
	for _, c := range certStores {
		res := resourceID(v1.KindCertificateStore, c.Namespace, c.Name)
		cert, err := kmpToCertificate(c.Spec.Provider, c.Spec.Parameters.Raw, res, rep)
		if err != nil {
			rep.Warnf(res, "skipped: %v", err)
			continue
		}
		idx.certs[c.Name] = cert
	}
	return idx
}

// kmpToCertificate converts a v1 KMP/CertStore (type + raw params) into a v2
// certificates[] entry.
func kmpToCertificate(kmpType string, rawParams []byte, res string, rep *report.Reporter) (map[string]any, error) {
	params := map[string]any{}
	if len(rawParams) > 0 {
		if err := jsonUnmarshal(rawParams, &params); err != nil {
			return nil, err
		}
	}
	switch kmpType {
	case "inline":
		val, _ := params["value"].(string)
		if val == "" {
			return nil, fmt.Errorf("inline provider has empty parameters.value")
		}
		return map[string]any{
			"type":   "ca",
			"inline": map[string]any{"certs": val},
		}, nil
	case "azurekeyvault":
		akv := map[string]any{}
		if v, ok := params["vaultURI"]; ok {
			akv["vaultURL"] = v
		}
		if v, ok := params["certificates"]; ok {
			akv["certificates"] = v
		}
		if _, ok := params["tenantID"]; ok {
			rep.Warnf(res, "parameters.tenantID dropped: v2 uses workload identity")
		}
		if _, ok := params["clientID"]; ok {
			rep.Warnf(res, "parameters.clientID dropped: v2 uses workload identity")
		}
		return map[string]any{
			"type":          "ca",
			"azurekeyvault": akv,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported provider type %q", kmpType)
	}
}

// resolveCertReferences rewrites a verifier's parameters: it resolves
// verificationCertStores references into an inlined certificates[] list and
// removes the reference field. Referenced names are marked used in the index.
func resolveCertReferences(params map[string]any, idx *certIndex, res string, rep *report.Reporter) {
	refField, ok := params["verificationCertStores"]
	if !ok {
		return
	}
	names := collectCertStoreRefs(refField)
	var certs []any
	for _, name := range names {
		cert, found := idx.certs[name]
		if !found {
			rep.Warnf(res, "verificationCertStores references unknown provider %q", name)
			continue
		}
		idx.used[name] = true
		certs = append(certs, cert)
	}
	if len(certs) > 0 {
		params["certificates"] = certs
	}
	delete(params, "verificationCertStores")
}

// collectCertStoreRefs flattens the notation verificationCertStores structure
// (map[certType]map[group][]name) into a flat, de-duplicated name list.
func collectCertStoreRefs(v any) []string {
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	byType, ok := v.(map[string]any)
	if !ok {
		return out
	}
	for _, group := range byType {
		switch g := group.(type) {
		case map[string]any: // v1beta1: certType -> group -> [names]
			for _, names := range g {
				for _, n := range toStringSlice(names) {
					add(n)
				}
			}
		case []any: // v1alpha1: certType -> [names]
			for _, n := range toStringSlice(g) {
				add(n)
			}
		}
	}
	return out
}

func toStringSlice(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// reportOrphans warns about KMP/CertStore entries never referenced by a verifier.
func (idx *certIndex) reportOrphans(rep *report.Reporter) {
	for name := range idx.certs {
		if !idx.used[name] {
			rep.Warnf("KeyManagementProvider/"+name, "not referenced by any verifier; not inlined into any Executor")
		}
	}
}
