#!/usr/bin/env bash
# End-to-end migration test.
#
# Flow:
#   1. (optional) create a kind cluster
#   2. install BOTH Ratify v1 (config.ratify.deislabs.io) and v2 (config.ratify.sh)
#      CRDs, fetched directly from the upstream repo at pinned tags (single
#      source of truth; no vendored copies to drift out of sync)
#   3. apply the v1 sample CRs   -> proves they are valid v1 resources
#   4. build ratify-convert and migrate the v1 CRs -> v2 Executor manifests
#   5. apply the generated v2 manifests to the cluster
#      -> the API server validates them against the installed v2 CRD OpenAPI schema,
#         which is the real guarantee that the migration output is correct v2
#   6. assert the applied objects are config.ratify.sh/v2beta1
#
# Env:
#   CREATE_CLUSTER=true   create/delete a kind cluster named $CLUSTER_NAME (default false;
#                         CI uses helm/kind-action to provide the cluster)
#   CLUSTER_NAME=ratify-migrate-e2e
#   KEEP_CLUSTER=false    keep the cluster after the run
#   RATIFY_V1_TAG=v1.4.6         upstream tag for the v1 CRDs
#   RATIFY_V2_TAG=v2.0.0-beta.2  upstream tag for the v2 CRDs
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
MANIFESTS="$SCRIPT_DIR/manifests"
OUT_DIR="$(mktemp -d)"
NS="ratify-e2e"

CREATE_CLUSTER="${CREATE_CLUSTER:-false}"
CLUSTER_NAME="${CLUSTER_NAME:-ratify-migrate-e2e}"
KEEP_CLUSTER="${KEEP_CLUSTER:-false}"

# CRDs are pulled from the upstream Ratify repo at these pinned tags rather than
# vendored into this repo, so the schema under test always matches a real release.
RATIFY_V1_TAG="${RATIFY_V1_TAG:-v1.4.6}"
RATIFY_V2_TAG="${RATIFY_V2_TAG:-v2.0.0-beta.2}"
RAW="https://raw.githubusercontent.com/ratify-project/ratify"

V1_CRDS=(
  "$RAW/$RATIFY_V1_TAG/config/crd/bases/config.ratify.deislabs.io_stores.yaml"
  "$RAW/$RATIFY_V1_TAG/config/crd/bases/config.ratify.deislabs.io_verifiers.yaml"
  "$RAW/$RATIFY_V1_TAG/config/crd/bases/config.ratify.deislabs.io_policies.yaml"
  "$RAW/$RATIFY_V1_TAG/config/crd/bases/config.ratify.deislabs.io_keymanagementproviders.yaml"
  "$RAW/$RATIFY_V1_TAG/config/crd/bases/config.ratify.deislabs.io_certificatestores.yaml"
  "$RAW/$RATIFY_V1_TAG/config/crd/bases/config.ratify.deislabs.io_namespacedstores.yaml"
  "$RAW/$RATIFY_V1_TAG/config/crd/bases/config.ratify.deislabs.io_namespacedverifiers.yaml"
  "$RAW/$RATIFY_V1_TAG/config/crd/bases/config.ratify.deislabs.io_namespacedpolicies.yaml"
  "$RAW/$RATIFY_V1_TAG/config/crd/bases/config.ratify.deislabs.io_namespacedkeymanagementproviders.yaml"
)
V2_CRDS=(
  "$RAW/$RATIFY_V2_TAG/deployments/ratify-gatekeeper-provider/crds/executors.config.ratify.sh.yaml"
  "$RAW/$RATIFY_V2_TAG/deployments/ratify-gatekeeper-provider/crds/namespacedexecutors.config.ratify.sh.yaml"
)

log()  { printf '\n\033[1;34m==> %s\033[0m\n' "$*"; }
pass() { printf '\033[1;32m✔ %s\033[0m\n' "$*"; }
fail() { printf '\033[1;31mFAIL: %s\033[0m\n' "$*" >&2; exit 1; }

