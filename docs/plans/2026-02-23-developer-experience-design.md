# Developer Experience Improvement: Go CLI

**Date:** 2026-02-23
**Status:** Approved
**Version:** v0.1.0

---

## Overview

Replace the bash-based `ztm.sh` CLI with a cross-platform Go binary using the Cobra framework. This improves portability, eliminates external dependencies (python3 for JSON formatting), and provides a foundation for future DX enhancements (logs streaming, health checks, observability).

## Goals

1. **Feature parity**: All existing `ztm.sh` commands work identically
2. **Zero regression**: Maintain kubectl-based API access (no new dependencies)
3. **Better UX**: Colored output, better error messages, shell completions
4. **Extensibility**: Clean architecture for adding logs, status, metrics later

## Non-Goals (Deferred to Future Versions)

- Logs streaming (v0.2.0)
- Health/status dashboard (v0.3.0)
- Metrics/analytics (v0.4.0)
- Local development environment (separate project)
- Observability web UI (separate project)

---

## Architecture

### Project Structure

```
cmd/ztm/
├── main.go                     # Entry point, cobra root setup
└── cmd/
    ├── root.go                 # Root command, global flags, config
    ├── tenant.go               # tenant subcommand group
    ├── tenant_create.go        # tenant create (complex flags)
    ├── tenant_update.go        # tenant update (complex flags)
    ├── webhook.go              # webhook subcommand group
    └── version.go              # version command

internal/cli/
├── k8s/
│   ├── exec.go                # kubectl exec wrapper
│   └── context.go             # kubeconfig detection, context resolution
├── api/
│   ├── client.go              # Shared API client interface
│   ├── orchestrator.go        # Orchestrator API methods
│   └── router.go              # Router API methods
└── output/
    ├── format.go              # JSON/table formatting
    └── style.go               # Color output (✓, ✗, info, warn)
```

### Dependencies

- `github.com/spf13/cobra v1.8.0` — CLI framework
- `k8s.io/client-go` (already in go.mod) — kubeconfig parsing
- `k8s.io/apimachinery` (already in go.mod) — k8s types
- stdlib only for HTTP, JSON, exec

### Thin Client Architecture

The CLI is a presentation layer over HTTP APIs. No business logic, no direct DynamoDB/Redis access. All operations go through Orchestrator/Router APIs via kubectl exec or direct HTTP.

```
┌─────────┐
│  ztm CLI│
└────┬────┘
     │
     ├─ kubectl exec deployment/orchestrator -- wget ...
     │         ↓
     │   Orchestrator API (in-cluster)
     │
     └─ kubectl exec deployment/router -- wget ...
               ↓
         Router API (in-cluster)
```

---

## Command Structure

### Hierarchy

```
ztm
├── tenant
│   ├── create <id> <bot_token> [--idle-timeout <secs>]
│   ├── delete <id>
│   ├── list
│   ├── get <id>
│   └── update <id> [--bot-token <token>] [--idle-timeout <secs>]
├── webhook
│   └── register <id>
└── version
```

### Global Flags

Inherited by all commands:

```
--namespace string      Kubernetes namespace (default: tenants)
--context string        kubectl context (default: current context)
--orchestrator-url      Direct HTTP URL (bypasses kubectl exec)
--router-url           Router public URL (for webhook registration)
--output string        Output format: json|table (default: table)
--no-color            Disable colored output
```

### Command Examples

```bash
# Create tenant (same as ztm.sh)
ztm tenant create alice 1234567890:AAHxyz --idle-timeout 3600

# List tenants with JSON output
ztm tenant list --output json

# Get tenant from different cluster
ztm tenant get alice --context prod-eks

# Update tenant
ztm tenant update alice --bot-token 9876:AAHnew --idle-timeout 1800

# Delete tenant
ztm tenant delete alice

# Register webhook
ztm webhook register alice

# Show version
ztm version
```

---

## API Client Design

### Interface

