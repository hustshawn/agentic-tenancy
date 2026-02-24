# Go CLI Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace bash-based `ztm.sh` with a cross-platform Go binary using Cobra framework, maintaining feature parity with existing CLI.

**Architecture:** Thin client using Cobra for command structure, kubectl exec for API calls (default), and clean separation between API client, k8s helpers, and output formatting. Two-phase rollout: parallel existence then deprecation.

**Tech Stack:** Go 1.24, Cobra v1.8.0, k8s.io/client-go (existing), stdlib exec/HTTP/JSON

---

## Task 1: Project Setup & Dependencies

**Files:**
- Modify: `go.mod`
- Create: `cmd/ztm/main.go`
- Create: `cmd/ztm/cmd/root.go`

**Step 1: Add Cobra dependency**

```bash
go get github.com/spf13/cobra@v1.8.0
```

Expected: `go.mod` and `go.sum` updated with cobra dependency

**Step 2: Create main.go entry point**

```go
// cmd/ztm/main.go
package main

import (
	"os"

	"github.com/shawn/agentic-tenancy/cmd/ztm/cmd"
)

var (
	version   = "v0.1.0"
	commit    = "unknown"
	buildDate = "unknown"
)

func main() {
	cmd.SetVersion(version, commit, buildDate)
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
```

**Step 3: Create root command**

```go
// cmd/ztm/cmd/root.go
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	version   string
	commit    string
	buildDate string

	// Global flags
	namespace       string
	context         string
	orchestratorURL string
	routerURL       string
	outputFormat    string
	noColor         bool
)

var rootCmd = &cobra.Command{
	Use:   "ztm",
	Short: "Agentic Tenancy CLI",
	Long: `ztm is a CLI for managing multi-tenant AI agent instances.

It provides commands to create, list, update, and delete tenants,
as well as register Telegram webhooks.`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	// Global flags
	rootCmd.PersistentFlags().StringVar(&namespace, "namespace", getEnvOrDefault("ZTM_NAMESPACE", "tenants"), "Kubernetes namespace")
	rootCmd.PersistentFlags().StringVar(&context, "context", os.Getenv("ZTM_KUBE_CONTEXT"), "kubectl context")
	rootCmd.PersistentFlags().StringVar(&orchestratorURL, "orchestrator-url", os.Getenv("ZTM_ORCHESTRATOR_URL"), "Orchestrator HTTP URL (bypasses kubectl)")
	rootCmd.PersistentFlags().StringVar(&routerURL, "router-url", os.Getenv("ZTM_ROUTER_URL"), "Router HTTP URL")
	rootCmd.PersistentFlags().StringVar(&outputFormat, "output", "table", "Output format: json|table")
	rootCmd.PersistentFlags().BoolVar(&noColor, "no-color", false, "Disable colored output")
}

func Execute() error {
	return rootCmd.Execute()
}

func SetVersion(v, c, d string) {
	version = v
	commit = c
	buildDate = d
}

func getEnvOrDefault(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}
```

**Step 4: Build and test basic execution**

```bash
mkdir -p bin
go build -o bin/ztm ./cmd/ztm
./bin/ztm --help
```

Expected: Help text displays with "Agentic Tenancy CLI" description

**Step 5: Commit**

```bash
git add go.mod go.sum cmd/ztm/
git commit -m "feat(cli): add Go CLI scaffolding with Cobra

- Add cobra dependency
- Create main.go entry point
- Add root command with global flags
- Support env vars: ZTM_NAMESPACE, ZTM_KUBE_CONTEXT, etc."
```

---

## Task 2: Output Formatting Package

**Files:**
- Create: `internal/cli/output/style.go`
- Create: `internal/cli/output/style_test.go`
- Create: `internal/cli/output/format.go`

**Step 1: Write test for colored output**

```go
// internal/cli/output/style_test.go
package output

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStyler_NoColor(t *testing.T) {
	s := NewStyler(true) // noColor = true
	result := s.Success("test")
	assert.Equal(t, "✓ test", result)
}

func TestStyler_WithColor(t *testing.T) {
	s := NewStyler(false) // noColor = false
	result := s.Success("test")
	assert.Contains(t, result, "✓")
	assert.Contains(t, result, "test")
	// Should contain ANSI codes
	assert.Contains(t, result, "\033[")
}

func TestStyler_Error(t *testing.T) {
	s := NewStyler(true)
	result := s.Error("failed")
	assert.Equal(t, "✗ failed", result)
}

func TestStyler_Info(t *testing.T) {
	s := NewStyler(true)
	result := s.Info("info message")
	assert.Equal(t, "ℹ info message", result)
}

func TestStyler_Warn(t *testing.T) {
	s := NewStyler(true)
	result := s.Warn("warning")
	assert.Equal(t, "⚠ warning", result)
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/cli/output -v
```

Expected: FAIL with "no buildable Go source files" or similar

**Step 3: Implement styler**

```go
// internal/cli/output/style.go
package output

import (
	"fmt"
	"io"
	"os"
)

const (
	colorReset  = "\033[0m"
	colorRed    = "\033[0;31m"
	colorGreen  = "\033[0;32m"
	colorYellow = "\033[1;33m"
	colorCyan   = "\033[0;36m"
)

type Styler struct {
	noColor bool
}

func NewStyler(noColor bool) *Styler {
	return &Styler{noColor: noColor}
}

func (s *Styler) Success(msg string) string {
	return s.format(colorGreen, "✓", msg)
}

func (s *Styler) Error(msg string) string {
	return s.format(colorRed, "✗", msg)
}

func (s *Styler) Info(msg string) string {
	return s.format(colorCyan, "ℹ", msg)
}

func (s *Styler) Warn(msg string) string {
	return s.format(colorYellow, "⚠", msg)
}

func (s *Styler) format(color, symbol, msg string) string {
	if s.noColor {
		return fmt.Sprintf("%s %s", symbol, msg)
	}
	return fmt.Sprintf("%s%s%s %s", color, symbol, colorReset, msg)
}

func (s *Styler) Fprint(w io.Writer, msg string) {
	fmt.Fprintln(w, msg)
}

func (s *Styler) FprintSuccess(w io.Writer, msg string) {
	s.Fprint(w, s.Success(msg))
}

func (s *Styler) FprintError(w io.Writer, msg string) {
	s.Fprint(w, s.Error(msg))
}

func (s *Styler) FprintInfo(w io.Writer, msg string) {
	s.Fprint(w, s.Info(msg))
}

func (s *Styler) FprintWarn(w io.Writer, msg string) {
	s.Fprint(w, s.Warn(msg))
}

// Print to stdout
func (s *Styler) PrintSuccess(msg string) {
	s.FprintSuccess(os.Stdout, msg)
}

func (s *Styler) PrintError(msg string) {
	s.FprintError(os.Stderr, msg)
}

func (s *Styler) PrintInfo(msg string) {
	s.FprintInfo(os.Stdout, msg)
}

func (s *Styler) PrintWarn(msg string) {
	s.FprintWarn(os.Stdout, msg)
}
```

**Step 4: Run test to verify it passes**

```bash
go test ./internal/cli/output -v
```

Expected: PASS for all styler tests

**Step 5: Write test for JSON formatter**

```go
// internal/cli/output/format.go - add to style_test.go
func TestFormatJSON(t *testing.T) {
	data := map[string]interface{}{
		"tenant_id": "alice",
		"status":    "running",
	}

	result, err := FormatJSON(data)
	assert.NoError(t, err)
	assert.Contains(t, result, "alice")
	assert.Contains(t, result, "running")
	assert.Contains(t, result, "\n") // Pretty-printed
}

func TestFormatJSON_Error(t *testing.T) {
	// channels cannot be marshaled
	data := make(chan int)

	_, err := FormatJSON(data)
	assert.Error(t, err)
}
```

**Step 6: Run test to verify it fails**

```bash
go test ./internal/cli/output -v
```

Expected: FAIL with "undefined: FormatJSON"

**Step 7: Implement JSON formatter**

```go
// internal/cli/output/format.go
package output

import (
	"encoding/json"
)

func FormatJSON(data interface{}) (string, error) {
	bytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}
```

**Step 8: Run test to verify it passes**

```bash
go test ./internal/cli/output -v
```

Expected: PASS for all tests

