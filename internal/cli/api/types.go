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