```go
// internal/cli/api/client.go
type Client interface {
    // Orchestrator APIs
    CreateTenant(ctx context.Context, req *CreateTenantRequest) (*Tenant, error)
    DeleteTenant(ctx context.Context, id string) error
    ListTenants(ctx context.Context) ([]Tenant, error)
    GetTenant(ctx context.Context, id string) (*Tenant, error)
    UpdateTenant(ctx context.Context, id string, req *UpdateTenantRequest) (*Tenant, error)

    // Router APIs
    RegisterWebhook(ctx context.Context, tenantID string) (*WebhookResponse, error)
}
```

### Two Implementations

1. **KubectlExecClient** (default)
   - Calls `kubectl exec deployment/orchestrator -- wget ...`
   - No network requirements, works same as bash script
   - Requires kubectl in PATH and valid kubeconfig

2. **HTTPClient** (when `--orchestrator-url` provided)
   - Direct HTTP calls to orchestrator/router
   - Used for port-forward scenarios or external access
   - Bypasses kubectl entirely

### Configuration Resolution

```
Priority (lowest to highest):
1. Default values (namespace: tenants, context: current)
2. Environment variables (ZTM_NAMESPACE, ZTM_KUBE_CONTEXT, ZTM_ORCHESTRATOR_URL)
3. CLI flags (--namespace, --context, --orchestrator-url)
```

### Error Handling

- **API errors**: Parse HTTP status, show user-friendly messages
- **kubectl errors**: Detect missing kubectl, invalid context, pod not found
- **JSON errors**: Show raw response if pretty-print fails
- **Timeouts**: 30s for API calls, 10s for kubectl exec

---

## Implementation Details

### kubectl exec Flow

```go
// internal/cli/k8s/exec.go
func ExecAPICall(ctx context.Context, cfg *Config, method, path string, body []byte) ([]byte, error) {
    // 1. Build kubectl exec command
    args := []string{
        "exec",
        "-n", cfg.Namespace,
        fmt.Sprintf("deployment/%s", cfg.Deployment),
        "--",
        "wget",
        "-qO-",
        fmt.Sprintf("--method=%s", method),
    }

    // 2. Add context flag if specified
    if cfg.Context != "" {
        args = append([]string{"--context", cfg.Context}, args...)
    }

    // 3. Add headers and body
    if body != nil {
        args = append(args,
            "--header=Content-Type: application/json",
            fmt.Sprintf("--body-data=%s", string(body)))
    }

    // 4. Append local URL
    url := fmt.Sprintf("http://localhost:%d%s", cfg.Port, path)
    args = append(args, url)

    // 5. Execute with timeout
    cmd := exec.CommandContext(ctx, "kubectl", args...)
    output, err := cmd.CombinedOutput()

    // 6. Parse response and errors
    return parseResponse(output, err)
}
```

### Output Formatting

```go
// internal/cli/output/format.go
type Formatter interface {
    FormatTenant(t *api.Tenant)
    FormatTenantList(tenants []api.Tenant)
    Success(msg string)
    Error(msg string)
    Info(msg string)
}

// JSONFormatter: raw JSON (--output=json)
// TableFormatter: text/tabwriter aligned columns (default)
```

**Table output example:**

```
TENANT ID    STATUS     LAST ACTIVE          IDLE TIMEOUT
alice        running    2026-02-23 14:32:10  3600s
bob          idle       2026-02-22 09:15:43  600s
charlie      running    2026-02-23 15:01:22  1800s
```

**Colored output** (auto-detected TTY):
- ✓ Success messages (green)
- ✗ Error messages (red)
- ℹ Info messages (cyan)
- ⚠ Warning messages (yellow)

### Version Information

```go
// Set via ldflags during build
var (
    version   = "v0.1.0"  // Semantic version
    commit    = "unknown" // Git commit SHA
    buildDate = "unknown" // ISO8601 build timestamp
    goVersion = runtime.Version()
)

// Output:
// ztm v0.1.0
// commit: a1b2c3d4
// built: 2026-02-23T10:30:45Z
// go: go1.24.0
```

---

## Build Integration

### Makefile Updates