**Step 9: Commit**

```bash
git add internal/cli/output/
git commit -m "feat(cli): add output formatting with colors and JSON

- Add Styler for colored terminal output (✓, ✗, ℹ, ⚠)
- Support --no-color flag
- Add JSON pretty-printing helper
- Full test coverage"
```

---

## Task 3: Kubernetes Exec Client

**Files:**
- Create: `internal/cli/k8s/exec.go`
- Create: `internal/cli/k8s/exec_test.go`

**Step 1: Write test for kubectl exec wrapper**

```go
// internal/cli/k8s/exec_test.go
package k8s

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestBuildKubectlArgs_Basic(t *testing.T) {
	cfg := &Config{
		Namespace:  "tenants",
		Context:    "",
		Deployment: "orchestrator",
		Port:       8080,
	}

	args := buildKubectlArgs(cfg, "GET", "/tenants", nil)

	assert.Contains(t, args, "exec")
	assert.Contains(t, args, "-n")
	assert.Contains(t, args, "tenants")
	assert.Contains(t, args, "deployment/orchestrator")
	assert.Contains(t, args, "wget")
	assert.Contains(t, args, "--method=GET")
	assert.Contains(t, args, "http://localhost:8080/tenants")
}

func TestBuildKubectlArgs_WithContext(t *testing.T) {
	cfg := &Config{
		Namespace:  "tenants",
		Context:    "prod-cluster",
		Deployment: "orchestrator",
		Port:       8080,
	}

	args := buildKubectlArgs(cfg, "GET", "/tenants", nil)

	assert.Equal(t, "--context", args[0])
	assert.Equal(t, "prod-cluster", args[1])
}

func TestBuildKubectlArgs_WithBody(t *testing.T) {
	cfg := &Config{
		Namespace:  "tenants",
		Deployment: "orchestrator",
		Port:       8080,
	}

	body := []byte(`{"tenant_id":"alice"}`)
	args := buildKubectlArgs(cfg, "POST", "/tenants", body)

	assert.Contains(t, args, "--header=Content-Type: application/json")
	assert.Contains(t, args, "--body-data={\"tenant_id\":\"alice\"}")
}

func TestParseResponse_Success(t *testing.T) {
	output := []byte(`{"tenant_id":"alice","status":"idle"}`)

	result, err := parseResponse(output, nil)

	assert.NoError(t, err)
	assert.Equal(t, output, result)
}

func TestParseResponse_KubectlError(t *testing.T) {
	output := []byte("Error from server (NotFound): deployments.apps \"orchestrator\" not found")

	_, err := parseResponse(output, assert.AnError)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "kubectl exec failed")
	assert.Contains(t, err.Error(), "NotFound")
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./internal/cli/k8s -v
```

Expected: FAIL with "no buildable Go source files"

**Step 3: Implement kubectl exec wrapper**

```go
// internal/cli/k8s/exec.go
package k8s

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type Config struct {
	Namespace  string
	Context    string
	Deployment string
	Port       int
}

func ExecAPICall(ctx context.Context, cfg *Config, method, path string, body []byte) ([]byte, error) {
	args := buildKubectlArgs(cfg, method, path, body)

	cmd := exec.CommandContext(ctx, "kubectl", args...)
	output, err := cmd.CombinedOutput()

	return parseResponse(output, err)
}

func buildKubectlArgs(cfg *Config, method, path string, body []byte) []string {
	var args []string

	// Add context if specified
	if cfg.Context != "" {
		args = append(args, "--context", cfg.Context)
	}

	// kubectl exec command
	args = append(args,
		"exec",
		"-n", cfg.Namespace,
		fmt.Sprintf("deployment/%s", cfg.Deployment),
		"--",
		"wget",
		"-qO-",
		fmt.Sprintf("--method=%s", method),
	)

	// Add headers and body for POST/PATCH/PUT
	if body != nil && len(body) > 0 {
		args = append(args,
			"--header=Content-Type: application/json",
			fmt.Sprintf("--body-data=%s", string(body)),
		)
	}

	// Target URL (pod-local)
	url := fmt.Sprintf("http://localhost:%d%s", cfg.Port, path)
	args = append(args, url)

	return args
}

func parseResponse(output []byte, execErr error) ([]byte, error) {
	if execErr != nil {
		// kubectl exec failed
		errMsg := string(output)
		if strings.Contains(errMsg, "not found") {
			return nil, fmt.Errorf("kubectl exec failed: deployment not found. Make sure the deployment is running and namespace is correct.\n%s", errMsg)
		}
		if strings.Contains(errMsg, "No such file or directory") {
			return nil, fmt.Errorf("kubectl not found in PATH. Please install kubectl")
		}
		return nil, fmt.Errorf("kubectl exec failed: %w\n%s", execErr, errMsg)
	}

	return output, nil
}

func NewConfig(namespace, context, deployment string, port int) *Config {
	return &Config{
		Namespace:  namespace,
		Context:    context,
		Deployment: deployment,
		Port:       port,
	}
}
```

**Step 4: Run test to verify it passes**

```bash
go test ./internal/cli/k8s -v
```

Expected: PASS for all tests

**Step 5: Commit**

```bash
git add internal/cli/k8s/
git commit -m "feat(cli): add kubectl exec wrapper

- Build kubectl exec commands with context, namespace, deployment
- Support GET, POST, PATCH, DELETE methods
- Parse kubectl errors (not found, missing binary)
- Handle request body for POST/PATCH
- Full test coverage"
```

---

## Task 4: API Client Interface & Types

**Files:**
- Create: `internal/cli/api/types.go`
- Create: `internal/cli/api/client.go`
- Create: `internal/cli/api/client_test.go`

**Step 1: Define API types**

```go
// internal/cli/api/types.go
package api

import (
	"time"
)

type Tenant struct {
	TenantID      string    `json:"tenant_id"`
	Status        string    `json:"status"`
	BotToken      string    `json:"bot_token,omitempty"` // Redacted in most responses
	IdleTimeoutS  int       `json:"idle_timeout_s"`
	PodName       string    `json:"pod_name,omitempty"`
	PodIP         string    `json:"pod_ip,omitempty"`
	LastActiveAt  time.Time `json:"last_active_at,omitempty"`
	CreatedAt     time.Time `json:"created_at,omitempty"`
}

type CreateTenantRequest struct {
	TenantID     string `json:"tenant_id"`
	BotToken     string `json:"bot_token"`
	IdleTimeoutS int    `json:"idle_timeout_s"`
}

type UpdateTenantRequest struct {
	BotToken     *string `json:"bot_token,omitempty"`
	IdleTimeoutS *int    `json:"idle_timeout_s,omitempty"`
}

type WebhookResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	URL     string `json:"url,omitempty"`
}
```

**Step 2: Write test for client interface**

```go
// internal/cli/api/client_test.go
package api

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

// MockClient for testing
type MockClient struct {
	CreateTenantFunc    func(ctx context.Context, req *CreateTenantRequest) (*Tenant, error)
	DeleteTenantFunc    func(ctx context.Context, id string) error
	ListTenantsFunc     func(ctx context.Context) ([]Tenant, error)
	GetTenantFunc       func(ctx context.Context, id string) (*Tenant, error)
	UpdateTenantFunc    func(ctx context.Context, id string, req *UpdateTenantRequest) (*Tenant, error)
	RegisterWebhookFunc func(ctx context.Context, tenantID string) (*WebhookResponse, error)
}

func (m *MockClient) CreateTenant(ctx context.Context, req *CreateTenantRequest) (*Tenant, error) {
	if m.CreateTenantFunc != nil {
		return m.CreateTenantFunc(ctx, req)
	}
	return nil, nil
}

func (m *MockClient) DeleteTenant(ctx context.Context, id string) error {
	if m.DeleteTenantFunc != nil {
		return m.DeleteTenantFunc(ctx, id)
	}
	return nil
}

func (m *MockClient) ListTenants(ctx context.Context) ([]Tenant, error) {
	if m.ListTenantsFunc != nil {
		return m.ListTenantsFunc(ctx)
	}
	return nil, nil
}

func (m *MockClient) GetTenant(ctx context.Context, id string) (*Tenant, error) {
	if m.GetTenantFunc != nil {
		return m.GetTenantFunc(ctx, id)
	}
	return nil, nil
}

func (m *MockClient) UpdateTenant(ctx context.Context, id string, req *UpdateTenantRequest) (*Tenant, error) {
	if m.UpdateTenantFunc != nil {
		return m.UpdateTenantFunc(ctx, id, req)
	}
	return nil, nil
}

func (m *MockClient) RegisterWebhook(ctx context.Context, tenantID string) (*WebhookResponse, error) {
	if m.RegisterWebhookFunc != nil {
		return m.RegisterWebhookFunc(ctx, tenantID)
	}
	return nil, nil
}

func TestMockClient_CreateTenant(t *testing.T) {
	mock := &MockClient{
		CreateTenantFunc: func(ctx context.Context, req *CreateTenantRequest) (*Tenant, error) {
			return &Tenant{
				TenantID:     req.TenantID,
				Status:       "idle",
				IdleTimeoutS: req.IdleTimeoutS,
			}, nil
		},
	}

	tenant, err := mock.CreateTenant(context.Background(), &CreateTenantRequest{
		TenantID:     "test",
		BotToken:     "token",
		IdleTimeoutS: 600,
	})

	assert.NoError(t, err)
	assert.Equal(t, "test", tenant.TenantID)
	assert.Equal(t, "idle", tenant.Status)
}
```

