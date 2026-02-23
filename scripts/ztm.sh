#!/usr/bin/env bash
# ztm — Agentic Tenancy CLI
# Usage: ztm <command> [args]
#
# Commands:
#   tenant create <id> <bot_token> [--idle-timeout <secs>]
#   tenant delete <id>
#   tenant list
#   tenant get <id>
#   tenant update <id> --bot-token <token> [--idle-timeout <secs>]
#   webhook register <id>
#
# Environment variables:
#   ZTM_ORCHESTRATOR_URL   Orchestrator API base URL (default: http://localhost:8080)
#   ZTM_ROUTER_URL         Router public base URL (default: https://<YOUR_ROUTER_DOMAIN>)
#   ZTM_NAMESPACE          Kubernetes namespace (default: tenants)
#   ZTM_KUBE_CONTEXT       kubectl context (default: current context)

set -euo pipefail

# ── Config ───────────────────────────────────────────────────────────────────

ORCHESTRATOR_URL="${ZTM_ORCHESTRATOR_URL:-}"
ROUTER_URL="${ZTM_ROUTER_URL:-}"
NAMESPACE="${ZTM_NAMESPACE:-tenants}"
KUBE_CONTEXT="${ZTM_KUBE_CONTEXT:-}"

# ── Helpers ───────────────────────────────────────────────────────────────────

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

info()    { echo -e "${CYAN}ℹ${NC}  $*"; }
success() { echo -e "${GREEN}✓${NC}  $*"; }
warn()    { echo -e "${YELLOW}⚠${NC}  $*"; }
error()   { echo -e "${RED}✗${NC}  $*" >&2; exit 1; }

require_cmd() {
  command -v "$1" &>/dev/null || error "Required command not found: $1"
}

require_cmd curl
require_cmd kubectl

kubectl_exec() {
  local ctx_arg=""
  [[ -n "$KUBE_CONTEXT" ]] && ctx_arg="--context $KUBE_CONTEXT"
  kubectl $ctx_arg -n "$NAMESPACE" exec deployment/orchestrator -- "$@"
}

# Auto-detect orchestrator URL: try local port-forward path first, then in-cluster
resolve_orchestrator_url() {
  if [[ -n "$ORCHESTRATOR_URL" ]]; then
    echo "$ORCHESTRATOR_URL"
    return
  fi
  # Use kubectl exec to call the API in-cluster (no port-forward needed)
  echo "in-cluster"
}

# Call orchestrator API — either direct HTTP or via kubectl exec
orch_api() {
  local method="$1"; shift
  local path="$1"; shift
  local data="${1:-}"

  if [[ -n "$ORCHESTRATOR_URL" ]]; then
    # Direct HTTP call
    local url="${ORCHESTRATOR_URL}${path}"
    if [[ -n "$data" ]]; then
      curl -sf -X "$method" "$url" \
        -H "Content-Type: application/json" \
        -d "$data"
    else
      curl -sf -X "$method" "$url"
    fi
  else
    # In-cluster via kubectl exec
    local wget_args=()
    wget_args+=(--method="$method")
    wget_args+=(--header="Content-Type: application/json")
    [[ -n "$data" ]] && wget_args+=(--body-data="$data")
    kubectl_exec wget -qO- "${wget_args[@]}" "http://localhost:8080${path}"
  fi
}

# Call router API
router_api() {
  local method="$1"
  local path="$2"
  local url

  if [[ -n "$ROUTER_URL" ]]; then
    url="${ROUTER_URL}${path}"
  else
    # In-cluster via kubectl exec on router
    local ctx_arg=""
    [[ -n "$KUBE_CONTEXT" ]] && ctx_arg="--context $KUBE_CONTEXT"
    kubectl $ctx_arg -n "$NAMESPACE" exec deployment/router -- \
      wget -qO- --post-data='' "http://localhost:9090${path}"
    return
  fi
  curl -sf -X "$method" "$url"
}

usage() {
  cat <<EOF
${CYAN}ztm${NC} — Agentic Tenancy CLI

${YELLOW}Usage:${NC}
  ztm tenant create <id> <bot_token> [--idle-timeout <secs>]
  ztm tenant delete <id>
  ztm tenant list
  ztm tenant get <id>
  ztm tenant update <id> [--bot-token <token>] [--idle-timeout <secs>]
  ztm webhook register <id>

${YELLOW}Environment:${NC}
  ZTM_ORCHESTRATOR_URL   Orchestrator base URL (omit to use kubectl exec)
  ZTM_ROUTER_URL         Router public URL (omit to use kubectl exec)
  ZTM_NAMESPACE          Kubernetes namespace (default: tenants)
  ZTM_KUBE_CONTEXT       kubectl context (default: current context)

${YELLOW}Examples:${NC}
  # Create a tenant and register webhook in one go
  ztm tenant create alice 1234567890:AAHxxx --idle-timeout 3600
  ztm webhook register alice

  # Update bot token
  ztm tenant update alice --bot-token 9876543210:AAHyyy

  # List all tenants
  ztm tenant list

  # Delete tenant
  ztm tenant delete alice
EOF
  exit 0
}

