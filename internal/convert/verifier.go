package convert

import (
	v1 "github.com/fseldow/ratify-crd-converter/internal/apis/v1beta1"
	v2 "github.com/fseldow/ratify-crd-converter/internal/apis/v2beta1"
	"github.com/fseldow/ratify-crd-converter/internal/report"
)

// convertVerifier maps a v1 Verifier to a v2 VerifierOptions:
//   - metadata.name becomes the unique instance name
//   - spec.name becomes the verifier type
//   - verificationCertStores references are inlined into parameters.certificates[]
//   - for notation, the v1 trustPolicyDoc is translated into the v2 shape
//     (scopes + trustedIdentities + certificates); v2 rebuilds the trust policy
//     document itself and does not accept the v1 trustPolicyDoc/artifactTypes keys
//   - address/source are dropped with a warning when non-empty
func convertVerifier(v *v1.Verifier, idx *certIndex, rep *report.Reporter) (*v2.VerifierOptions, error) {
	res := resourceID(v1.KindVerifier, v.Namespace, v.Name)
	if v.Spec.Address != "" {
		rep.Warnf(res, "spec.address=%q dropped: v2 has no external-plugin path", v.Spec.Address)
	}
	if v.Spec.Source != nil {
		rep.Warnf(res, "spec.source (dynamic plugin %q) dropped: port plugin to v2 Go model", v.Spec.Source.Artifact)
	}

	params, err := rawToMap(v.Spec.Parameters)
	if err != nil {
		return nil, err
	}

	// Inline referenced KMP/CertStore certificates and drop verificationCertStores.
	resolveCertReferences(params, idx, res, rep)

	switch v.Spec.Name {
	case "notation":
		rewriteNotationParams(params, res, rep)
	default:
		// artifactTypes has no v2 equivalent for any built-in verifier.
		delete(params, "artifactTypes")
	}

	name := v.Name
	if name == "" {
		name = v.Spec.Name
	}

	raw, err := mapToRaw(params)
	if err != nil {
		return nil, err
	}
	return &v2.VerifierOptions{
		Name:       name,
		Type:       v.Spec.Name,
		Parameters: raw,
	}, nil
}

// rewriteNotationParams translates v1 notation parameters into the v2 notation
// options shape. v2 only accepts `scopes`, `trustedIdentities` and
// `certificates`; it reconstructs the trust policy document internally. The v1
// `trustPolicyDoc` carries registryScopes and trustedIdentities per trust
// policy, which we hoist to the top level (unioned across trust policies).
func rewriteNotationParams(params map[string]any, res string, rep *report.Reporter) {
	delete(params, "artifactTypes")

	doc, ok := params["trustPolicyDoc"].(map[string]any)
	if !ok {
		return
	}
	tps, _ := doc["trustPolicies"].([]any)

	scopes := newStringSet()
	identities := newStringSet()
	for _, tp := range tps {
		m, ok := tp.(map[string]any)
		if !ok {
			continue
		}
		for _, s := range toStringSlice(m["registryScopes"]) {
			scopes.add(s)
		}
		for _, id := range toStringSlice(m["trustedIdentities"]) {
			identities.add(id)
		}
	}

	if _, exists := params["scopes"]; !exists && len(scopes.ordered) > 0 {
		params["scopes"] = scopes.slice()
	}
	if _, exists := params["trustedIdentities"]; !exists && len(identities.ordered) > 0 {
		params["trustedIdentities"] = identities.slice()
	}

	delete(params, "trustPolicyDoc")
	rep.Infof(res, "translated notation trustPolicyDoc into v2 scopes/trustedIdentities (v2 rebuilds the trust policy document)")
}
