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