**Step 3: Run test to verify it passes**

```bash
go test ./internal/cli/api -v
```

Expected: PASS

**Step 4: Define client interface**

```go
// internal/cli/api/client.go
package api

import (
	"context"
)

// Client is the interface for interacting with Orchestrator and Router APIs
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

**Step 5: Commit**

```bash
git add internal/cli/api/
git commit -m "feat(cli): add API client interface and types

- Define Tenant, CreateTenantRequest, UpdateTenantRequest types
- Define Client interface for orchestrator/router APIs
- Add MockClient for testing
- Match orchestrator API response structure"
```

---

## Task 5: Kubectl-Based API Client Implementation

**Files:**
- Create: `internal/cli/api/kubectl_client.go`
- Create: `internal/cli/api/kubectl_client_test.go`

**Step 1: Write test for kubectl client**

```go
// internal/cli/api/kubectl_client_test.go
package api

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestKubectlClient_BuildCreateTenantBody(t *testing.T) {
	req := &CreateTenantRequest{
		TenantID:     "alice",
		BotToken:     "token:123",
		IdleTimeoutS: 600,
	}

	body, err := json.Marshal(req)
	assert.NoError(t, err)

	var decoded CreateTenantRequest
	err = json.Unmarshal(body, &decoded)
	assert.NoError(t, err)
	assert.Equal(t, "alice", decoded.TenantID)
	assert.Equal(t, "token:123", decoded.BotToken)
	assert.Equal(t, 600, decoded.IdleTimeoutS)
}

func TestKubectlClient_BuildUpdateTenantBody(t *testing.T) {
	newToken := "new-token"
	newTimeout := 1800

	req := &UpdateTenantRequest{
		BotToken:     &newToken,
		IdleTimeoutS: &newTimeout,
	}

	body, err := json.Marshal(req)
	assert.NoError(t, err)

	var decoded UpdateTenantRequest
	err = json.Unmarshal(body, &decoded)
	assert.NoError(t, err)
	assert.NotNil(t, decoded.BotToken)
	assert.Equal(t, "new-token", *decoded.BotToken)
	assert.NotNil(t, decoded.IdleTimeoutS)
	assert.Equal(t, 1800, *decoded.IdleTimeoutS)
}

func TestKubectlClient_ParseTenant(t *testing.T) {
	responseJSON := `{
		"tenant_id": "alice",
		"status": "running",
		"idle_timeout_s": 3600,
		"pod_name": "zeroclaw-alice",
		"pod_ip": "10.0.1.5"
	}`

	var tenant Tenant
	err := json.Unmarshal([]byte(responseJSON), &tenant)
	assert.NoError(t, err)
	assert.Equal(t, "alice", tenant.TenantID)
	assert.Equal(t, "running", tenant.Status)
	assert.Equal(t, 3600, tenant.IdleTimeoutS)
}
```

**Step 2: Run test to verify it passes**

```bash
go test ./internal/cli/api -v -run TestKubectlClient
```

Expected: PASS

**Step 3: Implement kubectl client (part 1 - structure)**

```go
// internal/cli/api/kubectl_client.go
package api

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/shawn/agentic-tenancy/internal/cli/k8s"
)

type KubectlClient struct {
	orchestratorCfg *k8s.Config
	routerCfg       *k8s.Config
}

func NewKubectlClient(namespace, context string) *KubectlClient {
	return &KubectlClient{
		orchestratorCfg: k8s.NewConfig(namespace, context, "orchestrator", 8080),
		routerCfg:       k8s.NewConfig(namespace, context, "router", 9090),
	}
}

func (c *KubectlClient) CreateTenant(ctx context.Context, req *CreateTenantRequest) (*Tenant, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	resp, err := k8s.ExecAPICall(ctx, c.orchestratorCfg, "POST", "/tenants", body)
	if err != nil {
		return nil, fmt.Errorf("API call failed: %w", err)
	}

	var tenant Tenant
	if err := json.Unmarshal(resp, &tenant); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &tenant, nil
}

func (c *KubectlClient) DeleteTenant(ctx context.Context, id string) error {
	path := fmt.Sprintf("/tenants/%s", id)
	_, err := k8s.ExecAPICall(ctx, c.orchestratorCfg, "DELETE", path, nil)
	if err != nil {
		return fmt.Errorf("failed to delete tenant: %w", err)
	}
	return nil
}

func (c *KubectlClient) ListTenants(ctx context.Context) ([]Tenant, error) {
	resp, err := k8s.ExecAPICall(ctx, c.orchestratorCfg, "GET", "/tenants", nil)
	if err != nil {
		return nil, fmt.Errorf("API call failed: %w", err)
	}

	var tenants []Tenant
	if err := json.Unmarshal(resp, &tenants); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return tenants, nil
}

func (c *KubectlClient) GetTenant(ctx context.Context, id string) (*Tenant, error) {
	path := fmt.Sprintf("/tenants/%s", id)
	resp, err := k8s.ExecAPICall(ctx, c.orchestratorCfg, "GET", path, nil)
	if err != nil {
		return nil, fmt.Errorf("API call failed: %w", err)
	}

	var tenant Tenant
	if err := json.Unmarshal(resp, &tenant); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &tenant, nil
}

func (c *KubectlClient) UpdateTenant(ctx context.Context, id string, req *UpdateTenantRequest) (*Tenant, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	path := fmt.Sprintf("/tenants/%s", id)
	resp, err := k8s.ExecAPICall(ctx, c.orchestratorCfg, "PATCH", path, body)
	if err != nil {
		return nil, fmt.Errorf("API call failed: %w", err)
	}

	var tenant Tenant
	if err := json.Unmarshal(resp, &tenant); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &tenant, nil
}

func (c *KubectlClient) RegisterWebhook(ctx context.Context, tenantID string) (*WebhookResponse, error) {
	path := fmt.Sprintf("/admin/webhook/%s", tenantID)
	resp, err := k8s.ExecAPICall(ctx, c.routerCfg, "POST", path, nil)
	if err != nil {
		return nil, fmt.Errorf("API call failed: %w", err)
	}

	var webhook WebhookResponse
	if err := json.Unmarshal(resp, &webhook); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &webhook, nil
}
```

**Step 4: Commit**

```bash
git add internal/cli/api/
git commit -m "feat(cli): implement kubectl-based API client

- KubectlClient uses kubectl exec to call orchestrator/router
- Implements all Client interface methods
- JSON marshaling/unmarshaling for requests/responses
- Error wrapping with context"
```

---

## Task 6: Version Command

**Files:**
- Create: `cmd/ztm/cmd/version.go`
- Create: `cmd/ztm/cmd/version_test.go`

**Step 1: Write test for version command**

```go
// cmd/ztm/cmd/version_test.go
package cmd

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVersionCommand(t *testing.T) {
	// Set test version info
	SetVersion("v0.1.0-test", "abc123", "2026-02-23T10:00:00Z")

	cmd := newVersionCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	err := cmd.Execute()
	assert.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "v0.1.0-test")
	assert.Contains(t, output, "abc123")
	assert.Contains(t, output, "2026-02-23T10:00:00Z")
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./cmd/ztm/cmd -v -run TestVersionCommand
```

Expected: FAIL with "undefined: newVersionCmd"

**Step 3: Implement version command**

```go
// cmd/ztm/cmd/version.go
package cmd

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "ztm %s\n", version)
			fmt.Fprintf(cmd.OutOrStdout(), "commit: %s\n", commit)
			fmt.Fprintf(cmd.OutOrStdout(), "built: %s\n", buildDate)
			fmt.Fprintf(cmd.OutOrStdout(), "go: %s\n", runtime.Version())
		},
	}
}

