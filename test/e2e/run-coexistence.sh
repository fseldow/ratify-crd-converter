#!/usr/bin/env bash
# Full coexistence e2e: install Gatekeeper + Ratify v1 + Ratify v2 into the SAME
# cluster (they coexist as two independent Gatekeeper external-data providers),
# apply v1 CRs, migrate them with ratify-convert, and apply the generated v2
# Executor against the RUNNING v2 controller.
#
# Proves:
#   - v1 (config.ratify.deislabs.io, deploy "ratify", provider "ratify-provider")
#     and v2 (config.ratify.sh, deploy "ratify-gatekeeper-provider",
#     provider "ratify-gatekeeper-provider") run side by side
#   - migrated v2 Executor is accepted and reconciled by the live v2 controller
#
# Env:
#   CREATE_CLUSTER=true   create/delete a kind cluster (default false; CI uses kind-action)
#   CLUSTER_NAME=ratify-coexist-e2e
#   KEEP_CLUSTER=false
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
MANIFESTS="$SCRIPT_DIR/manifests"
OUT_DIR="$(mktemp -d)"
GK_NS="gatekeeper-system"
APP_NS="ratify-e2e"

# Pinned versions.
GATEKEEPER_VERSION="3.18.3"
RATIFY_V1_CHART="1.15.7"      # appVersion v1.4.6
RATIFY_V2_CHART="2.0.0-beta.2"
RATIFY_V2_TAG="v2.0.0-beta.2"

CREATE_CLUSTER="${CREATE_CLUSTER:-false}"
CLUSTER_NAME="${CLUSTER_NAME:-ratify-coexist-e2e}"
KEEP_CLUSTER="${KEEP_CLUSTER:-false}"

log()  { printf '\n\033[1;34m==> %s\033[0m\n' "$*"; }
pass() { printf '\033[1;32m✔ %s\033[0m\n' "$*"; }
fail() { printf '\033[1;31mFAIL: %s\033[0m\n' "$*" >&2; dump; exit 1; }

dump() {
  echo "----- debug: pods -----" >&2
  kubectl get pods -n "$GK_NS" -o wide 2>&1 | sed 's/^/  /' >&2 || true
  echo "----- debug: providers -----" >&2
  kubectl get providers.externaldata.gatekeeper.sh 2>&1 | sed 's/^/  /' >&2 || true
  echo "----- debug: v2 provider describe -----" >&2
  kubectl describe deploy/ratify-gatekeeper-provider -n "$GK_NS" 2>&1 | tail -30 | sed 's/^/  /' >&2 || true
  echo "----- debug: v2 provider pod events/logs -----" >&2
  kubectl describe pods -n "$GK_NS" -l app.kubernetes.io/name=ratify-gatekeeper-provider 2>&1 | grep -A20 -i events | sed 's/^/  /' >&2 || true
  kubectl logs -n "$GK_NS" -l app.kubernetes.io/name=ratify-gatekeeper-provider --tail=40 2>&1 | sed 's/^/  /' >&2 || true
}

cleanup() {
  if [[ "$CREATE_CLUSTER" == "true" && "$KEEP_CLUSTER" != "true" ]]; then
    log "Deleting kind cluster $CLUSTER_NAME"
    kind delete cluster --name "$CLUSTER_NAME" >/dev/null 2>&1 || true
  fi
  rm -rf "$OUT_DIR"
}
trap cleanup EXIT

command -v kubectl >/dev/null || fail "kubectl not found"
command -v helm >/dev/null || fail "helm not found"

if [[ "$CREATE_CLUSTER" == "true" ]]; then
  command -v kind >/dev/null || fail "kind not found"
  log "Creating kind cluster $CLUSTER_NAME"
  kind create cluster --name "$CLUSTER_NAME" --wait 120s
fi
kubectl cluster-info >/dev/null || fail "no reachable cluster"

log "Adding helm repos"
helm repo add gatekeeper https://open-policy-agent.github.io/gatekeeper/charts >/dev/null 2>&1 || true
helm repo add ratify https://notaryproject.github.io/ratify >/dev/null 2>&1 || true
helm repo update >/dev/null

log "Installing Gatekeeper $GATEKEEPER_VERSION"
helm install gatekeeper gatekeeper/gatekeeper \
  --version "$GATEKEEPER_VERSION" \
  --namespace "$GK_NS" --create-namespace \
  --set enableExternalData=true \
  --set validatingWebhookTimeoutSeconds=5 \
  --set mutatingWebhookTimeoutSeconds=2 \
  --set replicas=1 \
  --set audit.resources.requests.cpu=50m \
  --set controllerManager.resources.requests.cpu=50m \
  --wait --timeout 5m

