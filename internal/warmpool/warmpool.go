package warmpool

import (
	"context"
	"log/slog"
	"time"

	k8sclient "github.com/shawn/agentic-tenancy/internal/k8s"
)

// Manager maintains a warm-pool Deployment of low-priority ZeroClaw pods.
// The Deployment keeps `target` replicas running at all times.
// When a warm pod is claimed by a tenant (label changed to warm=consuming),
// the Deployment automatically creates a replacement pod.
type Manager struct {
	k8s       *k8sclient.Client
	namespace string
	target    int32
	interval  time.Duration
}

func New(k8s *k8sclient.Client, namespace string, target int) *Manager {
	return &Manager{
		k8s:       k8s,
		namespace: namespace,
		target:    int32(target),
		interval:  30 * time.Second,
	}
}

// Run ensures the warm-pool Deployment exists and stays at the desired replica count.
func (m *Manager) Run(ctx context.Context) {
	slog.Info("warm pool: starting", "target", m.target, "namespace", m.namespace)

	m.reconcile(ctx)

	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("warm pool: shutting down")
			return
		case <-ticker.C:
			m.reconcile(ctx)
		}
	}
}

func (m *Manager) reconcile(ctx context.Context) {
	if err := m.k8s.EnsureWarmPoolDeployment(ctx, m.namespace, m.target); err != nil {
		slog.Error("warm pool: ensure deployment failed", "err", err)
	}
}