cleanup() {
  if [[ "$CREATE_CLUSTER" == "true" && "$KEEP_CLUSTER" != "true" ]]; then
    log "Deleting kind cluster $CLUSTER_NAME"
    kind delete cluster --name "$CLUSTER_NAME" >/dev/null 2>&1 || true
  fi
  rm -rf "$OUT_DIR"
}
trap cleanup EXIT

command -v kubectl >/dev/null || fail "kubectl not found"

if [[ "$CREATE_CLUSTER" == "true" ]]; then
  command -v kind >/dev/null || fail "kind not found"
  log "Creating kind cluster $CLUSTER_NAME"
  kind create cluster --name "$CLUSTER_NAME" --wait 120s
fi

kubectl cluster-info >/dev/null || fail "no reachable cluster (set CREATE_CLUSTER=true or configure kubeconfig)"

log "Installing Ratify v1 CRDs (config.ratify.deislabs.io @ $RATIFY_V1_TAG)"
for url in "${V1_CRDS[@]}"; do kubectl apply -f "$url"; done

log "Installing Ratify v2 CRDs (config.ratify.sh @ $RATIFY_V2_TAG)"
for url in "${V2_CRDS[@]}"; do kubectl apply -f "$url"; done

kubectl wait --for=condition=Established --timeout=60s \
  crd/stores.config.ratify.deislabs.io \
  crd/verifiers.config.ratify.deislabs.io \
  crd/executors.config.ratify.sh \
  crd/namespacedexecutors.config.ratify.sh

kubectl create namespace "$NS" --dry-run=client -o yaml | kubectl apply -f -

log "Applying v1 sample CRs (validates v1 schema)"
kubectl apply -f "$MANIFESTS/v1-cluster.yaml"
kubectl apply -f "$MANIFESTS/v1-namespaced.yaml"
pass "v1 CRs accepted by the API server"

log "Building ratify-convert"
( cd "$ROOT_DIR" && go build -o "$OUT_DIR/ratify-convert" ./cmd/ratify-convert )

log "Migrating v1 CRs -> v2 Executor manifests"
"$OUT_DIR/ratify-convert" -f "$MANIFESTS/v1-cluster.yaml"    -o "$OUT_DIR/executor.yaml"    --name executor-sample
"$OUT_DIR/ratify-convert" -f "$MANIFESTS/v1-namespaced.yaml" -o "$OUT_DIR/ns-executor.yaml" --name executor-sample
echo "--- generated cluster Executor ---"; cat "$OUT_DIR/executor.yaml"
echo "--- generated NamespacedExecutor ---"; cat "$OUT_DIR/ns-executor.yaml"

log "Server-side validation: apply generated v2 manifests against installed v2 CRDs"
kubectl apply --server-side -f "$OUT_DIR/executor.yaml"
kubectl apply --server-side -f "$OUT_DIR/ns-executor.yaml"
pass "generated v2 manifests accepted by the API server"

log "Asserting migrated objects are config.ratify.sh/v2beta1"
api="$(kubectl get executor executor-sample -o jsonpath='{.apiVersion}')"
[[ "$api" == "config.ratify.sh/v2beta1" ]] || fail "cluster Executor apiVersion=$api"
nsapi="$(kubectl get namespacedexecutor executor-sample -n "$NS" -o jsonpath='{.apiVersion}')"
[[ "$nsapi" == "config.ratify.sh/v2beta1" ]] || fail "NamespacedExecutor apiVersion=$nsapi"

# The migrated store type must have been aliased oras -> registry-store.
stype="$(kubectl get executor executor-sample -o jsonpath='{.spec.stores[0].type}')"
[[ "$stype" == "registry-store" ]] || fail "store type=$stype (expected registry-store)"

# The inline KMP must have been inlined into the verifier's certificates[].
certs="$(kubectl get executor executor-sample -o jsonpath='{.spec.verifiers[0].parameters.certificates}')"
[[ -n "$certs" ]] || fail "verifier certificates[] is empty (KMP was not inlined)"

pass "cluster Executor: apiVersion=$api store=$stype certificates inlined"
pass "namespaced Executor: apiVersion=$nsapi"

log "MIGRATION E2E PASSED"
