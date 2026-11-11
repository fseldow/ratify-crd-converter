package policyparams

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"

	v1 "github.com/fseldow/ratify-crd-converter/internal/apis/v1beta1"
	"github.com/fseldow/ratify-crd-converter/internal/loader"
)

// DefaultExcludedNamespaces mirrors the policy definition default.
var DefaultExcludedNamespaces = []string{"kube-system", "gatekeeper-system"}

// Effect is pinned to Audit: switching to Deny is a deliberate customer edit
// after comparing V1 and V2 results.
const Effect = "Audit"

// Order in which certificate types are rendered.
var certTypeOrder = []string{"ca", "signingAuthority", "tsa"}

var (
	scopePattern       = regexp.MustCompile(`^(\*\.[a-z0-9]([a-z0-9.-]*[a-z0-9])?(:[0-9]+)?|[a-z0-9]([a-z0-9.-]*[a-z0-9])?(:[0-9]+)?(/[a-z0-9]+([._-][a-z0-9]+)*)*)$`)
	vaultURLPattern    = regexp.MustCompile(`^https://[a-z0-9-]+\.vault\.azure\.net/?$`)
	identityIDPattern  = regexp.MustCompile(`^/subscriptions/[^/]+/resource[Gg]roups/[^/]+/providers/[Mm]icrosoft\.[Mm]anaged[Ii]dentity/user[Aa]ssigned[Ii]dentities/[^/]+$`)
	workloadIdentities = map[string]bool{"azureWorkloadIdentity": true}
)

// Options configures the conversion.
type Options struct {
	// IdentityResourceID overrides identity resolution with an explicit UAMI resource ID.
	IdentityResourceID string
	// ExcludedNamespaces overrides the policy default when non-nil.
	ExcludedNamespaces []string
	// ResolveIdentity maps a workload-identity client ID to a UAMI resource ID.
	// Optional; when nil or it returns "", the identity is left for review.
	ResolveIdentity func(clientID string) (string, error)
}

// kmpInfo is a v1 KeyManagementProvider / CertificateStore reduced to what V2 can express.
type kmpInfo struct {
	res       string
	vaultURL  string
	certs     []CertRef
	clientID  string
	supported bool
	reason    string
}

// assignment accumulates trust policies that share identical trust material.
type assignment struct {
	namespaces map[string]bool // "" = cluster-wide
	scopes     map[string]bool
	identities []string
	certs      map[string]map[string][]CertRef // type -> vaultURL -> certs
	clientIDs  map[string]bool
	sources    []string
}

type converter struct {
	opts     Options
	findings []Finding
	touched  map[string]bool
	kmps     map[string]*kmpInfo // key: namespace/name ("" namespace = cluster)
	used     map[string]bool
}

// Convert derives a single AKS-NotarySignatureVerification parameter set from a
// v1 bundle. It returns an error only when no single assignment can represent
// the input; per-field problems are reported as findings.
func Convert(b *loader.Bundle, opts Options) (*Result, error) {
	c := &converter{
		opts:    opts,
		touched: map[string]bool{},
		kmps:    map[string]*kmpInfo{},
		used:    map[string]bool{},
	}
	c.indexKMPs(b)

	groups := map[string]*assignment{}
	var order []string
	for _, v := range b.Verifiers {
		c.convertVerifier(v, groups, &order)
	}
	storeClients := c.convertStores(b.Stores)
	c.convertPolicies(b.Policies)
	c.reportUnusedKMPs()

	if len(order) == 0 {
		c.markRemaining(b)
		return &Result{Findings: c.sorted()}, fmt.Errorf("no notation trust policy could be converted into V2 policy parameters")
	}
	if len(order) > 1 {
		for _, k := range order {
			c.add("Assignment/"+k, NeedsReview, fmt.Sprintf("trust policies %s need a separate V2 assignment (different trust stores, trusted identities or namespaces)", strings.Join(groups[k].sources, ", ")))
		}
		c.markRemaining(b)
		return &Result{Findings: c.sorted()}, fmt.Errorf("v1 configuration maps to %d V2 assignments with different trust material; a single parameters JSON cannot represent it without widening trust", len(order))
	}

	a := groups[order[0]]
	for id := range storeClients {
		a.clientIDs[id] = true
	}
	params := &Parameters{
		Effect:             Value[string]{Effect},
		Namespaces:         Value[[]string]{c.namespaces(a)},
		ExcludedNamespaces: Value[[]string]{c.excludedNamespaces()},
		Scopes:             Value[[]string]{sortedKeys(a.scopes)},
		Certificates:       Value[[]Certificate]{c.certificates(a)},
		TrustedIdentities:  Value[[]string]{a.identities},
	}
	clientIDs := sortedKeys(a.clientIDs)
	params.IdentityResourceID = Value[string]{c.identity(clientIDs)}

	if len(params.Scopes.Value) == 0 {
		c.add("Parameter/scopes", Unsupported, "no V2-expressible image scope remains; V2 requires at least one scope")
	}
	c.markRemaining(b)
	return &Result{Parameters: params, Findings: c.sorted(), ClientIDs: clientIDs}, nil
}

