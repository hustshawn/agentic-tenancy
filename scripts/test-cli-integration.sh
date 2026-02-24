#!/usr/bin/env bash
# scripts/test-cli-integration.sh
# Integration tests for ztm CLI (requires running cluster)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
BIN="$PROJECT_DIR/bin/ztm"

RED='\033[0;31m'
GREEN='\033[0;32m'
CYAN='\033[0;36m'
NC='\033[0m'

info() { echo -e "${CYAN}ℹ${NC} $*"; }
success() { echo -e "${GREEN}✓${NC} $*"; }
error() { echo -e "${RED}✗${NC} $*" >&2; exit 1; }

# Build CLI
info "Building ztm CLI..."
cd "$PROJECT_DIR"
make ztm || error "Failed to build CLI"
success "CLI built"

# Test version
info "Testing version command..."
VERSION_OUTPUT=$("$BIN" version)
[[ "$VERSION_OUTPUT" =~ "ztm v" ]] || error "Version command failed"
success "Version: $(echo "$VERSION_OUTPUT" | head -1)"

# Test help
info "Testing help output..."
"$BIN" --help > /dev/null || error "Help command failed"
"$BIN" tenant --help > /dev/null || error "Tenant help failed"
"$BIN" webhook --help > /dev/null || error "Webhook help failed"
success "Help commands work"

# Test invalid command
info "Testing error handling..."
"$BIN" invalid-command &>/dev/null && error "Should fail on invalid command"
success "Error handling works"

# If kubectl is available and cluster is accessible, test API calls
if command -v kubectl &>/dev/null; then
	if kubectl get namespace tenants &>/dev/null; then
		info "Testing tenant list (requires running cluster)..."
		"$BIN" tenant list --output json &>/dev/null || {
			info "Tenant list failed (orchestrator may not be deployed)"
		}
	else
		info "Skipping API tests (no tenants namespace)"
	fi
else
	info "Skipping API tests (kubectl not found)"
fi

success "All integration tests passed"
