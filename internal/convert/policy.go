package convert

import (
	v1 "github.com/fseldow/ratify-crd-converter/internal/apis/v1beta1"
	v2 "github.com/fseldow/ratify-crd-converter/internal/apis/v2beta1"
	"github.com/fseldow/ratify-crd-converter/internal/report"
)

// convertPolicy maps a v1 Policy to a v2 PolicyEnforcerOptions. v2 allows only
// one policy enforcer; callers pass the chosen policy.
func convertPolicy(p *v1.Policy, rep *report.Reporter) *v2.PolicyEnforcerOptions {
	return &v2.PolicyEnforcerOptions{
		Type:       p.Spec.Type,
		Parameters: p.Spec.Parameters,
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