func init() {
	rootCmd.AddCommand(newVersionCmd())
}
```

**Step 4: Run test to verify it passes**

```bash
go test ./cmd/ztm/cmd -v -run TestVersionCommand
```

Expected: PASS

**Step 5: Build and test**

```bash
go build -o bin/ztm ./cmd/ztm
./bin/ztm version
```

Expected: Version info displays

**Step 6: Commit**

```bash
git add cmd/ztm/cmd/version*
git commit -m "feat(cli): add version command

- Display version, commit, build date, go version
- Tested with mocked version info"
```

---

## Task 7: Tenant Create Command

**Files:**
- Create: `cmd/ztm/cmd/tenant_create.go`
- Create: `cmd/ztm/cmd/tenant_create_test.go`

**Step 1: Write test for tenant create**

```go
// cmd/ztm/cmd/tenant_create_test.go
package cmd

import (
	"bytes"
	"context"
	"testing"

	"github.com/shawn/agentic-tenancy/internal/cli/api"
	"github.com/stretchr/testify/assert"
)

func TestTenantCreateCommand(t *testing.T) {
	mockClient := &api.MockClient{
		CreateTenantFunc: func(ctx context.Context, req *api.CreateTenantRequest) (*api.Tenant, error) {
			assert.Equal(t, "alice", req.TenantID)
			assert.Equal(t, "token:123", req.BotToken)
			assert.Equal(t, 600, req.IdleTimeoutS)

			return &api.Tenant{
				TenantID:     req.TenantID,
				Status:       "idle",
				IdleTimeoutS: req.IdleTimeoutS,
			}, nil
		},
	}

	cmd := newTenantCreateCmd(mockClient)
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"alice", "token:123", "--idle-timeout", "600"})

	err := cmd.Execute()
	assert.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "alice")
	assert.Contains(t, output, "idle")
}

func TestTenantCreateCommand_MissingArgs(t *testing.T) {
	mockClient := &api.MockClient{}

	cmd := newTenantCreateCmd(mockClient)
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	assert.Error(t, err)
}

func TestTenantCreateCommand_DefaultTimeout(t *testing.T) {
	mockClient := &api.MockClient{
		CreateTenantFunc: func(ctx context.Context, req *api.CreateTenantRequest) (*api.Tenant, error) {
			assert.Equal(t, 600, req.IdleTimeoutS) // Default
			return &api.Tenant{TenantID: req.TenantID, Status: "idle"}, nil
		},
	}

	cmd := newTenantCreateCmd(mockClient)
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"alice", "token:123"})

	err := cmd.Execute()
	assert.NoError(t, err)
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./cmd/ztm/cmd -v -run TestTenantCreate
```

Expected: FAIL with "undefined: newTenantCreateCmd"

**Step 3: Implement tenant create command**

```go
// cmd/ztm/cmd/tenant_create.go
package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/shawn/agentic-tenancy/internal/cli/api"
	"github.com/shawn/agentic-tenancy/internal/cli/output"
	"github.com/spf13/cobra"
)

var idleTimeout int

func newTenantCreateCmd(client api.Client) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create <tenant-id> <bot-token>",
		Short: "Create a new tenant",
		Long: `Create a new tenant with the specified ID and Telegram bot token.

The tenant will be registered in DynamoDB and the Telegram webhook will
be auto-registered if ROUTER_PUBLIC_URL is configured on the orchestrator.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			tenantID := args[0]
			botToken := args[1]

			styler := output.NewStyler(noColor)
			styler.PrintInfo(fmt.Sprintf("Creating tenant '%s'...", tenantID))

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			tenant, err := client.CreateTenant(ctx, &api.CreateTenantRequest{
				TenantID:     tenantID,
				BotToken:     botToken,
				IdleTimeoutS: idleTimeout,
			})
			if err != nil {
				styler.PrintError(fmt.Sprintf("Failed to create tenant: %v", err))
				return err
			}

			styler.PrintSuccess(fmt.Sprintf("Tenant '%s' created", tenantID))

			// Format output
			if outputFormat == "json" {
				jsonStr, err := output.FormatJSON(tenant)
				if err != nil {
					return fmt.Errorf("failed to format output: %w", err)
				}
				fmt.Fprintln(cmd.OutOrStdout(), jsonStr)
			} else {
				// Table format
				fmt.Fprintf(cmd.OutOrStdout(), "\nTenant ID:     %s\n", tenant.TenantID)
				fmt.Fprintf(cmd.OutOrStdout(), "Status:        %s\n", tenant.Status)
				fmt.Fprintf(cmd.OutOrStdout(), "Idle Timeout:  %ds\n", tenant.IdleTimeoutS)
				if !tenant.CreatedAt.IsZero() {
					fmt.Fprintf(cmd.OutOrStdout(), "Created At:    %s\n", tenant.CreatedAt.Format(time.RFC3339))
				}
			}

			return nil
		},
	}

	cmd.Flags().IntVar(&idleTimeout, "idle-timeout", 600, "Idle timeout in seconds")

	return cmd
}
```

**Step 4: Run test to verify it passes**

```bash
go test ./cmd/ztm/cmd -v -run TestTenantCreate
```

Expected: PASS

**Step 5: Commit**

```bash
git add cmd/ztm/cmd/tenant_create*
git commit -m "feat(cli): add tenant create command

- Create tenant with ID, bot token, idle timeout
- Support --idle-timeout flag (default: 600s)
- JSON and table output formats
- Colored status messages
- Full test coverage with MockClient"
```

---

## Task 8: Tenant List, Get, Delete Commands

**Files:**
- Create: `cmd/ztm/cmd/tenant_other.go`
- Create: `cmd/ztm/cmd/tenant_other_test.go`

**Step 1: Write tests**

```go
// cmd/ztm/cmd/tenant_other_test.go
package cmd

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/shawn/agentic-tenancy/internal/cli/api"
	"github.com/stretchr/testify/assert"
)

func TestTenantListCommand(t *testing.T) {
	mockClient := &api.MockClient{
		ListTenantsFunc: func(ctx context.Context) ([]api.Tenant, error) {
			return []api.Tenant{
				{TenantID: "alice", Status: "running", IdleTimeoutS: 3600},
				{TenantID: "bob", Status: "idle", IdleTimeoutS: 600},
			}, nil
		},
	}

	cmd := newTenantListCmd(mockClient)
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	assert.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "alice")
	assert.Contains(t, output, "bob")
	assert.Contains(t, output, "running")
	assert.Contains(t, output, "idle")
}

func TestTenantGetCommand(t *testing.T) {
	mockClient := &api.MockClient{
		GetTenantFunc: func(ctx context.Context, id string) (*api.Tenant, error) {
			assert.Equal(t, "alice", id)
			return &api.Tenant{
				TenantID:     "alice",
				Status:       "running",
				IdleTimeoutS: 3600,
				PodIP:        "10.0.1.5",
			}, nil
		},
	}

	cmd := newTenantGetCmd(mockClient)
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"alice"})

	err := cmd.Execute()
	assert.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "alice")
	assert.Contains(t, output, "running")
	assert.Contains(t, output, "10.0.1.5")
}

func TestTenantDeleteCommand(t *testing.T) {
	deleted := false
	mockClient := &api.MockClient{
		DeleteTenantFunc: func(ctx context.Context, id string) error {
			assert.Equal(t, "alice", id)
			deleted = true
			return nil
		},
	}

	cmd := newTenantDeleteCmd(mockClient)
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"alice"})

	err := cmd.Execute()
	assert.NoError(t, err)
	assert.True(t, deleted)

	output := buf.String()
	assert.Contains(t, output, "deleted")
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./cmd/ztm/cmd -v -run TestTenant
```