// Render serializes parameters as indented JSON with a trailing newline.
func Render(p *Parameters) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(p); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (c *converter) add(res string, o Outcome, reason string) {
	c.touched[res] = true
	f := Finding{Resource: res, Outcome: o, Reason: reason}
	for _, e := range c.findings {
		if e == f {
			return
		}
	}
	c.findings = append(c.findings, f)
}

func (c *converter) sorted() []Finding {
	out := make([]Finding, len(c.findings))
	copy(out, c.findings)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Resource < out[j].Resource })
	return out
}

// markRemaining records Migrated for every v1 CR that produced no other finding,
// so each input CR is accounted for in the report.
func (c *converter) markRemaining(b *loader.Bundle) {
	mark := func(res string) {
		if !c.touched[res] {
			c.add(res, Migrated, "converted without loss")
		}
	}
	for _, v := range b.Verifiers {
		mark(resID(v.Kind, v1.KindVerifier, v.Namespace, v.Name))
	}
	for _, s := range b.Stores {
		mark(resID(s.Kind, v1.KindStore, s.Namespace, s.Name))
	}
	for _, k := range b.KMPs {
		mark(resID(k.Kind, v1.KindKMP, k.Namespace, k.Name))
	}
	for _, cs := range b.CertStores {
		mark(resID(cs.Kind, v1.KindCertificateStore, cs.Namespace, cs.Name))
	}
}

func resID(kind, fallback, ns, name string) string {
	if kind == "" {
		kind = fallback
	}
	if ns == "" {
		return kind + "/" + name
	}
	return kind + "/" + ns + "/" + name
}

// ---- KMP / CertificateStore ----

func (c *converter) indexKMPs(b *loader.Bundle) {
	for _, k := range b.KMPs {
		res := resID(k.Kind, v1.KindKMP, k.Namespace, k.Name)
		c.kmps[k.Namespace+"/"+k.Name] = parseKMP(res, k.Spec.Type, k.Spec.Parameters.Raw)
	}
	for _, cs := range b.CertStores {
		res := resID(cs.Kind, v1.KindCertificateStore, cs.Namespace, cs.Name)
		c.kmps[cs.Namespace+"/"+cs.Name] = parseKMP(res, cs.Spec.Provider, cs.Spec.Parameters.Raw)
	}
}

func parseKMP(res, typ string, raw []byte) *kmpInfo {
	info := &kmpInfo{res: res}
	params := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &params); err != nil {
			info.reason = fmt.Sprintf("cannot parse parameters: %v", err)
			return info
		}
	}
	switch typ {
	case "azurekeyvault":
	case "inline":
		info.reason = "inline certificates are not supported in V2; upload the certificate to Azure Key Vault and reference it"
		return info
	default:
		info.reason = fmt.Sprintf("provider type %q is not supported in V2 (Azure Key Vault only)", typ)
		return info
	}

	vault, _ := params["vaultURI"].(string)
	if vault == "" {
		vault, _ = params["vaultURL"].(string)
	}
	vault = strings.TrimRight(strings.ToLower(strings.TrimSpace(vault)), "/")
	if !vaultURLPattern.MatchString(vault) {
		info.reason = fmt.Sprintf("vault URL %q does not match the V2 schema (https://<name>.vault.azure.net)", vault)
		return info
	}
	info.vaultURL = vault
	info.clientID, _ = params["clientID"].(string)
	info.clientID = strings.ToLower(strings.TrimSpace(info.clientID))

	certs, err := parseCertRefs(params["certificates"])
	if err != nil {
		info.reason = err.Error()
		return info
	}
	if len(certs) == 0 {
		info.reason = "no certificates listed; V2 requires at least one Key Vault certificate (keys are not supported)"
		return info
	}
	info.certs = certs
	info.supported = true
	if _, ok := params["keys"]; ok {
		info.reason = "parameters.keys dropped: V2 only references Key Vault certificates"
	}
	return info
}

