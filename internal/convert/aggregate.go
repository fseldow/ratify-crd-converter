package convert

import (
	"sort"

	v1 "github.com/fseldow/ratify-crd-converter/internal/apis/v1beta1"
	v2 "github.com/fseldow/ratify-crd-converter/internal/apis/v2beta1"
	"github.com/fseldow/ratify-crd-converter/internal/loader"
	"github.com/fseldow/ratify-crd-converter/internal/report"
)

// Options configures aggregation behaviour.
type Options struct {
	// DefaultScopes is used when scopes cannot be derived from verifiers.
	// Empty falls back to ["*"].
	DefaultScopes []string
	// Concurrency sets Executor.spec.concurrency (0 = omit / use v2 default).
	Concurrency int
	// Name is the metadata.name for generated executors.
	Name string
}

// Result holds the generated v2 executors.
type Result struct {
	Executors           []*v2.Executor
	NamespacedExecutors []*v2.NamespacedExecutor
}

// group buckets v1 resources sharing a scope (cluster or one namespace).
type group struct {
	namespace string // "" = cluster
	stores    []*v1.Store
	verifiers []*v1.Verifier
	policies  []*v1.Policy
}

// Aggregate converts a loaded v1 Bundle into v2 executors, one per scope group.
func Aggregate(b *loader.Bundle, opts Options, rep *report.Reporter) (*Result, error) {
	idx := buildCertIndex(b.KMPs, b.CertStores, rep)
	groups := bucket(b)

	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	res := &Result{}
	for _, k := range keys {
		g := groups[k]
		spec, err := buildSpec(g, idx, opts, k, rep)
		if err != nil {
			return nil, err
		}
		name := opts.Name
		if name == "" {
			name = "executor"
		}
		if g.namespace == "" {
			e := v2.NewExecutor(name)
			e.Spec = *spec
			res.Executors = append(res.Executors, e)
		} else {
			e := v2.NewNamespacedExecutor(name, g.namespace)
			e.Spec = *spec
			res.NamespacedExecutors = append(res.NamespacedExecutors, e)
		}
	}

	idx.reportOrphans(rep)
	return res, nil
}

func bucket(b *loader.Bundle) map[string]*group {
	groups := map[string]*group{}
	get := func(ns string) *group {
		key := scopeGroup(ns)
		g, ok := groups[key]
		if !ok {
			g = &group{namespace: ns}
			groups[key] = g
		}
		return g
	}
	for _, s := range b.Stores {
		get(s.Namespace).stores = append(get(s.Namespace).stores, s)
	}
	for _, v := range b.Verifiers {
		get(v.Namespace).verifiers = append(get(v.Namespace).verifiers, v)
	}
	for _, p := range b.Policies {
		get(p.Namespace).policies = append(get(p.Namespace).policies, p)
	}
	return groups
}

func buildSpec(g *group, idx *certIndex, opts Options, groupKey string, rep *report.Reporter) (*v2.ExecutorSpec, error) {
	spec := &v2.ExecutorSpec{Concurrency: opts.Concurrency}

	for _, s := range g.stores {
		so, err := convertStore(s, rep)
		if err != nil {
			return nil, err
		}
		spec.Stores = append(spec.Stores, so)
	}
	for _, v := range g.verifiers {
		vo, err := convertVerifier(v, idx, rep)
		if err != nil {
			return nil, err
		}
		spec.Verifiers = append(spec.Verifiers, vo)
	}
	if p := choosePolicy(g.policies, groupKey, rep); p != nil {
		spec.PolicyEnforcer = convertPolicy(p, rep)
	}

	scopes, _ := extractScopes(g.verifiers, groupKey, rep)
	if len(scopes) == 0 {
		if len(opts.DefaultScopes) > 0 {
			scopes = opts.DefaultScopes
		} else {
			scopes = wildcardScope
			rep.Warnf("Executor["+groupKey+"]", "no scope derivable from verifiers; defaulting to [\"*\"] (use --scope to override)")
		}
	}
	spec.Scopes = scopes

	if len(spec.Stores) == 0 {
		rep.Warnf("Executor["+groupKey+"]", "no stores: v2 Executor requires at least one store")
	}
	if len(spec.Verifiers) == 0 {
		rep.Warnf("Executor["+groupKey+"]", "no verifiers: v2 Executor requires at least one verifier")
	}
	return spec, nil
}