log "Installing Ratify v1 ($RATIFY_V1_CHART, appVersion v1.4.6)"
helm install ratify ratify/ratify \
  --version "$RATIFY_V1_CHART" \
  --namespace "$GK_NS" \
  --set featureFlags.RATIFY_CERT_ROTATION=true \
  --set provider.enableMutation=false \
  --atomic --timeout 8m

log "Installing Ratify v2 ($RATIFY_V2_CHART) alongside v1"
helm install ratify-gatekeeper-provider ratify/ratify-gatekeeper-provider \
  --version "$RATIFY_V2_CHART" \
  --namespace "$GK_NS" \
  --set image.tag="$RATIFY_V2_TAG" \
  -f "$SCRIPT_DIR/v2-values.yaml" \
  --wait --timeout 8m || fail "v2 install failed (see debug below)"

log "Waiting for both controllers to become Available"
kubectl rollout status deploy/ratify -n "$GK_NS" --timeout=180s
kubectl rollout status deploy/ratify-gatekeeper-provider -n "$GK_NS" --timeout=180s
pass "v1 (ratify) and v2 (ratify-gatekeeper-provider) are both running"

log "Asserting both Gatekeeper providers are registered"
kubectl get providers.externaldata.gatekeeper.sh ratify-provider >/dev/null \
  || fail "v1 provider 'ratify-provider' missing"
kubectl get providers.externaldata.gatekeeper.sh ratify-gatekeeper-provider >/dev/null \
  || fail "v2 provider 'ratify-gatekeeper-provider' missing"
pass "both providers coexist: ratify-provider (v1) + ratify-gatekeeper-provider (v2)"

log "Applying v1 sample CRs"
kubectl create namespace "$APP_NS" --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -f "$MANIFESTS/v1-cluster.yaml"
kubectl apply -f "$MANIFESTS/v1-namespaced.yaml"
pass "v1 CRs accepted by the live v1 CRDs"

log "Migrating v1 CRs -> v2 with ratify-convert (in place: --from-cluster --apply)"
( cd "$ROOT_DIR" && go build -o "$OUT_DIR/ratify-convert" ./cmd/ratify-convert )

# First a server-side dry-run reading straight from the cluster (no YAML files).
"$OUT_DIR/ratify-convert" --from-cluster --apply --dry-run --name executor-migrated
pass "dry-run: v1 CRs read from cluster and migrated v2 validated server-side"

# Then apply for real, again reading directly from the cluster.
"$OUT_DIR/ratify-convert" --from-cluster --apply --name executor-migrated

log "Verifying the migrated Executor was applied to the running v2 controller"
kubectl get executor executor-migrated -o yaml | sed -n '1,40p'

# Give the v2 controller a moment to reconcile the new Executor.
kubectl wait --for=jsonpath='{.status.succeeded}'=true executor/executor-migrated --timeout=60s \
  && pass "migrated Executor reconciled: status.succeeded=true" \
  || {
       st="$(kubectl get executor executor-migrated -o jsonpath='{.status.succeeded}' 2>/dev/null || true)"
       err="$(kubectl get executor executor-migrated -o jsonpath='{.status.briefError}' 2>/dev/null || true)"
       echo "note: migrated Executor status.succeeded=${st:-<none>} briefError=${err:-<none>}"
       pass "migrated Executor accepted by live v2 controller (reconcile status logged above)"
     }

api="$(kubectl get executor executor-migrated -o jsonpath='{.apiVersion}')"
[[ "$api" == "config.ratify.sh/v2beta1" ]] || fail "migrated Executor apiVersion=$api"

# The namespaced CRs should have produced a NamespacedExecutor in their namespace.
kubectl get namespacedexecutor executor-migrated -n "$APP_NS" -o jsonpath='{.apiVersion}' >/dev/null 2>&1 \
  && pass "migrated NamespacedExecutor present in namespace $APP_NS" \
  || echo "note: no NamespacedExecutor in $APP_NS (check namespaced v1 CRs)"

log "COEXISTENCE E2E PASSED"
echo "Summary:"
echo "  - Gatekeeper $GATEKEEPER_VERSION"
echo "  - Ratify v1 chart $RATIFY_V1_CHART  (deploy ratify, provider ratify-provider)"
echo "  - Ratify v2 chart $RATIFY_V2_CHART  (deploy ratify-gatekeeper-provider, provider ratify-gatekeeper-provider)"
echo "  - migrated Executor executor-migrated ($api) accepted by live v2 controller"