// parseCertRefs accepts both the v1beta1 list form ([{name, version}]) and the
// legacy string form ("array:\n- |\n  certificateName: x\n  certificateVersion: y").
func parseCertRefs(v any) ([]CertRef, error) {
	var items []any
	switch t := v.(type) {
	case nil:
		return nil, nil
	case []any:
		items = t
	case string:
		var doc struct {
			Array []string `json:"array"`
		}
		if err := yaml.Unmarshal([]byte(t), &doc); err != nil {
			return nil, fmt.Errorf("cannot parse legacy certificates block: %v", err)
		}
		for _, s := range doc.Array {
			m := map[string]any{}
			if err := yaml.Unmarshal([]byte(s), &m); err != nil {
				return nil, fmt.Errorf("cannot parse legacy certificate entry: %v", err)
			}
			items = append(items, m)
		}
	default:
		return nil, fmt.Errorf("unexpected certificates format %T", v)
	}
	var out []CertRef
	for _, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		name := firstString(m, "name", "certificateName")
		if name == "" {
			continue
		}
		out = append(out, CertRef{Name: name, Version: firstString(m, "version", "certificateVersion")})
	}
	return out, nil
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// lookupKMP resolves a verificationCertStores reference to its index key: an
// explicit "namespace/name", then the verifier's namespace, then cluster scope,
// then a unique match by name in any namespace.
func (c *converter) lookupKMP(ns, name string) (string, *kmpInfo) {
	if strings.Contains(name, "/") {
		if k, ok := c.kmps[name]; ok {
			return name, k
		}
		return "", nil
	}
	for _, key := range []string{ns + "/" + name, "/" + name} {
		if k, ok := c.kmps[key]; ok {
			return key, k
		}
	}
	var found string
	for key := range c.kmps {
		if strings.HasSuffix(key, "/"+name) {
			if found != "" {
				return "", nil
			}
			found = key
		}
	}
	if found == "" {
		return "", nil
	}
	return found, c.kmps[found]
}

func (c *converter) reportUnusedKMPs() {
	keys := make([]string, 0, len(c.kmps))
	for k := range c.kmps {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !c.used[k] {
			c.add(c.kmps[k].res, Dropped, "not referenced by any converted notation trust policy")
		}
	}
}

// ---- Verifier ----

func (c *converter) convertVerifier(v *v1.Verifier, groups map[string]*assignment, order *[]string) {
	res := resID(v.Kind, v1.KindVerifier, v.Namespace, v.Name)
	if v.Spec.Name != "notation" {
		c.add(res, Unsupported, fmt.Sprintf("verifier type %q is not supported by AKS-NotarySignatureVerification; images in its scopes will not be verified by V2", v.Spec.Name))
		return
	}
	params := map[string]any{}
	if len(v.Spec.Parameters.Raw) > 0 {
		if err := json.Unmarshal(v.Spec.Parameters.Raw, &params); err != nil {
			c.add(res, Unsupported, fmt.Sprintf("cannot parse parameters: %v", err))
			return
		}
	}
	doc, _ := params["trustPolicyDoc"].(map[string]any)
	tps, _ := doc["trustPolicies"].([]any)
	if len(tps) == 0 {
		c.add(res, Unsupported, "no trustPolicyDoc.trustPolicies to convert")
		return
	}
	certStores, _ := params["verificationCertStores"].(map[string]any)

	for i, raw := range tps {
		tp, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name, _ := tp["name"].(string)
		if name == "" {
			name = fmt.Sprintf("#%d", i)
		}
		tpRes := res + "#" + name
		c.touched[res] = true
		if a := c.convertTrustPolicy(tp, certStores, v.Namespace, tpRes); a != nil {
			key := a.key()
			g, ok := groups[key]
			if !ok {
				g = a
				groups[key] = g
				*order = append(*order, key)
			} else {
				for s := range a.scopes {
					g.scopes[s] = true
				}
				for id := range a.clientIDs {
					g.clientIDs[id] = true
				}
				for n := range a.namespaces {
					g.namespaces[n] = true
				}
				g.sources = append(g.sources, a.sources...)
			}
		}
	}
}

