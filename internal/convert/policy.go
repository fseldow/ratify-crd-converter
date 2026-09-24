package convert

import (
	v1 "github.com/fseldow/ratify-crd-converter/internal/apis/v1beta1"
	v2 "github.com/fseldow/ratify-crd-converter/internal/apis/v2beta1"
	"github.com/fseldow/ratify-crd-converter/internal/report"
)

// buildPolicyEnforcer produces the v2 policyEnforcer. v2 ships a single policy
// enforcer implementation ("threshold-policy") whose parameters reference each
// verifier by name via policy.rules[].verifierName. The v1 policy types
// (rego-policy / config-policy) and their parameters have no v2 equivalent, so
// we synthesize a threshold policy that references every verifier in the group.
// The original v1 policy type is surfaced as a warning so the user can review.
func buildPolicyEnforcer(policies []*v1.Policy, verifierNames []string, group string, rep *report.Reporter) *v2.PolicyEnforcerOptions {
	if p := choosePolicy(policies, group, rep); p != nil && p.Spec.Type != "" {
		rep.Infof("Policy["+group+"]", "v1 policy type %q mapped to v2 threshold-policy (v1 policy parameters are not portable)", p.Spec.Type)
	}
	if len(verifierNames) == 0 {
		return nil
	}

	rules := make([]any, 0, len(verifierNames))
	for _, n := range verifierNames {
		rules = append(rules, map[string]any{"verifierName": n})
	}
	raw, err := valueToRaw(map[string]any{
		"policy": map[string]any{"rules": rules},
	})
	if err != nil {
		return nil
	}
	return &v2.PolicyEnforcerOptions{
		Type:       "threshold-policy",
		Parameters: raw,
	}
}

// choosePolicy picks a single policy from a group, warning if more than one
// exists (v2 supports exactly one policy enforcer per executor).
func choosePolicy(policies []*v1.Policy, group string, rep *report.Reporter) *v1.Policy {
	if len(policies) == 0 {
		return nil
	}
	if len(policies) > 1 {
		names := make([]string, 0, len(policies))
		for _, p := range policies {
			names = append(names, p.Name)
		}
		rep.Warnf("Policy["+group+"]", "%d policies found %v; v2 allows one — using %q", len(policies), names, policies[0].Name)
	}
	return policies[0]
}
