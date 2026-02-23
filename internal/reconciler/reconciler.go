package reconciler

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
	k8sclient "github.com/shawn/agentic-tenancy/internal/k8s"
	"github.com/shawn/agentic-tenancy/internal/registry"
)

// Reconciler periodically checks for state drift between DynamoDB and k8s.
// If a tenant is marked as "running" in DynamoDB but its pod no longer exists
// in k8s, the reconciler resets the tenant state to "idle" and cleans up
// stale Redis endpoint cache entries.
type Reconciler struct {
	reg       registry.Client
	k8s       *k8sclient.Client
	rdb       *redis.Client
	namespace string
	interval  time.Duration
}

// New creates a new Reconciler.
func New(reg registry.Client, k8s *k8sclient.Client, rdb *redis.Client, namespace string) *Reconciler {
	return &Reconciler{
		reg:       reg,
		k8s:       k8s,
		rdb:       rdb,
		namespace: namespace,
		interval:  60 * time.Second,
	}
}

// Run starts the reconciliation loop. It blocks until ctx is cancelled.
func (r *Reconciler) Run(ctx context.Context) {
	slog.Info("reconciler: starting", "interval", r.interval, "namespace", r.namespace)

	// Run immediately on startup, then on ticker
	r.reconcile(ctx)

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("reconciler: shutting down")
			return
		case <-ticker.C:
			r.reconcile(ctx)
		}
	}
}

// reconcile performs a single reconciliation pass.
func (r *Reconciler) reconcile(ctx context.Context) {
	tenants, err := r.reg.ListByStatus(ctx, registry.StatusRunning)
	if err != nil {
		slog.Error("reconciler: failed to list running tenants", "err", err)
		return
	}

	if len(tenants) == 0 {
		return
	}

	slog.Debug("reconciler: checking running tenants", "count", len(tenants))

	for _, t := range tenants {
		if ctx.Err() != nil {
			return
		}

		podName := fmt.Sprintf("zeroclaw-%s", t.TenantID)
		exists, err := r.k8s.PodExists(ctx, podName, r.namespace)
		if err != nil {
			slog.Error("reconciler: failed to check pod existence",
				"tenant", t.TenantID,
				"pod", podName,
				"err", err,
			)
			continue
		}

		if exists {
			continue
		}

		// Pod is missing — reset state to idle
		slog.Warn("reconciler: pod missing, resetting state",
			"tenant", t.TenantID,
			"pod", podName,
		)

		if err := r.reg.UpdateStatus(ctx, t.TenantID, registry.StatusIdle, "", ""); err != nil {
			slog.Error("reconciler: failed to reset tenant state",
				"tenant", t.TenantID,
				"err", err,
			)
			continue
		}

		// Clean up stale Redis endpoint cache
		cacheKey := fmt.Sprintf("router:endpoint:%s", t.TenantID)
		if err := r.rdb.Del(ctx, cacheKey).Err(); err != nil {
			slog.Error("reconciler: failed to delete Redis cache",
				"tenant", t.TenantID,
				"key", cacheKey,
				"err", err,
			)
		} else {
			slog.Info("reconciler: cleaned up Redis cache",
				"tenant", t.TenantID,
				"key", cacheKey,
			)
		}
	}
}