func (c *converter) convertTrustPolicy(tp map[string]any, certStores map[string]any, ns, res string) *assignment {
	a := &assignment{
		namespaces: map[string]bool{ns: true},
		scopes:     map[string]bool{},
		certs:      map[string]map[string][]CertRef{},
		clientIDs:  map[string]bool{},
		sources:    []string{res},
	}
	clean := true

	sv, _ := tp["signatureVerification"].(map[string]any)
	level, _ := sv["level"].(string)
	switch {
	case level == "skip":
		c.add(res, Unsupported, "signatureVerification.level=skip has no V2 equivalent; remove these scopes or sign the images")
		return nil
	case level == "permissive":
		c.add(res, NeedsReview, "signatureVerification.level=permissive: V2 enforces authentic timestamp, expiry and revocation; images V1 accepted may be denied")
		clean = false
	case level == "audit":
		c.add(res, NeedsReview, "signatureVerification.level=audit: V2 enforces authenticity; images V1 accepted may be denied")
		clean = false
	case level != "" && level != "strict":
		c.add(res, NeedsReview, fmt.Sprintf("signatureVerification.level=%q is not recognised; V2 always verifies at strict", level))
		clean = false
	}
	if ov, ok := sv["override"].(map[string]any); ok && len(ov) > 0 {
		c.add(res, NeedsReview, "signatureVerification.override dropped: V2 enforces every check; images V1 accepted may be denied")
		clean = false
	}
	if vt, _ := sv["verifyTimestamp"].(string); vt == "afterCertExpiry" {
		c.add(res, NeedsReview, "signatureVerification.verifyTimestamp=afterCertExpiry dropped: V2 always verifies the timestamp countersignature when a tsa store is configured")
		clean = false
	}

	for _, s := range toStrings(tp["registryScopes"]) {
		norm := normalizeScope(s)
		switch {
		case norm == "*":
			c.add(res, Unsupported, `registryScopes "*" has no V2 catch-all; list the registries/repositories explicitly`)
			clean = false
		case !scopePattern.MatchString(norm):
			c.add(res, Unsupported, fmt.Sprintf("registryScope %q is not a valid V2 scope", s))
			clean = false
		default:
			a.scopes[norm] = true
		}
	}

	ids := map[string]bool{}
	for _, id := range toStrings(tp["trustedIdentities"]) {
		ids[strings.TrimSpace(id)] = true
	}
	switch {
	case len(ids) == 0:
		c.add(res, Unsupported, "trustedIdentities is empty; notation rejects this policy in V1, and V2 must not widen it to \"*\"")
		return nil
	case ids["*"] && len(ids) > 1:
		c.add(res, Unsupported, "trustedIdentities mixes \"*\" with specific identities; notation rejects this policy in V1, fix it before migrating")
		return nil
	}
	a.identities = sortedKeys(ids)

	for _, ts := range toStrings(tp["trustStores"]) {
		typ, storeName, found := strings.Cut(ts, ":")
		if !found {
			typ, storeName = "ca", ts
		}
		if typ != "ca" && typ != "tsa" && typ != "signingAuthority" {
			c.add(res, Unsupported, fmt.Sprintf("trust store type %q is not supported in V2", typ))
			clean = false
			continue
		}
		for _, kmpName := range certStoreRefs(certStores, typ, storeName) {
			key, k := c.lookupKMP(ns, kmpName)
			if k == nil {
				c.add(res, NeedsReview, fmt.Sprintf("trust store %s references unknown or ambiguous provider %q", ts, kmpName))
				clean = false
				continue
			}
			c.used[key] = true
			if !k.supported {
				c.add(k.res, Unsupported, k.reason)
				clean = false
				continue
			}
			if k.reason != "" {
				c.add(k.res, NeedsReview, k.reason)
			}
			if k.clientID != "" {
				a.clientIDs[k.clientID] = true
			}
			if a.certs[typ] == nil {
				a.certs[typ] = map[string][]CertRef{}
			}
			a.certs[typ][k.vaultURL] = append(a.certs[typ][k.vaultURL], k.certs...)
		}
	}
	for typ, vaults := range a.certs {
		for url, refs := range vaults {
			a.certs[typ][url] = dedupeRefs(refs)
		}
	}
	if len(a.certs) == 0 {
		c.add(res, Unsupported, "no Azure Key Vault trust material could be derived; V2 requires at least one certificate")
		return nil
	}
	if clean {
		c.add(res, Migrated, "trust policy converted")
	}
	return a
}

