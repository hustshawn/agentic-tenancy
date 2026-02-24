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