Expected: FAIL with "undefined: newTenantListCmd"

**Step 3: Implement commands**

```go
// cmd/ztm/cmd/tenant_other.go
package cmd

import (
	"context"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/shawn/agentic-tenancy/internal/cli/api"
	"github.com/shawn/agentic-tenancy/internal/cli/output"
	"github.com/spf13/cobra"
)

func newTenantListCmd(client api.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all tenants",
		RunE: func(cmd *cobra.Command, args []string) error {
			styler := output.NewStyler(noColor)
			styler.PrintInfo("Listing tenants...")

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			tenants, err := client.ListTenants(ctx)
			if err != nil {
				styler.PrintError(fmt.Sprintf("Failed to list tenants: %v", err))
				return err
			}

			if outputFormat == "json" {
				jsonStr, err := output.FormatJSON(tenants)
				if err != nil {
					return fmt.Errorf("failed to format output: %w", err)
				}
				fmt.Fprintln(cmd.OutOrStdout(), jsonStr)
				return nil
			}

			// Table format
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "TENANT ID\tSTATUS\tLAST ACTIVE\tIDLE TIMEOUT")
			for _, t := range tenants {
				lastActive := "never"
				if !t.LastActiveAt.IsZero() {
					lastActive = t.LastActiveAt.Format("2006-01-02 15:04:05")
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%ds\n", t.TenantID, t.Status, lastActive, t.IdleTimeoutS)
			}
			w.Flush()

			return nil
		},
	}
}

func newTenantGetCmd(client api.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "get <tenant-id>",
		Short: "Get tenant details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tenantID := args[0]

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			tenant, err := client.GetTenant(ctx, tenantID)
			if err != nil {
				styler := output.NewStyler(noColor)
				styler.PrintError(fmt.Sprintf("Failed to get tenant: %v", err))
				return err
			}

			if outputFormat == "json" {
				jsonStr, err := output.FormatJSON(tenant)
				if err != nil {
					return fmt.Errorf("failed to format output: %w", err)
				}
				fmt.Fprintln(cmd.OutOrStdout(), jsonStr)
				return nil
			}

			// Table format
			fmt.Fprintf(cmd.OutOrStdout(), "Tenant ID:     %s\n", tenant.TenantID)
			fmt.Fprintf(cmd.OutOrStdout(), "Status:        %s\n", tenant.Status)
			fmt.Fprintf(cmd.OutOrStdout(), "Idle Timeout:  %ds\n", tenant.IdleTimeoutS)
			if tenant.PodName != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Pod Name:      %s\n", tenant.PodName)
			}
			if tenant.PodIP != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Pod IP:        %s\n", tenant.PodIP)
			}
			if !tenant.LastActiveAt.IsZero() {
				fmt.Fprintf(cmd.OutOrStdout(), "Last Active:   %s\n", tenant.LastActiveAt.Format(time.RFC3339))
			}
			if !tenant.CreatedAt.IsZero() {
				fmt.Fprintf(cmd.OutOrStdout(), "Created At:    %s\n", tenant.CreatedAt.Format(time.RFC3339))
			}

			return nil
		},
	}
}

func newTenantDeleteCmd(client api.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <tenant-id>",
		Short: "Delete a tenant",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tenantID := args[0]
			styler := output.NewStyler(noColor)
			styler.PrintInfo(fmt.Sprintf("Deleting tenant '%s'...", tenantID))

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			err := client.DeleteTenant(ctx, tenantID)
			if err != nil {
				styler.PrintError(fmt.Sprintf("Failed to delete tenant: %v", err))
				return err
			}

			styler.PrintSuccess(fmt.Sprintf("Tenant '%s' deleted", tenantID))
			return nil
		},
	}
}
```

**Step 4: Run test to verify it passes**

```bash
go test ./cmd/ztm/cmd -v -run TestTenant
```

Expected: PASS

**Step 5: Commit**

```bash
git add cmd/ztm/cmd/tenant_other*
git commit -m "feat(cli): add tenant list, get, delete commands

- tenant list: table format with status, last active, timeout
- tenant get: detailed view of single tenant
- tenant delete: remove tenant with confirmation
- JSON and table output support
- Full test coverage"
```

---

## Task 9: Tenant Update Command

**Files:**
- Create: `cmd/ztm/cmd/tenant_update.go`
- Create: `cmd/ztm/cmd/tenant_update_test.go`

**Step 1: Write tests**

```go
// cmd/ztm/cmd/tenant_update_test.go
package cmd

import (
	"bytes"
	"context"
	"testing"

	"github.com/shawn/agentic-tenancy/internal/cli/api"
	"github.com/stretchr/testify/assert"
)

func TestTenantUpdateCommand_BotToken(t *testing.T) {
	mockClient := &api.MockClient{
		UpdateTenantFunc: func(ctx context.Context, id string, req *api.UpdateTenantRequest) (*api.Tenant, error) {
			assert.Equal(t, "alice", id)
			assert.NotNil(t, req.BotToken)
			assert.Equal(t, "new-token:456", *req.BotToken)
			assert.Nil(t, req.IdleTimeoutS)

			return &api.Tenant{
				TenantID:     id,
				Status:       "idle",
				IdleTimeoutS: 600,
			}, nil
		},
	}

	cmd := newTenantUpdateCmd(mockClient)
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"alice", "--bot-token", "new-token:456"})

	err := cmd.Execute()
	assert.NoError(t, err)
}

func TestTenantUpdateCommand_IdleTimeout(t *testing.T) {
	mockClient := &api.MockClient{
		UpdateTenantFunc: func(ctx context.Context, id string, req *api.UpdateTenantRequest) (*api.Tenant, error) {
			assert.Equal(t, "alice", id)
			assert.Nil(t, req.BotToken)
			assert.NotNil(t, req.IdleTimeoutS)
			assert.Equal(t, 1800, *req.IdleTimeoutS)

			return &api.Tenant{
				TenantID:     id,
				Status:       "idle",
				IdleTimeoutS: 1800,
			}, nil
		},
	}

	cmd := newTenantUpdateCmd(mockClient)
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"alice", "--idle-timeout", "1800"})

	err := cmd.Execute()
	assert.NoError(t, err)
}

func TestTenantUpdateCommand_Both(t *testing.T) {
	mockClient := &api.MockClient{
		UpdateTenantFunc: func(ctx context.Context, id string, req *api.UpdateTenantRequest) (*api.Tenant, error) {
			assert.NotNil(t, req.BotToken)
			assert.NotNil(t, req.IdleTimeoutS)
			return &api.Tenant{TenantID: id, Status: "idle"}, nil
		},
	}

	cmd := newTenantUpdateCmd(mockClient)
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"alice", "--bot-token", "token", "--idle-timeout", "900"})

	err := cmd.Execute()
	assert.NoError(t, err)
}

func TestTenantUpdateCommand_NoFlags(t *testing.T) {
	mockClient := &api.MockClient{}

	cmd := newTenantUpdateCmd(mockClient)
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"alice"})

	err := cmd.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "at least one")
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./cmd/ztm/cmd -v -run TestTenantUpdate
```

Expected: FAIL with "undefined: newTenantUpdateCmd"

**Step 3: Implement update command**

```go
// cmd/ztm/cmd/tenant_update.go
package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/shawn/agentic-tenancy/internal/cli/api"
	"github.com/shawn/agentic-tenancy/internal/cli/output"
	"github.com/spf13/cobra"
)

var (
	updateBotToken     string
	updateIdleTimeout  int
	updateBotTokenSet  bool
	updateTimeoutSet   bool
)

func newTenantUpdateCmd(client api.Client) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update <tenant-id>",
		Short: "Update tenant configuration",
		Long: `Update bot token and/or idle timeout for an existing tenant.

