package convert

import (
	"sort"
	"strings"

	v1 "github.com/fseldow/ratify-crd-converter/internal/apis/v1beta1"
	"github.com/fseldow/ratify-crd-converter/internal/report"
)

// wildcardScope is used when no scope can be derived or trust policies disagree.
var wildcardScope = []string{"*"}

// extractScopes derives Executor.spec.scopes from a group of verifiers.
//   - notation: parameters.trustPolicyDoc.trustPolicies[].registryScopes
//   - cosign:   parameters.trustPolicies[].scopes
//
// Rules: union identical trust-policy scope sets; if any scope is "*", or trust
// policies disagree, fall back to ["*"]. Returns the fallback flag for logging.
func extractScopes(verifiers []*v1.Verifier, group string, rep *report.Reporter) ([]string, bool) {
	var signatures []string
	sigToSet := map[string][]string{}
	sawAny := false

	for _, v := range verifiers {
		params, err := rawToMap(v.Spec.Parameters)
		if err != nil {
			continue
		}
		var sets [][]string
		switch v.Spec.Name {
		case "notation":
			sets = notationScopeSets(params)
		case "cosign":
			sets = cosignScopeSets(params)
		}
		for _, s := range sets {
			sawAny = true
			for _, sc := range s {
				if sc == "*" {
					rep.Infof("Executor["+group+"]", "wildcard scope found; using [\"*\"]")
					return wildcardScope, true
				}
			}
			norm := normalizeSet(s)
			sig := strings.Join(norm, "\n")
			if _, ok := sigToSet[sig]; !ok {
				sigToSet[sig] = norm
				signatures = append(signatures, sig)
			}
		}
	}

	if !sawAny {
		return nil, false
	}
	if len(signatures) > 1 {
		rep.Warnf("Executor["+group+"]", "trust policies define differing scopes; defaulting to [\"*\"]")
		return wildcardScope, true
	}
	return sigToSet[signatures[0]], false
}

func notationScopeSets(params map[string]any) [][]string {
	doc, ok := params["trustPolicyDoc"].(map[string]any)
	if !ok {
		return nil
	}
	return policyScopeSets(doc["trustPolicies"], "registryScopes")
}

func cosignScopeSets(params map[string]any) [][]string {
	return policyScopeSets(params["trustPolicies"], "scopes")
}

func policyScopeSets(trustPolicies any, field string) [][]string {
	arr, ok := trustPolicies.([]any)
	if !ok {
		return nil
	}
	var sets [][]string
	for _, tp := range arr {
		m, ok := tp.(map[string]any)
		if !ok {
			continue
		}
		if s := toStringSlice(m[field]); len(s) > 0 {
			sets = append(sets, s)
		}
	}
	return sets
}

func normalizeSet(s []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(s))
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}
