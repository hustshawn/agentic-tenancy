package reconciler

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	k8sclient "github.com/shawn/agentic-tenancy/internal/k8s"
	"github.com/shawn/agentic-tenancy/internal/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestReconcile_MissingPodResetsState(t *testing.T) {
	ctx := context.Background()
	reg := registry.NewMock()
	fakeCS := fake.NewSimpleClientset()
	k8s := k8sclient.New(fakeCS, k8sclient.Config{})

	// Use a real Redis or a mini-redis; for unit test we use a no-op approach.
	// Since we can't easily mock redis.Client, we'll use a client that connects to nothing
	// and accept the Redis error (the reconciler logs but continues).
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:59999"}) // non-existent, will error on Del

	// Create a tenant marked as "running" in the registry but with no actual pod
	err := reg.CreateTenant(ctx, &registry.TenantRecord{
		TenantID:     "abc123",
		Status:       registry.StatusRunning,
		PodName:      "zeroclaw-abc123",
		PodIP:        "10.0.0.1",
		Namespace:    "tenants",
		CreatedAt:    time.Now(),
		LastActiveAt: time.Now(),
	})
	require.NoError(t, err)

	rec := New(reg, k8s, rdb, "tenants")
	rec.reconcile(ctx)

	// Verify the tenant was reset to idle
	tenant, err := reg.GetTenant(ctx, "abc123")
	require.NoError(t, err)
	assert.Equal(t, registry.StatusIdle, tenant.Status)
	assert.Empty(t, tenant.PodName)
	assert.Empty(t, tenant.PodIP)
}

func TestReconcile_ExistingPodNotReset(t *testing.T) {
	ctx := context.Background()
	reg := registry.NewMock()

	// Create a fake pod in k8s
	fakeCS := fake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "zeroclaw-def456",
			Namespace: "tenants",
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	})
	k8s := k8sclient.New(fakeCS, k8sclient.Config{})
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:59999"})

	// Create a running tenant with a matching pod
	err := reg.CreateTenant(ctx, &registry.TenantRecord{
		TenantID:     "def456",
		Status:       registry.StatusRunning,
		PodName:      "zeroclaw-def456",
		PodIP:        "10.0.0.2",
		Namespace:    "tenants",
		CreatedAt:    time.Now(),
		LastActiveAt: time.Now(),
	})
	require.NoError(t, err)

	rec := New(reg, k8s, rdb, "tenants")
	rec.reconcile(ctx)

	// Verify the tenant is still running
	tenant, err := reg.GetTenant(ctx, "def456")
	require.NoError(t, err)
	assert.Equal(t, registry.StatusRunning, tenant.Status)
	assert.Equal(t, "zeroclaw-def456", tenant.PodName)
}

func TestReconcile_IdleTenantIgnored(t *testing.T) {
	ctx := context.Background()
	reg := registry.NewMock()
	fakeCS := fake.NewSimpleClientset()
	k8s := k8sclient.New(fakeCS, k8sclient.Config{})
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:59999"})

	// Create an idle tenant (should not be touched by reconciler)
	err := reg.CreateTenant(ctx, &registry.TenantRecord{
		TenantID:     "idle1",
		Status:       registry.StatusIdle,
		Namespace:    "tenants",
		CreatedAt:    time.Now(),
		LastActiveAt: time.Now(),
	})
	require.NoError(t, err)

	rec := New(reg, k8s, rdb, "tenants")
	rec.reconcile(ctx)

	// Verify idle tenant is unchanged
	tenant, err := reg.GetTenant(ctx, "idle1")
	require.NoError(t, err)
	assert.Equal(t, registry.StatusIdle, tenant.Status)
}