At least one of --bot-token or --idle-timeout must be specified.`,
		Args: cobra.ExactArgs(1),
		PreRunE: func(cmd *cobra.Command, args []string) error {
			updateBotTokenSet = cmd.Flags().Changed("bot-token")
			updateTimeoutSet = cmd.Flags().Changed("idle-timeout")

			if !updateBotTokenSet && !updateTimeoutSet {
				return fmt.Errorf("at least one of --bot-token or --idle-timeout must be specified")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			tenantID := args[0]
			styler := output.NewStyler(noColor)
			styler.PrintInfo(fmt.Sprintf("Updating tenant '%s'...", tenantID))

			req := &api.UpdateTenantRequest{}
			if updateBotTokenSet {
				req.BotToken = &updateBotToken
			}
			if updateTimeoutSet {
				req.IdleTimeoutS = &updateIdleTimeout
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			tenant, err := client.UpdateTenant(ctx, tenantID, req)
			if err != nil {
				styler.PrintError(fmt.Sprintf("Failed to update tenant: %v", err))
				return err
			}

			styler.PrintSuccess(fmt.Sprintf("Tenant '%s' updated", tenantID))

			// Format output
			if outputFormat == "json" {
				jsonStr, err := output.FormatJSON(tenant)
				if err != nil {
					return fmt.Errorf("failed to format output: %w", err)
				}
				fmt.Fprintln(cmd.OutOrStdout(), jsonStr)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "\nTenant ID:     %s\n", tenant.TenantID)
				fmt.Fprintf(cmd.OutOrStdout(), "Status:        %s\n", tenant.Status)
				fmt.Fprintf(cmd.OutOrStdout(), "Idle Timeout:  %ds\n", tenant.IdleTimeoutS)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&updateBotToken, "bot-token", "", "New Telegram bot token")
	cmd.Flags().IntVar(&updateIdleTimeout, "idle-timeout", 0, "New idle timeout in seconds")

	return cmd
}
```

**Step 4: Run test to verify it passes**

```bash
go test ./cmd/ztm/cmd -v -run TestTenantUpdate
```

Expected: PASS

**Step 5: Commit**

```bash
git add cmd/ztm/cmd/tenant_update*
git commit -m "feat(cli): add tenant update command

- Update bot token and/or idle timeout
- Require at least one flag to be specified
- Support partial updates (only changed fields sent)
- JSON and table output
- Full test coverage"
```

---

## Task 10: Tenant Command Group & Webhook Command

**Files:**
- Create: `cmd/ztm/cmd/tenant.go`
- Create: `cmd/ztm/cmd/webhook.go`
- Create: `cmd/ztm/cmd/webhook_test.go`
- Modify: `cmd/ztm/cmd/root.go`

**Step 1: Create tenant command group**

```go
// cmd/ztm/cmd/tenant.go
package cmd

import (
	"github.com/shawn/agentic-tenancy/internal/cli/api"
	"github.com/spf13/cobra"
)

func newTenantCmd(client api.Client) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tenant",
		Short: "Manage tenants",
		Long:  `Create, list, get, update, and delete tenants.`,
	}

	// Add subcommands
	cmd.AddCommand(newTenantCreateCmd(client))
	cmd.AddCommand(newTenantListCmd(client))
	cmd.AddCommand(newTenantGetCmd(client))
	cmd.AddCommand(newTenantUpdateCmd(client))
	cmd.AddCommand(newTenantDeleteCmd(client))

	return cmd
}

func init() {
	// Client will be initialized in root.go based on flags
	rootCmd.AddCommand(newTenantCmd(nil))
}
```

**Step 2: Write test for webhook command**

```go
// cmd/ztm/cmd/webhook_test.go
package cmd

import (
	"bytes"
	"context"
	"testing"

	"github.com/shawn/agentic-tenancy/internal/cli/api"
	"github.com/stretchr/testify/assert"
)

func TestWebhookRegisterCommand(t *testing.T) {
	mockClient := &api.MockClient{
		RegisterWebhookFunc: func(ctx context.Context, tenantID string) (*api.WebhookResponse, error) {
			assert.Equal(t, "alice", tenantID)
			return &api.WebhookResponse{
				Success: true,
				Message: "Webhook registered",
				URL:     "https://example.com/tg/alice",
			}, nil
		},
	}

	cmd := newWebhookRegisterCmd(mockClient)
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"alice"})

	err := cmd.Execute()
	assert.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "registered")
}
```

**Step 3: Run test to verify it fails**

```bash
go test ./cmd/ztm/cmd -v -run TestWebhook
```

Expected: FAIL with "undefined: newWebhookRegisterCmd"

**Step 4: Implement webhook command**

```go
// cmd/ztm/cmd/webhook.go
package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/shawn/agentic-tenancy/internal/cli/api"
	"github.com/shawn/agentic-tenancy/internal/cli/output"
	"github.com/spf13/cobra"
)

func newWebhookRegisterCmd(client api.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "register <tenant-id>",
		Short: "Register Telegram webhook for a tenant",
		Long: `Register the Telegram webhook for a tenant.

This is normally done automatically when creating a tenant if ROUTER_PUBLIC_URL
is configured on the orchestrator. Use this command to manually register or
re-register a webhook.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tenantID := args[0]
			styler := output.NewStyler(noColor)
			styler.PrintInfo(fmt.Sprintf("Registering webhook for tenant '%s'...", tenantID))

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			resp, err := client.RegisterWebhook(ctx, tenantID)
			if err != nil {
				styler.PrintError(fmt.Sprintf("Failed to register webhook: %v", err))
				return err
			}

			styler.PrintSuccess("Webhook registered")

			if outputFormat == "json" {
				jsonStr, err := output.FormatJSON(resp)
				if err != nil {
					return fmt.Errorf("failed to format output: %w", err)
				}
				fmt.Fprintln(cmd.OutOrStdout(), jsonStr)
			} else {
				if resp.URL != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "\nWebhook URL: %s\n", resp.URL)
				}
				if resp.Message != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "Message:     %s\n", resp.Message)
				}
			}

			return nil
		},
	}
}

func newWebhookCmd(client api.Client) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "webhook",
		Short: "Manage Telegram webhooks",
		Long:  `Register Telegram webhooks for tenants.`,
	}

	cmd.AddCommand(newWebhookRegisterCmd(client))

	return cmd
}

func init() {
	rootCmd.AddCommand(newWebhookCmd(nil))
}
```

**Step 5: Run test to verify it passes**

```bash
go test ./cmd/ztm/cmd -v -run TestWebhook
```

Expected: PASS

**Step 6: Update root.go to wire up client**

```go
// cmd/ztm/cmd/root.go - add before Execute()
func initClient() api.Client {
	// For now, always use kubectl client
	// HTTP client can be added later when --orchestrator-url is provided
	return api.NewKubectlClient(namespace, context)
}

// Update Execute() function
func Execute() error {
	// Wire up client for all commands
	client := initClient()

	// Replace nil clients with actual client
	for _, cmd := range rootCmd.Commands() {
		if cmd.Use == "tenant" {
			cmd.ResetCommands()
			for _, subcmd := range newTenantCmd(client).Commands() {
				cmd.AddCommand(subcmd)
			}
		}
		if cmd.Use == "webhook" {
			cmd.ResetCommands()
			for _, subcmd := range newWebhookCmd(client).Commands() {
				cmd.AddCommand(subcmd)
			}
		}
	}

	return rootCmd.Execute()
}
```

**Step 7: Commit**

```bash
git add cmd/ztm/cmd/tenant.go cmd/ztm/cmd/webhook* cmd/ztm/cmd/root.go
git commit -m "feat(cli): add tenant and webhook command groups

- tenant command group with all subcommands
- webhook register command
- Wire up kubectl client in root command
- Full test coverage for webhook command"
```

---

## Task 11: Makefile Integration

**Files:**
- Modify: `Makefile`

**Step 1: Add ztm targets to Makefile**

```makefile
# Append to Makefile after existing targets

ZTM_BIN := $(BINARY_DIR)/ztm
VERSION := v0.1.0
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
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
		mkdir -p $(GOPATH)/bin && \
		cp $(ZTM_BIN) $(GOPATH)/bin/ztm && \
		echo "✅ Installed to $(GOPATH)/bin/ztm"; \
	else \
		sudo cp $(ZTM_BIN) /usr/local/bin/ztm && \
		echo "✅ Installed to /usr/local/bin/ztm"; \
	fi

