package policyparams

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fseldow/ratify-crd-converter/internal/loader"
)

const uami = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/ii"

func load(t *testing.T, yamls ...string) *loader.Bundle {
	t.Helper()
	p := filepath.Join(t.TempDir(), "in.yaml")
	if err := os.WriteFile(p, []byte(strings.Join(yamls, "\n---\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := loader.LoadPaths([]string{p})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func hasFinding(r *Result, o Outcome, substr string) bool {
	for _, f := range r.Findings {
		if f.Outcome == o && strings.Contains(f.Resource+" "+f.Reason, substr) {
			return true
		}
	}
	return false
}

func TestConvertAKSBundle(t *testing.T) {
	b, err := loader.LoadPaths([]string{"../../testdata/v1_aks_akv.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := Convert(b, Options{IdentityResourceID: uami})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	p := res.Parameters
	if p.Effect.Value != "Audit" {
		t.Errorf("effect must be pinned to Audit, got %q", p.Effect.Value)
	}
	if len(p.Namespaces.Value) != 0 {
		t.Errorf("cluster-scoped v1 should yield empty namespaces, got %v", p.Namespaces.Value)
	}
	wantScopes := []string{"myregistry.azurecr.io/api", "myregistry.azurecr.io/app", "partnerregistry.azurecr.io/vendor/app"}
	if strings.Join(p.Scopes.Value, ",") != strings.Join(wantScopes, ",") {
		t.Errorf("scopes: got %v want %v", p.Scopes.Value, wantScopes)
	}
	if len(p.Certificates.Value) != 2 || p.Certificates.Value[0].Type != "ca" || p.Certificates.Value[1].Type != "tsa" {
		t.Fatalf("certificates: %+v", p.Certificates.Value)
	}
	ca := p.Certificates.Value[0].AzureKeyVault
	if ca.VaultURL != "https://myvault.vault.azure.net" || len(ca.Certificates) != 2 || ca.Certificates[1].Version == "" {
		t.Errorf("ca entry: %+v", ca)
	}
	if p.Certificates.Value[1].AzureKeyVault.Certificates[0].Name != "tsa-root" {
		t.Errorf("legacy certificates block not parsed: %+v", p.Certificates.Value[1])
	}
	if p.IdentityResourceID.Value != uami {
		t.Errorf("identity: %q", p.IdentityResourceID.Value)
	}
	if res.Count(Unsupported) != 0 || res.Count(NeedsReview) != 0 {
		t.Errorf("unexpected findings: %+v", res.Findings)
	}
	if strings.Join(res.ClientIDs, ",") != "22222222-2222-2222-2222-222222222222" {
		t.Errorf("client IDs: %v", res.ClientIDs)
	}

	// The output must be a valid parameters file and idempotent.
	out1, _ := Render(p)
	res2, _ := Convert(b, Options{IdentityResourceID: uami})
	out2, _ := Render(res2.Parameters)
	if !bytes.Equal(out1, out2) {
		t.Error("output is not deterministic")
	}
	var generic map[string]map[string]any
	if err := json.Unmarshal(out1, &generic); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"effect", "namespaces", "excludedNamespaces", "scopes", "certificates", "trustedIdentities", "identityResourceId"} {
		if _, ok := generic[k]["value"]; !ok {
			t.Errorf("parameter %q missing value envelope", k)
		}
	}
}

func TestIdentityResolution(t *testing.T) {
	b, _ := loader.LoadPaths([]string{"../../testdata/v1_aks_akv.yaml"})

	res, _ := Convert(b, Options{})
	if !strings.Contains(res.Parameters.IdentityResourceID.Value, "22222222-2222-2222-2222-222222222222") ||
		!hasFinding(res, NeedsReview, "identityResourceId") {
		t.Errorf("unresolved identity should be a NeedsReview placeholder: %+v", res.Parameters.IdentityResourceID)
	}

	res, _ = Convert(b, Options{ResolveIdentity: func(id string) (string, error) { return uami, nil }})
	if res.Parameters.IdentityResourceID.Value != uami || res.Count(NeedsReview) != 0 {
		t.Errorf("resolver result not used: %+v %+v", res.Parameters.IdentityResourceID, res.Findings)
	}
}

const akvKMP = `apiVersion: config.ratify.deislabs.io/v1beta1
kind: KeyManagementProvider
metadata: {name: kmp}
spec:
  type: azurekeyvault
  parameters:
    vaultURI: https://myvault.vault.azure.net/
    certificates: [{name: root}]
    clientID: 22222222-2222-2222-2222-222222222222`

func notationVerifier(policies string) string {
	return `apiVersion: config.ratify.deislabs.io/v1beta1
kind: Verifier
metadata: {name: notation}
spec:
  name: notation
  parameters:
    verificationCertStores:
      ca:
        certs: [kmp]
    trustPolicyDoc:
      version: "1.0"
      trustPolicies:
` + policies
}

func TestSignatureVerificationLevels(t *testing.T) {
	cases := []struct {
		sv      string
		outcome Outcome
	}{
		{"{level: strict}", Migrated},
		{"{level: strict, override: {revocation: log}}", NeedsReview},
		{"{level: permissive}", NeedsReview},
		{"{level: audit}", NeedsReview},
		{"{level: strict, verifyTimestamp: afterCertExpiry}", NeedsReview},
		{"{level: skip}", Unsupported},
	}
	for _, tc := range cases {
		b := load(t, akvKMP, notationVerifier(`        - name: p
          registryScopes: [myregistry.azurecr.io/app]
          signatureVerification: `+tc.sv+`
          trustStores: [ca:certs]
          trustedIdentities: ["*"]
`))
		res, _ := Convert(b, Options{IdentityResourceID: uami})
		if !hasFinding(res, tc.outcome, "Verifier/notation#p") {
			t.Errorf("%s: want %s, got %+v", tc.sv, tc.outcome, res.Findings)
		}
	}
}

func TestWildcardScopeUnsupported(t *testing.T) {
	b := load(t, akvKMP, notationVerifier(`        - name: p
          registryScopes: ["*"]
          signatureVerification: {level: strict}
          trustStores: [ca:certs]
          trustedIdentities: ["*"]
`))
	res, err := Convert(b, Options{IdentityResourceID: uami})
	if err != nil {
		t.Fatal(err)
	}
	if !hasFinding(res, Unsupported, `"*"`) || !hasFinding(res, Unsupported, "Parameter/scopes") {
		t.Errorf("wildcard scope must be Unsupported: %+v", res.Findings)
	}
}

func TestDifferentTrustMaterialIsNotMerged(t *testing.T) {
	b := load(t, akvKMP, notationVerifier(`        - name: release
          registryScopes: [myregistry.azurecr.io/app]
          signatureVerification: {level: strict}
          trustStores: [ca:certs]
          trustedIdentities: ["x509.subject: C=US, ST=WA, O=Contoso, CN=release"]
        - name: ci
          registryScopes: [myregistry.azurecr.io/dev]
          signatureVerification: {level: strict}
          trustStores: [ca:certs]
          trustedIdentities: ["x509.subject: C=US, ST=WA, O=Contoso, CN=ci"]
`))
	res, err := Convert(b, Options{IdentityResourceID: uami})
	if err == nil || res.Parameters != nil {
		t.Fatalf("trust policies with different identities must not be merged into one assignment")
	}
	if !hasFinding(res, NeedsReview, "separate V2 assignment") {
		t.Errorf("missing split finding: %+v", res.Findings)
	}
}

func TestInlineAndCosignUnsupported(t *testing.T) {
	inline := `apiVersion: config.ratify.deislabs.io/v1beta1
kind: KeyManagementProvider
metadata: {name: inline}
spec:
  type: inline
  parameters: {value: "-----BEGIN CERTIFICATE-----"}`
	cosign := `apiVersion: config.ratify.deislabs.io/v1beta1
kind: Verifier
metadata: {name: cosign}
spec:
  name: cosign
  parameters: {}`
	b := load(t, akvKMP, inline, cosign, notationVerifier(`        - name: p
          registryScopes: [myregistry.azurecr.io/app]
          signatureVerification: {level: strict}
          trustStores: [ca:certs]
          trustedIdentities: ["*"]
`))
	res, err := Convert(b, Options{IdentityResourceID: uami})
	if err != nil {
		t.Fatal(err)
	}
	if !hasFinding(res, Unsupported, "Verifier/cosign") {
		t.Errorf("cosign verifier must be Unsupported: %+v", res.Findings)
	}
	if !hasFinding(res, Dropped, "KeyManagementProvider/inline") {
		t.Errorf("unreferenced inline KMP must be accounted for: %+v", res.Findings)
	}
}

func TestNamespacedCertificateStoreReference(t *testing.T) {
	certStore := `apiVersion: config.ratify.deislabs.io/v1beta1
kind: CertificateStore
metadata: {name: certstore-akv, namespace: gatekeeper-system}
spec:
  provider: azurekeyvault
  parameters:
    vaultURI: https://myvault.vault.azure.net/
    certificates: |
      array:
      - |
        certificateName: root
    clientID: 22222222-2222-2222-2222-222222222222`
	for _, ref := range []string{"certstore-akv", "gatekeeper-system/certstore-akv"} {
		verifier := `apiVersion: config.ratify.deislabs.io/v1beta1
kind: Verifier
metadata: {name: notation}
spec:
  name: notation
  parameters:
    verificationCertStores:
      certs: [` + ref + `]
    trustPolicyDoc:
      version: "1.0"
      trustPolicies:
        - name: p
          registryScopes: [myregistry.azurecr.io/app]
          signatureVerification: {level: strict}
          trustStores: [ca:certs]
          trustedIdentities: ["*"]`
		res, err := Convert(load(t, certStore, verifier), Options{IdentityResourceID: uami})
		if err != nil {
			t.Fatalf("%s: %v (%+v)", ref, err, res.Findings)
		}
		if got := res.Parameters.Certificates.Value; len(got) != 1 || got[0].AzureKeyVault.Certificates[0].Name != "root" {
			t.Errorf("%s: certificates %+v", ref, got)
		}
		if !hasFinding(res, Migrated, "CertificateStore/gatekeeper-system/certstore-akv") {
			t.Errorf("%s: certificate store should be Migrated: %+v", ref, res.Findings)
		}
	}
}

func TestInvalidTrustedIdentitiesNotWidened(t *testing.T) {
	for _, ids := range []string{"[]", `["*", "x509.subject: C=US, ST=WA, O=Contoso"]`} {
		b := load(t, akvKMP, notationVerifier(`        - name: p
          registryScopes: [myregistry.azurecr.io/app]
          signatureVerification: {level: strict}
          trustStores: [ca:certs]
          trustedIdentities: `+ids+`
`))
		res, err := Convert(b, Options{IdentityResourceID: uami})
		if err == nil || !hasFinding(res, Unsupported, "trustedIdentities") {
			t.Errorf("%s: must be Unsupported, not widened to *: %+v", ids, res.Findings)
		}
	}
}

func TestMultipleClientIDsNeedReview(t *testing.T) {
	store := `apiVersion: config.ratify.deislabs.io/v1beta1
kind: Store
metadata: {name: oras}
spec:
  name: oras
  parameters:
    authProvider: {name: azureWorkloadIdentity, clientID: 33333333-3333-3333-3333-333333333333}`
	b := load(t, akvKMP, store, notationVerifier(`        - name: p
          registryScopes: [myregistry.azurecr.io/app]
          signatureVerification: {level: strict}
          trustStores: [ca:certs]
          trustedIdentities: ["*"]
`))
	res, err := Convert(b, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !hasFinding(res, NeedsReview, "different client IDs") {
		t.Errorf("different KMP/store client IDs must be NeedsReview: %+v", res.Findings)
	}
}
