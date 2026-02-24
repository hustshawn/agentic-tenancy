package api

import (
	"context"
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
