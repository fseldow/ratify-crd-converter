// Package policyparams converts Ratify v1 CRs into the Azure Policy assignment
// parameters of the AKS Image Integrity V2 verification policy
// (AKS-NotarySignatureVerification). The output is a single parameters JSON
// that can be passed to `az policy assignment create --params @file.json`.
package policyparams

// Outcome classifies how a v1 resource (or field) was carried into V2.
type Outcome string

const (
	Migrated    Outcome = "Migrated"
	NeedsReview Outcome = "NeedsReview"
	Unsupported Outcome = "Unsupported"
	Dropped     Outcome = "Dropped"
)

// Finding is one line of the migration report.
type Finding struct {
	Resource string  `json:"resource"`
	Outcome  Outcome `json:"outcome"`
	Reason   string  `json:"reason"`
}

// Value wraps a parameter value in the Azure Policy parameters-file envelope.
type Value[T any] struct {
	Value T `json:"value"`
}

// Parameters is the assignment parameter set of AKS-NotarySignatureVerification.
// Field order is fixed so the rendered JSON is byte-for-byte stable.
type Parameters struct {
	Effect             Value[string]        `json:"effect"`
	Namespaces         Value[[]string]      `json:"namespaces"`
	ExcludedNamespaces Value[[]string]      `json:"excludedNamespaces"`
	Scopes             Value[[]string]      `json:"scopes"`
	Certificates       Value[[]Certificate] `json:"certificates"`
	TrustedIdentities  Value[[]string]      `json:"trustedIdentities"`
	IdentityResourceID Value[string]        `json:"identityResourceId"`
}

// Certificate is one trust-store entry in upstream notation verifier format.
type Certificate struct {
	Type          string        `json:"type"`
	AzureKeyVault AzureKeyVault `json:"azurekeyvault"`
}

type AzureKeyVault struct {
	VaultURL     string    `json:"vaultURL"`
	Certificates []CertRef `json:"certificates"`
}

type CertRef struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// Result is the conversion output.
type Result struct {
	Parameters *Parameters
	Findings   []Finding
	// ClientIDs are the v1 workload-identity client IDs the identity was derived from.
	ClientIDs []string
}

// Count returns the number of findings with the given outcome.
func (r *Result) Count(o Outcome) int {
	n := 0
	for _, f := range r.Findings {
		if f.Outcome == o {
			n++
		}
	}
	return n
}
