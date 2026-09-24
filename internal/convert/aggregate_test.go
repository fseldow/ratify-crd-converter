package convert

import (
	"testing"

	"github.com/fseldow/ratify-crd-converter/internal/loader"
	"github.com/fseldow/ratify-crd-converter/internal/report"
)

func TestAggregateBundle(t *testing.T) {
	b, err := loader.LoadPaths([]string{"../../testdata/v1_bundle.yaml"})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	rep := report.New()
	res, err := Aggregate(b, Options{Name: "executor"}, rep)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}

	if len(res.Executors) != 1 {
		t.Fatalf("want 1 cluster executor, got %d", len(res.Executors))
	}
	e := res.Executors[0]

	if got := len(e.Spec.Stores); got != 1 {
		t.Fatalf("want 1 store, got %d", got)
	}
	if e.Spec.Stores[0].Type != "registry-store" {
		t.Errorf("store type: want registry-store (aliased from oras), got %q", e.Spec.Stores[0].Type)
	}
	storeParams := string(e.Spec.Stores[0].Parameters.Raw)
	if !contains(storeParams, "credential") || !contains(storeParams, "static") {
		t.Errorf("store should inject credential.provider=static, got: %s", storeParams)
	}
	if contains(storeParams, "cacheEnabled") || contains(storeParams, "ttl") {
		t.Errorf("v1-only store knobs should be dropped, got: %s", storeParams)
	}

	if got := len(e.Spec.Verifiers); got != 1 {
		t.Fatalf("want 1 verifier, got %d", got)
	}
	v := e.Spec.Verifiers[0]
	if v.Type != "notation" || v.Name != "verifier-notation" {
		t.Errorf("verifier name/type: got %q/%q", v.Name, v.Type)
	}
	params := string(v.Parameters.Raw)
	if !contains(params, "certificates") || !contains(params, "inline") {
		t.Errorf("verifier params should inline certificates[], got: %s", params)
	}
	if contains(params, "verificationCertStores") {
		t.Errorf("verificationCertStores should be stripped, got: %s", params)
	}
	if contains(params, "trustPolicyDoc") {
		t.Errorf("notation trustPolicyDoc should be stripped, got: %s", params)
	}
	if contains(params, "artifactTypes") {
		t.Errorf("artifactTypes should be dropped for notation, got: %s", params)
	}
	if !contains(params, "scopes") || !contains(params, "trustedIdentities") {
		t.Errorf("notation params should carry scopes + trustedIdentities, got: %s", params)
	}

	if e.Spec.PolicyEnforcer == nil || e.Spec.PolicyEnforcer.Type != "threshold-policy" {
		t.Fatalf("policyEnforcer should be threshold-policy: %+v", e.Spec.PolicyEnforcer)
	}
	polParams := string(e.Spec.PolicyEnforcer.Parameters.Raw)
	if !contains(polParams, "verifierName") || !contains(polParams, "verifier-notation") {
		t.Errorf("threshold policy should reference verifier by name, got: %s", polParams)
	}

	if len(e.Spec.Scopes) != 1 || e.Spec.Scopes[0] != "myregistry.io/prod" {
		t.Errorf("scopes should be derived from notation trust policy, got %v", e.Spec.Scopes)
	}

	if rep.HasWarnings() {
		t.Logf("report:\n%s", rep.String())
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