// certStoreRefs resolves a trust store ("type:name") to provider names, for
// both v1beta1 (type -> name -> [providers]) and v1alpha1 (name -> [providers]).
func certStoreRefs(certStores map[string]any, typ, name string) []string {
	if byType, ok := certStores[typ].(map[string]any); ok {
		if refs := toStrings(byType[name]); len(refs) > 0 {
			return refs
		}
	}
	return toStrings(certStores[name])
}

func (a *assignment) key() string {
	var b strings.Builder
	b.WriteString(strings.Join(sortedKeys(a.namespaces), ","))
	b.WriteString("|")
	b.WriteString(strings.Join(a.identities, ","))
	for _, typ := range certTypeOrder {
		vaults := a.certs[typ]
		for _, url := range sortedMapKeys(vaults) {
			fmt.Fprintf(&b, "|%s@%s:", typ, url)
			for _, r := range vaults[url] {
				b.WriteString(r.Name + "/" + r.Version + ",")
			}
		}
	}
	return b.String()
}

// ---- Store / Policy ----

func (c *converter) convertStores(stores []*v1.Store) map[string]bool {
	clients := map[string]bool{}
	for _, s := range stores {
		res := resID(s.Kind, v1.KindStore, s.Namespace, s.Name)
		if s.Spec.Name != "oras" {
			c.add(res, Unsupported, fmt.Sprintf("store type %q is not supported; V2 uses the AKS-managed registry store", s.Spec.Name))
			continue
		}
		params := map[string]any{}
		if len(s.Spec.Parameters.Raw) > 0 {
			_ = json.Unmarshal(s.Spec.Parameters.Raw, &params)
		}
		ap, _ := params["authProvider"].(map[string]any)
		name, _ := ap["name"].(string)
		switch {
		case ap == nil:
			c.add(res, NeedsReview, "no authProvider (anonymous / docker config); V2 always pulls with identityResourceId, grant it AcrPull")
		case workloadIdentities[name]:
			if id, _ := ap["clientID"].(string); strings.TrimSpace(id) != "" {
				clients[strings.ToLower(strings.TrimSpace(id))] = true
			}
		case name == "azureManagedIdentity":
			c.add(res, NeedsReview, "authProvider azureManagedIdentity: V2 pulls with the workload-identity UAMI in identityResourceId; grant it AcrPull")
			if id, _ := ap["clientID"].(string); strings.TrimSpace(id) != "" {
				clients[strings.ToLower(strings.TrimSpace(id))] = true
			}
		default:
			c.add(res, Unsupported, fmt.Sprintf("authProvider %q is not supported; V2 only authenticates to registries with an Azure UAMI", name))
		}
		var dropped []string
		for _, k := range []string{"cacheEnabled", "ttl", "capacity", "keyNumber", "useHttp", "cosignEnabled"} {
			if _, ok := params[k]; ok {
				dropped = append(dropped, k)
			}
		}
		if len(dropped) > 0 && !c.touched[res] {
			c.add(res, Migrated, fmt.Sprintf("store settings %s dropped (managed by AKS in V2)", strings.Join(dropped, ", ")))
		}
	}
	return clients
}