# ── Commands ──────────────────────────────────────────────────────────────────

cmd_tenant_create() {
  local id="${1:-}"; shift || true
  local bot_token="${1:-}"; shift || true
  local idle_timeout=600

  [[ -z "$id" ]]        && error "Usage: ztm tenant create <id> <bot_token> [--idle-timeout <secs>]"
  [[ -z "$bot_token" ]] && error "Usage: ztm tenant create <id> <bot_token> [--idle-timeout <secs>]"

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --idle-timeout) idle_timeout="$2"; shift 2 ;;
      *) error "Unknown flag: $1" ;;
    esac
  done

  info "Creating tenant '$id'..."
  local payload
  payload=$(printf '{"tenant_id":"%s","bot_token":"%s","idle_timeout_s":%d}' \
    "$id" "$bot_token" "$idle_timeout")

  local result
  result=$(orch_api POST /tenants "$payload") || error "Failed to create tenant '$id'"

  success "Tenant '$id' created"
  echo "$result" | python3 -m json.tool 2>/dev/null || echo "$result"
}

cmd_tenant_delete() {
  local id="${1:-}"
  [[ -z "$id" ]] && error "Usage: ztm tenant delete <id>"

  info "Deleting tenant '$id'..."
  orch_api DELETE "/tenants/$id" || error "Failed to delete tenant '$id'"
  success "Tenant '$id' deleted"
}

cmd_tenant_list() {
  info "Listing tenants..."
  local result
  result=$(orch_api GET /tenants) || error "Failed to list tenants"
  echo "$result" | python3 -m json.tool 2>/dev/null || echo "$result"
}

cmd_tenant_get() {
  local id="${1:-}"
  [[ -z "$id" ]] && error "Usage: ztm tenant get <id>"

  local result
  result=$(orch_api GET "/tenants/$id") || error "Tenant '$id' not found"
  echo "$result" | python3 -m json.tool 2>/dev/null || echo "$result"
}

cmd_tenant_update() {
  local id="${1:-}"; shift || true
  [[ -z "$id" ]] && error "Usage: ztm tenant update <id> [--bot-token <token>] [--idle-timeout <secs>]"

  local bot_token=""
  local idle_timeout=""

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --bot-token)    bot_token="$2";    shift 2 ;;
      --idle-timeout) idle_timeout="$2"; shift 2 ;;
      *) error "Unknown flag: $1" ;;
    esac
  done

  [[ -z "$bot_token" && -z "$idle_timeout" ]] && \
    error "Specify at least one of --bot-token or --idle-timeout"

  # Build JSON payload with only provided fields
  local payload="{"
  local sep=""
  [[ -n "$bot_token" ]]    && payload+="${sep}\"bot_token\":\"${bot_token}\""    && sep=","
  [[ -n "$idle_timeout" ]] && payload+="${sep}\"idle_timeout_s\":${idle_timeout}" && sep=","
  payload+="}"

  info "Updating tenant '$id'..."
  local result
  result=$(orch_api PATCH "/tenants/$id" "$payload") || error "Failed to update tenant '$id'"
  success "Tenant '$id' updated"
  echo "$result" | python3 -m json.tool 2>/dev/null || echo "$result"
}

cmd_webhook_register() {
  local id="${1:-}"
  [[ -z "$id" ]] && error "Usage: ztm webhook register <id>"

  info "Registering Telegram webhook for tenant '$id'..."
  local result
  result=$(router_api POST "/admin/webhook/$id") || error "Failed to register webhook for '$id'"
  success "Webhook registered"
  echo "$result" | python3 -m json.tool 2>/dev/null || echo "$result"
}

# ── Dispatch ──────────────────────────────────────────────────────────────────

[[ $# -eq 0 ]] && usage

case "$1" in
  tenant)
    shift
    case "${1:-}" in
      create)   shift; cmd_tenant_create "$@" ;;
      delete)   shift; cmd_tenant_delete "$@" ;;
      list)     shift; cmd_tenant_list ;;
      get)      shift; cmd_tenant_get "$@" ;;
      update)   shift; cmd_tenant_update "$@" ;;
      *)        error "Unknown tenant command: ${1:-}. Run 'ztm' for help." ;;
    esac
    ;;
  webhook)
    shift
    case "${1:-}" in
      register) shift; cmd_webhook_register "$@" ;;
      *)        error "Unknown webhook command: ${1:-}. Run 'ztm' for help." ;;
    esac
    ;;
  help|--help|-h) usage ;;
  *) error "Unknown command: $1. Run 'ztm' for help." ;;
esac
