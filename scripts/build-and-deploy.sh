#!/usr/bin/env bash
# build-and-deploy.sh — Build, push, and rollout one or more components
# Usage:
#   ./scripts/build-and-deploy.sh [orchestrator] [router] [zeroclaw] [all]
#
# Environment:
#   ECR_REGISTRY   default: <AWS_ACCOUNT_ID>.dkr.ecr.<AWS_REGION>.amazonaws.com
#   AWS_REGION     default: <AWS_REGION>
#   KUBE_CONTEXT   default: arn:aws:eks:<AWS_REGION>:<AWS_ACCOUNT_ID>:cluster/<EKS_CLUSTER_NAME>
#   NAMESPACE      default: tenants
#   PLATFORMS      default: linux/amd64,linux/arm64

set -euo pipefail

ECR_REGISTRY="${ECR_REGISTRY:-<AWS_ACCOUNT_ID>.dkr.ecr.<AWS_REGION>.amazonaws.com}"
AWS_REGION="${AWS_REGION:-us-east-1}"
KUBE_CONTEXT="${KUBE_CONTEXT:-}"  # set via env or kubeconfig default
NAMESPACE="${NAMESPACE:-tenants}"
PLATFORMS="${PLATFORMS:-linux/amd64,linux/arm64}"

CYAN='\033[0;36m'; GREEN='\033[0;32m'; RED='\033[0;31m'; NC='\033[0m'
info()    { echo -e "${CYAN}ℹ${NC}  $*"; }
success() { echo -e "${GREEN}✓${NC}  $*"; }
error()   { echo -e "${RED}✗${NC}  $*" >&2; exit 1; }

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

build_push() {
  local name="$1"
  local dockerfile="$2"
  local image="${ECR_REGISTRY}/${name}:latest"
  local extra_platforms="${3:-$PLATFORMS}"

  info "Building $name ($extra_platforms)..."
  docker buildx build \
    --platform "$extra_platforms" \
    -f "$PROJECT_DIR/$dockerfile" \
    -t "$image" \
    --push \
    "$PROJECT_DIR"
  success "Pushed $image"
}

rollout() {
  local deployment="$1"
  info "Restarting deployment/$deployment..."
  kubectl --context "$KUBE_CONTEXT" -n "$NAMESPACE" rollout restart "deployment/$deployment"
  kubectl --context "$KUBE_CONTEXT" -n "$NAMESPACE" rollout status  "deployment/$deployment" --timeout=120s
  success "deployment/$deployment rolled out"
}

build_orchestrator() {
  build_push "orchestrator" "Dockerfile.orchestrator"
  rollout "orchestrator"
}

build_router() {
  build_push "router" "Dockerfile.router"
  rollout "router"
}

build_zeroclaw() {
  # zeroclaw is amd64-only (kata-qemu nodes are amd64 metal)
  ZEROCLAW_DIR="${ZEROCLAW_DIR:-../zeroclaw}"
  local image="${ECR_REGISTRY}/zeroclaw:latest"
  info "Building zeroclaw (linux/amd64 only)..."
  docker build \
    -f "$ZEROCLAW_DIR/Dockerfile.prod" \
    -t "$image" \
    "$ZEROCLAW_DIR"
  docker push "$image"
  success "Pushed $image"
  info "zeroclaw pods are ephemeral — new image takes effect on next pod wake"
}

[[ $# -eq 0 ]] && { echo "Usage: $0 [orchestrator|router|zeroclaw|all]"; exit 1; }

for target in "$@"; do
  case "$target" in
    orchestrator) build_orchestrator ;;
    router)       build_router ;;
    zeroclaw)     build_zeroclaw ;;
    all)          build_orchestrator; build_router; build_zeroclaw ;;
    *) error "Unknown target: $target. Use orchestrator|router|zeroclaw|all" ;;
  esac
done

success "Done."
