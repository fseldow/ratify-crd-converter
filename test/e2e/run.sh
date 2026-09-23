#!/usr/bin/env bash
# End-to-end migration test.
#
# Flow:
#   1. (optional) create a kind cluster
#   2. install BOTH Ratify v1 (config.ratify.deislabs.io) and v2 (config.ratify.sh) CRDs
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
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
CRDS_V1="$SCRIPT_DIR/crds/v1"
CRDS_V2="$SCRIPT_DIR/crds/v2"
MANIFESTS="$SCRIPT_DIR/manifests"
OUT_DIR="$(mktemp -d)"
NS="ratify-e2e"

CREATE_CLUSTER="${CREATE_CLUSTER:-false}"
CLUSTER_NAME="${CLUSTER_NAME:-ratify-migrate-e2e}"
KEEP_CLUSTER="${KEEP_CLUSTER:-false}"

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

log "Installing Ratify v1 CRDs (config.ratify.deislabs.io)"
kubectl apply -f "$CRDS_V1"

log "Installing Ratify v2 CRDs (config.ratify.sh)"
kubectl apply -f "$CRDS_V2"

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