## ztm-release: build multi-platform binaries
ztm-release:
	@mkdir -p $(BINARY_DIR)/release
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o $(BINARY_DIR)/release/ztm-darwin-amd64 ./cmd/ztm
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $(BINARY_DIR)/release/ztm-darwin-arm64 ./cmd/ztm
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BINARY_DIR)/release/ztm-linux-amd64 ./cmd/ztm
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $(BINARY_DIR)/release/ztm-linux-arm64 ./cmd/ztm
	@echo "✅ Release binaries built in $(BINARY_DIR)/release/"
	@ls -lh $(BINARY_DIR)/release/

## test-cli: run CLI tests
test-cli:
	go test ./cmd/ztm/... ./internal/cli/... -v -timeout 30s
```

**Step 2: Update all target**

```makefile
# Change line 7 from:
all: build

# To:
all: build ztm
```

**Step 3: Update clean target**

```makefile
# Change clean target from:
clean:
	rm -rf $(BINARY_DIR) coverage.out coverage.html

# To:
clean:
	rm -rf $(BINARY_DIR) coverage.out coverage.html
```

**Step 4: Test Makefile targets**

```bash
make ztm
./bin/ztm version
make test-cli
```

Expected: Binary builds with version info, tests pass

**Step 5: Commit**

```bash
git add Makefile
git commit -m "build: add ztm CLI targets to Makefile

- make ztm: build CLI with ldflags (version, commit, date)
- make install-ztm: install to GOPATH or /usr/local/bin
- make ztm-release: build multi-platform binaries
- make test-cli: run CLI tests
- Update 'all' target to include ztm"
```

---

## Task 12: Documentation Updates

**Files:**
- Modify: `README.md`
- Modify: `docs/operations.md`
- Create: `docs/cli-migration.md`

**Step 1: Update README.md**

Add after "Quick Start" section:

```markdown
### CLI Installation

The `ztm` CLI is a Go binary that manages tenant operations. It requires `kubectl` and a valid kubeconfig.

```bash
# Build from source
make ztm

# Install to PATH
make install-ztm

# Verify installation
ztm version
```

### Using the CLI

```bash
# Create a tenant
ztm tenant create alice 1234567890:AAHxyz --idle-timeout 3600

# List tenants
ztm tenant list

# Get tenant details
ztm tenant get alice

# Update tenant
ztm tenant update alice --idle-timeout 1800

# Register webhook
ztm webhook register alice

# Delete tenant
ztm tenant delete alice

# Use different cluster
ztm tenant list --context prod-eks --namespace production

# JSON output
ztm tenant get alice --output json
```

See [docs/operations.md](docs/operations.md) for complete CLI reference.
```

**Step 2: Update docs/operations.md**

Replace the "ztm CLI Reference" section (lines 7-92) with:

```markdown
## ztm CLI Reference

`ztm` is a Go-based CLI for managing tenants. It uses `kubectl exec` by default to call orchestrator/router APIs in-cluster.

### Installation

```bash
# Build from source
make ztm

# Install to PATH
make install-ztm

# Verify
ztm version
```

### Global Flags

Available on all commands:

```
--namespace string       Kubernetes namespace (default: tenants)
--context string         kubectl context (default: current context)
--orchestrator-url       Direct HTTP URL (bypasses kubectl)
--router-url            Router public URL
--output string         Output format: json|table (default: table)
--no-color              Disable colored output
```

Environment variables:
- `ZTM_NAMESPACE` - Kubernetes namespace
- `ZTM_KUBE_CONTEXT` - kubectl context
- `ZTM_ORCHESTRATOR_URL` - Orchestrator HTTP URL
- `ZTM_ROUTER_URL` - Router public URL

### Tenant Commands

#### Create Tenant

```bash
ztm tenant create <id> <bot_token> [--idle-timeout <secs>]
```

Creates a DynamoDB record and auto-registers the Telegram webhook.

```bash
# Create with 1-hour idle timeout
ztm tenant create alice 1234567890:AAHxyz --idle-timeout 3600

# Default timeout (600s = 10min)
ztm tenant create bob 9876543210:AABabc
```

#### List Tenants

```bash
ztm tenant list [--output json]
```

Returns all tenants with status, last active time, and idle timeout.

```bash
ztm tenant list
ztm tenant list --output json
```

#### Get Tenant

```bash
ztm tenant get <id> [--output json]
```

Shows detailed information for a single tenant.

```bash
ztm tenant get alice
ztm tenant get alice --output json
```

#### Update Tenant

```bash
ztm tenant update <id> [--bot-token <token>] [--idle-timeout <secs>]
```

Updates bot token and/or idle timeout. At least one flag required.

```bash
# Update bot token
ztm tenant update alice --bot-token 1111:AAHnew

# Update idle timeout
ztm tenant update alice --idle-timeout 1800

# Update both
ztm tenant update alice --bot-token 2222:AAH --idle-timeout 3600
```

#### Delete Tenant

```bash
ztm tenant delete <id>
```

Deletes the tenant, pod (if running), PVC/PV, Redis cache, and webhook.

```bash
ztm tenant delete alice
```

### Webhook Commands

#### Register Webhook

```bash
ztm webhook register <id>
```

Manually registers the Telegram webhook. Normally auto-registered on create.

```bash
ztm webhook register alice
```

---

## Legacy Bash CLI

The original bash-based `scripts/ztm.sh` is deprecated and will be removed in v1.0.0. Use the Go binary instead.

Migration guide: [docs/cli-migration.md](docs/cli-migration.md)
```

**Step 3: Create migration guide**

```markdown
// docs/cli-migration.md
# CLI Migration Guide: Bash → Go

This guide helps migrate from the legacy bash CLI (`scripts/ztm.sh`) to the new Go binary (`ztm`).

## Installation

**Old (bash):**
```bash
# Add to PATH
export PATH="$PATH:/path/to/agentic-tenancy/scripts"
ztm.sh tenant list
```

**New (Go):**
```bash
# Build and install
make install-ztm

# Use directly
ztm tenant list
```

## Command Changes

All commands are **identical** except for the binary name:

| Old | New |
|-----|-----|
| `ztm.sh tenant create ...` | `ztm tenant create ...` |
| `ztm.sh tenant list` | `ztm tenant list` |
| `ztm.sh tenant get <id>` | `ztm tenant get <id>` |
| `ztm.sh tenant update ...` | `ztm tenant update ...` |
| `ztm.sh tenant delete <id>` | `ztm tenant delete <id>` |
| `ztm.sh webhook register <id>` | `ztm webhook register <id>` |

## Environment Variables

Same variables, same behavior:

- `ZTM_NAMESPACE` (default: tenants)
- `ZTM_KUBE_CONTEXT` (default: current context)
- `ZTM_ORCHESTRATOR_URL` (default: use kubectl exec)
- `ZTM_ROUTER_URL` (default: use kubectl exec)

## Flags

Same flags, same behavior:

- `--namespace`
- `--context`
- `--orchestrator-url`
- `--router-url`
- `--idle-timeout` (tenant create/update)
- `--bot-token` (tenant update)

## New Features in Go CLI

- `--output json` - JSON output for all commands
- `--no-color` - Disable colored output
- `ztm version` - Show version info
- Better error messages
- Shell completions (coming in v0.2.0)
- No python3 dependency for JSON formatting

## Removed Dependencies

**Old requirements:**
- bash
- kubectl
- curl
- python3 (for JSON formatting)

**New requirements:**
- kubectl only (no python3, no curl)

## Deprecation Timeline

| Version | Status |
|---------|--------|
| v0.1.0  | Both CLIs available, Go recommended |
| v0.5.0  | Bash CLI prints deprecation warning |
| v1.0.0  | Bash CLI removed |

## Migration Checklist

- [ ] Install Go CLI: `make install-ztm`
- [ ] Test commands: `ztm tenant list`
- [ ] Update scripts/CI to use `ztm` instead of `ztm.sh`
- [ ] Update documentation/runbooks
- [ ] Remove `scripts/ztm.sh` from PATH
```

**Step 4: Commit**

```bash
git add README.md docs/operations.md docs/cli-migration.md
git commit -m "docs: update documentation for Go CLI