```makefile
ZTM_BIN := $(BINARY_DIR)/ztm
VERSION := v0.1.0
COMMIT := $(shell git rev-parse --short HEAD)
BUILD_DATE := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS := -ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildDate=$(BUILD_DATE)"

## ztm: build CLI binary
ztm:
	@mkdir -p $(BINARY_DIR)
	go build $(LDFLAGS) -o $(ZTM_BIN) ./cmd/ztm
	@echo "✅ Built: $(ZTM_BIN)"

## install-ztm: install CLI to $GOPATH/bin or /usr/local/bin
install-ztm: ztm
	@if [ -n "$(GOPATH)" ]; then \
		cp $(ZTM_BIN) $(GOPATH)/bin/ztm && echo "✅ Installed to $(GOPATH)/bin/ztm"; \
	else \
		sudo cp $(ZTM_BIN) /usr/local/bin/ztm && echo "✅ Installed to /usr/local/bin/ztm"; \
	fi

## ztm-release: build multi-platform binaries
ztm-release:
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o $(BINARY_DIR)/ztm-darwin-amd64 ./cmd/ztm
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $(BINARY_DIR)/ztm-darwin-arm64 ./cmd/ztm
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BINARY_DIR)/ztm-linux-amd64 ./cmd/ztm
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $(BINARY_DIR)/ztm-linux-arm64 ./cmd/ztm
	@echo "✅ Release binaries built in $(BINARY_DIR)/"
```

Update `all` target:
```makefile
all: build ztm
```

---

## Testing Strategy

### Unit Tests

```go
// cmd/ztm/cmd/tenant_test.go
func TestTenantCreateCommand(t *testing.T) {
    mockClient := &mockClient{
        createTenantFunc: func(ctx context.Context, req *api.CreateTenantRequest) (*api.Tenant, error) {
            return &api.Tenant{
                TenantID:      req.TenantID,
                Status:        "idle",
                IdleTimeoutS:  req.IdleTimeoutS,
            }, nil
        },
    }

    cmd := newTenantCreateCmd(mockClient)
    cmd.SetArgs([]string{"test", "token:123", "--idle-timeout", "600"})

    err := cmd.Execute()
    assert.NoError(t, err)
}
```

### Integration Tests

Test against local docker-compose stack:
```bash
# Start local stack
docker-compose up -d

# Run CLI integration tests
ZTM_ORCHESTRATOR_URL=http://localhost:8080 \
ZTM_ROUTER_URL=http://localhost:9090 \
go test ./cmd/ztm/... -tags=integration
```

---

## Migration Path

### Phase 1: Parallel Existence (v0.1.0)
- Keep both `scripts/ztm.sh` and `bin/ztm`
- Document Go CLI as recommended, bash as legacy
- Add deprecation notice to `ztm.sh` header

### Phase 2: Deprecation (v0.5.0)
- Update all docs to use `ztm` (Go binary)
- Move `ztm.sh` → `scripts/legacy/ztm.sh`
- Print deprecation warning when `ztm.sh` is executed

### Phase 3: Removal (v1.0.0)
- Delete `scripts/legacy/ztm.sh`
- Go CLI is the only official interface

---

## Version Roadmap

| Version | Features |
|---------|----------|
| **v0.1.0** | Feature parity with ztm.sh (tenant/webhook commands) |
| v0.2.0 | Add `ztm logs` (stream orchestrator/router/tenant logs) |
| v0.3.0 | Add `ztm status` (health dashboard, warm pool metrics) |
| v0.4.0 | Add `ztm exec <tenant>` (kubectl exec into tenant pod) |
| v0.5.0 | Add `ztm metrics` (wake latency, warm pool hit rate) |
| v1.0.0 | Production ready, remove bash script |

---

## Success Criteria

- [ ] All existing `ztm.sh` commands work identically
- [ ] Works without `--orchestrator-url` (kubectl exec default)
- [ ] Cross-platform: darwin/linux, amd64/arm64
- [ ] Error messages are clear and actionable
- [ ] Help text is comprehensive (`ztm --help`, `ztm tenant --help`)
- [ ] Binary size < 15MB
- [ ] Shell completions generated (`ztm completion bash|zsh|fish`)
- [ ] Zero new runtime dependencies (no python3 needed)

---

## Implementation Plan

See implementation plan in separate document (created via writing-plans skill).

---

## References

- Current bash CLI: `scripts/ztm.sh`
- Orchestrator API: `internal/api/handler.go`
- Operations guide: `docs/operations.md`
- Cobra docs: https://cobra.dev
