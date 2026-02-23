package warmpool

import (
	"context"
	"testing"

	k8sclient "github.com/shawn/agentic-tenancy/internal/k8s"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// TestWarmPool_EnsureDeployment verifies the warm-pool Deployment is created
func TestWarmPool_EnsureDeployment(t *testing.T) {
	cs := fake.NewSimpleClientset()
	k8s := k8sclient.New(cs, k8sclient.Config{
		KataRuntimeClass: "kata-qemu",
		ZeroClawImage:    "zeroclaw:test",
	})

	wp := New(k8s, "tenants", 2)
	wp.reconcile(context.Background())

	deploy, err := cs.AppsV1().Deployments("tenants").Get(context.Background(), "warm-pool", metav1.GetOptions{})
	assert.NoError(t, err)
	assert.Equal(t, int32(2), *deploy.Spec.Replicas)
}

// TestWarmPool_IdempotentReconcile verifies reconcile is idempotent
func TestWarmPool_IdempotentReconcile(t *testing.T) {
	cs := fake.NewSimpleClientset()
	k8s := k8sclient.New(cs, k8sclient.Config{
		KataRuntimeClass: "kata-qemu",
		ZeroClawImage:    "zeroclaw:test",
	})

	wp := New(k8s, "tenants", 1)
	wp.reconcile(context.Background())
	wp.reconcile(context.Background()) // second call should not fail

	deploy, err := cs.AppsV1().Deployments("tenants").Get(context.Background(), "warm-pool", metav1.GetOptions{})
	assert.NoError(t, err)
	assert.Equal(t, int32(1), *deploy.Spec.Replicas)
}
