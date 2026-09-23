package convert

import (
	v1 "github.com/fseldow/ratify-crd-converter/internal/apis/v1beta1"
	v2 "github.com/fseldow/ratify-crd-converter/internal/apis/v2beta1"
	"github.com/fseldow/ratify-crd-converter/internal/report"
)

// convertVerifier maps a v1 Verifier to a v2 VerifierOptions:
//   - metadata.name becomes the unique instance name
//   - spec.name becomes the verifier type
//   - artifactTypes is folded into parameters (no top-level field in v2)
//   - verificationCertStores references are inlined into parameters.certificates[]
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

	resolveCertReferences(params, idx, res, rep)

	if v.Spec.ArtifactTypes != "" {
		params["artifactTypes"] = v.Spec.ArtifactTypes
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
