# ratify-crd-converter

An offline CLI that migrates Ratify **v1** CRDs (`config.ratify.deislabs.io/v1beta1`)
into aggregated **v2** `Executor` / `NamespacedExecutor` resources
(`config.ratify.sh/v2beta1`).

## Why it's an aggregator, not a 1:1 converter

v1 models configuration as many independent CRs
(Store / Verifier / Policy / KeyManagementProvider / CertificateStore), whereas
v2 collapses all of them into a single `Executor`. The tool therefore consumes a
set of v1 CRs and aggregates them by scope (cluster, or one per namespace) into
`Executor` / `NamespacedExecutor` objects.

## Usage

### From files

```bash
# A directory (recurses over all .yaml files)
ratify-convert -f ./v1-manifests/ -o executor.yaml

# Multiple files + a fallback scope
ratify-convert -f store.yaml -f verifier.yaml -f kmp.yaml --scope "myregistry.io/*"
```

### Directly against a cluster (no intermediate YAML)

`--from-cluster` reads every v1 CR from the current kube-context, and `--apply`
writes the migrated v2 `Executor` / `NamespacedExecutor` back via server-side
apply — so you can migrate in place without exporting YAML by hand.

```bash
# Read all v1 CRs from the cluster and preview the v2 output
ratify-convert --from-cluster -o -

# Read from the cluster and apply the migrated v2 in place
ratify-convert --from-cluster --apply

# Safe preview: server-side dry-run (nothing is persisted)
ratify-convert --from-cluster --apply --dry-run
```

| Flag | Description |
|---|---|
| `-f, --file` | Input file or directory (repeatable) |
| `-k, --from-cluster` | Read all v1 CRs directly from the cluster instead of files |
| `--apply` | Apply the generated v2 Executor(s) to the cluster (server-side apply) |
| `--dry-run` | With `--apply`, use server-side dry-run (nothing persisted) |
| `--kubeconfig` | Path to kubeconfig (default: `$KUBECONFIG` or `~/.kube/config`) |
| `-o, --output` | Output file, or `-` for stdout (default: stdout unless `--apply`) |
| `--scope` | Fallback scopes when none can be derived from verifiers |
| `--concurrency` | `Executor.spec.concurrency` (0 = v2 default) |
| `--name` | `metadata.name` for generated executors |


## Mapping summary

- **Store**: `spec.name` → `type` (`oras` → `registry-store`); `parameters`
  passthrough; non-empty `address` / `source` produce a warning and are dropped.
- **Verifier**: `metadata.name` → instance `name`, `spec.name` → `type`;
  `artifactTypes` is folded into `parameters`.
- **Policy**: `type` / `parameters` passthrough; multiple policies → the first is
  used and a warning is emitted (v2 allows one policy enforcer).
- **KeyManagementProvider / CertificateStore**: when referenced by a verifier's
  `verificationCertStores`, they are inlined into that verifier's
  `parameters.certificates[]` (`inline` → `inline.certs`, `azurekeyvault` →
  `azurekeyvault.vaultURL`); unreferenced providers produce an orphan warning.
- **scopes**: derived from verifiers by default (notation
  `trustPolicyDoc.trustPolicies[].registryScopes`, cosign
  `trustPolicies[].scopes`); if any scope is `*` or trust policies disagree,
  falls back to `["*"]`.

## Fields that cannot be auto-mapped (warned)

1. Store/Verifier `address` / `source` — v2 has no external-plugin download model.
2. KMP `tenantID` / `clientID` — v2 uses workload identity.
3. Orphan KMPs, groups with no stores/verifiers, and multiple policies.

## Layout

```
cmd/ratify-convert/   CLI (cobra)
internal/apis/v1beta1  source types
internal/apis/v2beta1  target types
internal/loader        multi-document YAML -> Bundle
internal/cluster       read v1 CRs from / apply v2 to a live cluster (client-go)
internal/convert       converters + reference resolution + scope extraction + aggregator
internal/report        diagnostics collection
test/e2e/              kind-based end-to-end tests
testdata/              golden fixtures
```

## Development

```bash
go build ./...
go test ./...
go run ./cmd/ratify-convert -f testdata/v1_bundle.yaml
```

## End-to-end tests

Two kind-based suites run in CI (`.github/workflows/ci.yml`):

- **Lightweight CRD e2e** (`test/e2e/run.sh`) — installs both the v1 and v2 CRDs
  (fetched from the upstream Ratify repo at pinned tags, so there are no vendored
  copies to drift), applies the v1 sample CRs, migrates them, and validates the
  generated v2 manifests via server-side apply against the installed v2 CRD schema.
- **Full coexistence e2e** (`test/e2e/run-coexistence.sh`) — installs Gatekeeper,
  Ratify v1 (chart `ratify`) and Ratify v2 (chart `ratify-gatekeeper-provider`)
  into the same cluster, proving the two controllers coexist as independent
  Gatekeeper external-data providers, then migrates v1 CRs and applies the
  generated v2 `Executor` against the live v2 controller.

Run either locally against a throwaway cluster:

```bash
CREATE_CLUSTER=true ./test/e2e/run.sh
CREATE_CLUSTER=true ./test/e2e/run-coexistence.sh
# add KEEP_CLUSTER=true to inspect the cluster afterwards
```

## License

[Apache-2.0](LICENSE)
