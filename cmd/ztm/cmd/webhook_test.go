package cmd

import (
	"bytes"
	stdcontext "context"
	"testing"

	"github.com/shawn/agentic-tenancy/internal/cli/api"
	"github.com/stretchr/testify/assert"
)

func TestWebhookRegisterCommand(t *testing.T) {
	mockClient := &api.MockClient{
		RegisterWebhookFunc: func(ctx stdcontext.Context, tenantID string) (*api.WebhookResponse, error) {
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