func (c *converter) convertPolicies(policies []*v1.Policy) {
	for _, p := range policies {
		res := resID(p.Kind, v1.KindPolicy, p.Namespace, p.Name)
		switch p.Spec.Type {
		case "rego-policy":
			c.add(res, Unsupported, "rego policies are not supported; V2 uses a fixed threshold policy requiring notation")
		default:
			c.add(res, Dropped, "managed by AKS: V2 uses a fixed threshold policy requiring notation")
		}
	}
}

// ---- assembly ----

func (c *converter) namespaces(a *assignment) []string {
	if a.namespaces[""] {
		if len(a.namespaces) > 1 {
			c.add("Parameter/namespaces", NeedsReview, "cluster-scoped and namespaced trust policies share trust material; emitted as cluster-wide")
		}
		return []string{}
	}
	return sortedKeys(a.namespaces)
}

func (c *converter) excludedNamespaces() []string {
	if c.opts.ExcludedNamespaces != nil {
		return c.opts.ExcludedNamespaces
	}
	return DefaultExcludedNamespaces
}

func (c *converter) certificates(a *assignment) []Certificate {
	out := []Certificate{}
	for _, typ := range certTypeOrder {
		vaults := a.certs[typ]
		urls := sortedMapKeys(vaults)
		if len(urls) > 1 {
			c.add("Parameter/certificates", Unsupported, fmt.Sprintf("%s certificates come from %d Key Vaults (%s); V2 allows one entry per type", typ, len(urls), strings.Join(urls, ", ")))
		}
		for _, url := range urls {
			out = append(out, Certificate{Type: typ, AzureKeyVault: AzureKeyVault{VaultURL: url, Certificates: vaults[url]}})
		}
	}
	if len(a.certs["ca"]) == 0 && len(a.certs["signingAuthority"]) == 0 {
		c.add("Parameter/certificates", Unsupported, "a tsa trust store alone is invalid; at least one ca or signingAuthority entry is required")
	}
	return out
}

func (c *converter) identity(clientIDs []string) string {
	const res = "Parameter/identityResourceId"
	if id := strings.TrimSpace(c.opts.IdentityResourceID); id != "" {
		if !identityIDPattern.MatchString(id) {
			c.add(res, NeedsReview, fmt.Sprintf("--identity-resource-id %q is not a UAMI resource ID", id))
		}
		return id
	}
	switch len(clientIDs) {
	case 0:
		c.add(res, NeedsReview, "no workload-identity client ID found in v1 KMPs or stores; set the UAMI with Key Vault read and AcrPull access")
		return "<UAMI resource ID>"
	case 1:
	default:
		c.add(res, NeedsReview, fmt.Sprintf("v1 uses %d different client IDs (%s); V2 uses one identity per assignment for both Key Vault and registry", len(clientIDs), strings.Join(clientIDs, ", ")))
	}
	if c.opts.ResolveIdentity != nil {
		id, err := c.opts.ResolveIdentity(clientIDs[0])
		if err != nil {
			c.add(res, NeedsReview, fmt.Sprintf("lookup of client ID %s failed: %v", clientIDs[0], err))
		} else if id != "" {
			return id
		} else {
			c.add(res, NeedsReview, fmt.Sprintf("no UAMI with client ID %s found in the current subscription", clientIDs[0]))
		}
	} else {
		c.add(res, NeedsReview, fmt.Sprintf("replace the placeholder with the resource ID of the UAMI whose client ID is %s (or use --identity-resource-id / --lookup-identity)", clientIDs[0]))
	}
	return "<UAMI resource ID for client ID " + clientIDs[0] + ">"
}

// ---- helpers ----

func normalizeScope(s string) string {
	s = strings.TrimSpace(s)
	host, path, hasPath := strings.Cut(s, "/")
	host = strings.ToLower(host)
	if hasPath {
		return host + "/" + path
	}
	return host
}

func toStrings(v any) []string {
	arr, _ := v.([]any)
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		if s, ok := e.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

func dedupeRefs(refs []CertRef) []CertRef {
	seen := map[CertRef]bool{}
	out := make([]CertRef, 0, len(refs))
	for _, r := range refs {
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Version < out[j].Version
	})
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedMapKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