- Add installation instructions to README
- Replace ztm.sh reference in operations.md with Go CLI
- Add CLI migration guide (bash → Go)
- Document all commands, flags, and features
- Mark bash CLI as deprecated"
```

---

## Task 13: Add Deprecation Notice to Bash Script

**Files:**
- Modify: `scripts/ztm.sh`

**Step 1: Add deprecation notice**

Add after line 18 (after `set -euo pipefail`):

```bash
# ── Deprecation Notice ────────────────────────────────────────────────────────
cat >&2 <<EOF
${YELLOW}⚠ DEPRECATION NOTICE:${NC}
This bash-based CLI is deprecated and will be removed in v1.0.0.
Please migrate to the Go binary: ${CYAN}make install-ztm${NC}

See docs/cli-migration.md for migration guide.

EOF
```

**Step 2: Test deprecation notice**

```bash
./scripts/ztm.sh --help
```

Expected: Yellow deprecation warning displays, then help text

**Step 3: Commit**

```bash
git add scripts/ztm.sh
git commit -m "chore: add deprecation notice to bash CLI

- Print yellow warning when ztm.sh is executed
- Direct users to Go CLI (make install-ztm)
- Reference migration guide"
```

---

## Task 14: Integration Testing & Verification

**Files:**
- Create: `cmd/ztm/integration_test.go`
- Create: `scripts/test-cli-integration.sh`

**Step 1: Create integration test**

```go
// cmd/ztm/integration_test.go
//go:build integration
// +build integration

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCLI_Integration(t *testing.T) {
	// Build CLI
	buildCmd := exec.Command("go", "build", "-o", "../../bin/ztm-test", "./cmd/ztm")
	err := buildCmd.Run()
	require.NoError(t, err, "Failed to build CLI")
	defer os.Remove("../../bin/ztm-test")

	// Test version command
	t.Run("Version", func(t *testing.T) {
		cmd := exec.Command("../../bin/ztm-test", "version")
		output, err := cmd.CombinedOutput()
		require.NoError(t, err)
		assert.Contains(t, string(output), "ztm")
		assert.Contains(t, string(output), "commit:")
	})

	// Test help
	t.Run("Help", func(t *testing.T) {
		cmd := exec.Command("../../bin/ztm-test", "--help")
		output, err := cmd.CombinedOutput()
		require.NoError(t, err)
		assert.Contains(t, string(output), "Agentic Tenancy CLI")
		assert.Contains(t, string(output), "tenant")
		assert.Contains(t, string(output), "webhook")
	})

	// Test tenant help
	t.Run("TenantHelp", func(t *testing.T) {
		cmd := exec.Command("../../bin/ztm-test", "tenant", "--help")
		output, err := cmd.CombinedOutput()
		require.NoError(t, err)
		assert.Contains(t, string(output), "create")
		assert.Contains(t, string(output), "list")
		assert.Contains(t, string(output), "delete")
	})

	// Test invalid command
	t.Run("InvalidCommand", func(t *testing.T) {
		cmd := exec.Command("../../bin/ztm-test", "invalid-command")
		output, err := cmd.CombinedOutput()
		assert.Error(t, err)
		assert.Contains(t, string(output), "unknown command")
	})
}

func TestCLI_Flags(t *testing.T) {
	cmd := exec.Command("go", "build", "-o", "../../bin/ztm-test", "./cmd/ztm")
	err := cmd.Run()
	require.NoError(t, err)
	defer os.Remove("../../bin/ztm-test")

	// Test global flags parsing
	t.Run("GlobalFlags", func(t *testing.T) {
		cmd := exec.Command("../../bin/ztm-test",
			"tenant", "list",
			"--namespace", "test-ns",
			"--context", "test-ctx",
			"--output", "json",
			"--no-color")

		// This will fail (no cluster) but we're testing flag parsing
		output, _ := cmd.CombinedOutput()

		// Should attempt to exec kubectl (proves flags were parsed)
		outputStr := string(output)
		if !strings.Contains(outputStr, "kubectl") && !strings.Contains(outputStr, "exec") {
			t.Skip("No kubectl available, skipping")
		}
	})
}
```

**Step 2: Create bash integration test script**

```bash
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
```

**Step 3: Make script executable and run tests**

```bash
chmod +x scripts/test-cli-integration.sh
./scripts/test-cli-integration.sh
```

Expected: All tests pass

**Step 4: Run Go integration tests**

```bash
go test ./cmd/ztm -tags=integration -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add cmd/ztm/integration_test.go scripts/test-cli-integration.sh
git commit -m "test: add CLI integration tests

- Go integration tests for help, version, flags
- Bash integration test script
- Tests work without cluster (basic CLI functionality)
- Optional API tests if cluster is available"
```

---

## Task 15: Final Verification & Release Prep

**Files:**
- Modify: `README.md` (add badge)
- Create: `CHANGELOG.md`

**Step 1: Run all tests**

```bash
make test
make test-cli
./scripts/test-cli-integration.sh
```

Expected: All tests pass

**Step 2: Build release binaries**

```bash
make ztm-release
ls -lh bin/release/
```

Expected: 4 binaries (darwin/linux, amd64/arm64), each < 15MB

**Step 3: Create CHANGELOG**

```markdown
// CHANGELOG.md
# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [v0.1.0] - 2026-02-23

### Added

- Go-based CLI using Cobra framework
- All commands from bash CLI: tenant (create/list/get/update/delete), webhook (register)
- kubectl exec-based API client (no direct HTTP dependencies)
- Colored terminal output with --no-color flag
- JSON output format (--output json)
- Global flags: --namespace, --context, --orchestrator-url, --router-url
- Version command with build info
- Cross-platform support (darwin/linux, amd64/arm64)
- Comprehensive test coverage (unit + integration)
- Documentation updates and migration guide

### Changed

- Bash CLI (`scripts/ztm.sh`) marked as deprecated
- README updated with Go CLI installation instructions

### Deprecated

- `scripts/ztm.sh` will be removed in v1.0.0

## [Unreleased]

### Planned for v0.2.0

- `ztm logs` command (stream logs from orchestrator/router/tenant pods)
- Shell completions (bash/zsh/fish)

### Planned for v0.3.0

- `ztm status` command (health dashboard, warm pool metrics)

### Planned for v1.0.0

- Remove bash CLI
- Mark as production-ready
```

**Step 4: Update README with version badge**

Add after title:

```markdown
# Agentic Tenancy

[![Version](https://img.shields.io/badge/version-v0.1.0-blue.svg)](CHANGELOG.md)
[![Go](https://img.shields.io/badge/go-1.24-00ADD8.svg)](go.mod)
```

**Step 5: Commit**

```bash
git add CHANGELOG.md README.md
git commit -m "chore: prepare v0.1.0 release

- Add CHANGELOG with v0.1.0 features
- Add version badge to README
- Document deprecation of bash CLI
- Outline roadmap for v0.2.0, v0.3.0, v1.0.0"
```

**Step 6: Create git tag**

```bash
git tag -a v0.1.0 -m "Release v0.1.0: Go CLI with feature parity to bash CLI"
```

**Step 7: Final verification**

```bash
# Build and test
make clean
make all
make test-cli
./bin/ztm version

# Verify version in binary
./bin/ztm version | grep "v0.1.0"

# Test help
./bin/ztm --help
./bin/ztm tenant --help
./bin/ztm webhook --help
```

Expected: All commands work, version is v0.1.0

**Step 8: Push (when ready)**

```bash
# Don't run this now - just documenting for later
# git push origin main
# git push origin v0.1.0
```

---

## Completion Checklist

Verify all success criteria:

- [ ] All existing `ztm.sh` commands work identically
- [ ] Works without `--orchestrator-url` (kubectl exec default)
- [ ] Cross-platform: darwin/linux, amd64/arm64
- [ ] Error messages are clear and actionable
- [ ] Help text is comprehensive
- [ ] Binary size < 15MB
- [ ] Zero new runtime dependencies
- [ ] Bash CLI marked as deprecated
- [ ] Documentation updated
- [ ] All tests passing

---

## Next Steps

After v0.1.0 is complete:

1. **User testing**: Get feedback from operators using the CLI
2. **v0.2.0 planning**: Design logs streaming feature
3. **Shell completions**: Generate and distribute completion scripts
4. **GoReleaser**: Automate multi-platform builds and GitHub releases
5. **Docker image**: Bundle CLI in container for CI/CD use

---

## Notes

- Keep commits small and focused (one feature per commit)
- Run tests after each task
- Use TDD: write test → fail → implement → pass
- Follow DRY, YAGNI principles
- Error messages should guide users to solutions
