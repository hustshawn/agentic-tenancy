package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/redis/go-redis/v9"
	k8sclient "github.com/shawn/agentic-tenancy/internal/k8s"
	"github.com/shawn/agentic-tenancy/internal/lock"
	"github.com/shawn/agentic-tenancy/internal/registry"
	"github.com/shawn/agentic-tenancy/internal/telegram"
)

const routerEndpointCachePrefix = "router:endpoint:"

// Config holds orchestrator API configuration
type Config struct {
	Namespace    string
	S3Bucket     string
	WakeLockTTL  time.Duration
	PodReadyWait time.Duration
}

// Handler is the main orchestrator HTTP handler
type Handler struct {
	reg  registry.Client
	k8s  *k8sclient.Client
	lock lock.Locker
	rdb  *redis.Client
	tg   *telegram.Client // nil if ROUTER_PUBLIC_URL not set
	cfg  Config
}

func New(reg registry.Client, k8s *k8sclient.Client, locker lock.Locker, rdb *redis.Client, tg *telegram.Client, cfg Config) *Handler {
	if cfg.WakeLockTTL == 0 {
		cfg.WakeLockTTL = 240 * time.Second
	}
	if cfg.PodReadyWait == 0 {
		cfg.PodReadyWait = 210 * time.Second
	}
	return &Handler{reg: reg, k8s: k8s, lock: locker, rdb: rdb, tg: tg, cfg: cfg}
}

// Router returns the chi router with all routes registered
func (h *Handler) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)

	r.Get("/healthz", h.Healthz)
	r.Post("/tenants", h.CreateTenant)
	r.Get("/tenants", h.ListTenants)
	r.Get("/tenants/{tenantID}", h.GetTenant)
	r.Get("/tenants/{tenantID}/bot_token", h.GetBotToken)
	r.Patch("/tenants/{tenantID}", h.UpdateTenant)
	r.Delete("/tenants/{tenantID}", h.DeleteTenant)
	r.Put("/tenants/{tenantID}/activity", h.UpdateActivity)
	r.Post("/wake/{tenantID}", h.Wake)

	return r
}

// Healthz returns 200 OK
func (h *Handler) Healthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

// CreateTenant creates a new tenant record
func (h *Handler) CreateTenant(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TenantID     string `json:"tenant_id"`
		IdleTimeoutS int64  `json:"idle_timeout_s"`
		BotToken     string `json:"bot_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if req.TenantID == "" {
		http.Error(w, "tenant_id required", http.StatusBadRequest)
		return
	}
	if req.IdleTimeoutS == 0 {
		req.IdleTimeoutS = 300
	}
	rec := &registry.TenantRecord{
		TenantID:     req.TenantID,
		Status:       registry.StatusIdle,
		Namespace:    h.cfg.Namespace,
		S3Prefix:     fmt.Sprintf("tenants/%s/", req.TenantID),
		BotToken:     req.BotToken,
		CreatedAt:    time.Now().UTC(),
		LastActiveAt: time.Now().UTC(),
		IdleTimeoutS: req.IdleTimeoutS,
	}
	if err := h.reg.CreateTenant(r.Context(), rec); err != nil {
		slog.Error("create tenant failed", "tenant", req.TenantID, "err", err)
		http.Error(w, "conflict", http.StatusConflict)
		return
	}
	// Auto-register Telegram webhook if router URL is configured and bot token provided
	if h.tg != nil && req.BotToken != "" {
		if err := h.tg.RegisterWebhook(r.Context(), req.BotToken, req.TenantID); err != nil {
			slog.Warn("webhook registration failed (tenant created, fix manually)", "tenant", req.TenantID, "err", err)
		} else {
			slog.Info("webhook registered", "tenant", req.TenantID)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	rec.BotToken = "" // redact in response
	json.NewEncoder(w).Encode(rec)
}

// ListTenants returns all tenant records (BotToken redacted)
func (h *Handler) ListTenants(w http.ResponseWriter, r *http.Request) {
	records, err := h.reg.ListAll(r.Context())
	if err != nil {
		slog.Error("list tenants failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if records == nil {
		records = []*registry.TenantRecord{}
	}
	// Redact BotToken from all records
	for _, rec := range records {
		rec.BotToken = ""
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(records)
}

// GetTenant returns a tenant record (BotToken redacted)
func (h *Handler) GetTenant(w http.ResponseWriter, r *http.Request) {
	tenantID := chi.URLParam(r, "tenantID")
	rec, err := h.reg.GetTenant(r.Context(), tenantID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if rec == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	rec.BotToken = ""
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(rec)
}

// GetBotToken returns only the bot_token for a tenant (internal use by Router)
func (h *Handler) GetBotToken(w http.ResponseWriter, r *http.Request) {
	tenantID := chi.URLParam(r, "tenantID")
	rec, err := h.reg.GetTenant(r.Context(), tenantID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if rec == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"BotToken": rec.BotToken})
}

// UpdateTenant updates mutable tenant fields (currently: bot_token, idle_timeout_s)
func (h *Handler) UpdateTenant(w http.ResponseWriter, r *http.Request) {
	tenantID := chi.URLParam(r, "tenantID")
	var req struct {
		BotToken     *string `json:"bot_token"`
		IdleTimeoutS *int64  `json:"idle_timeout_s"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if req.BotToken != nil {
		if err := h.reg.UpdateBotToken(r.Context(), tenantID, *req.BotToken); err != nil {
			slog.Error("update bot_token failed", "tenant", tenantID, "err", err)
			http.Error(w, "not found or internal error", http.StatusNotFound)
			return
		}
		// Re-register webhook with new token
		if h.tg != nil && *req.BotToken != "" {
			if err := h.tg.RegisterWebhook(r.Context(), *req.BotToken, tenantID); err != nil {
				slog.Warn("webhook re-registration failed (token updated, fix manually)", "tenant", tenantID, "err", err)
			} else {
				slog.Info("webhook re-registered", "tenant", tenantID)
			}
		}
	}
	if req.IdleTimeoutS != nil {
		if err := h.reg.UpdateIdleTimeout(r.Context(), tenantID, *req.IdleTimeoutS); err != nil {
			slog.Error("update idle_timeout_s failed", "tenant", tenantID, "err", err)
			http.Error(w, "not found or internal error", http.StatusNotFound)
			return
		}
	}
	rec, err := h.reg.GetTenant(r.Context(), tenantID)
	if err != nil || rec == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	rec.BotToken = "" // redact in response
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(rec)
}

// DeleteTenant removes a tenant and all its resources
func (h *Handler) DeleteTenant(w http.ResponseWriter, r *http.Request) {
	tenantID := chi.URLParam(r, "tenantID")
	rec, err := h.reg.GetTenant(r.Context(), tenantID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if rec == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if rec.PodName != "" && h.k8s != nil {
		if err := h.k8s.DeletePod(r.Context(), rec.PodName, rec.Namespace, 30); err != nil {
			slog.Error("delete pod failed", "tenant", tenantID, "err", err)
		}
	}
	if h.k8s != nil {
		if err := h.k8s.DeletePVC(r.Context(), tenantID, rec.Namespace); err != nil {
			slog.Error("delete PVC failed", "tenant", tenantID, "err", err)
		}
	}
	if err := h.reg.DeleteTenant(r.Context(), tenantID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Clear Redis endpoint cache so Router doesn't serve stale IP
	if h.rdb != nil {
		cacheKey := routerEndpointCachePrefix + tenantID
		if err := h.rdb.Del(r.Context(), cacheKey).Err(); err != nil {
			slog.Warn("delete tenant: failed to clear Redis cache", "tenant", tenantID, "err", err)
		}
	}
	// Remove Telegram webhook
	if h.tg != nil && rec.BotToken != "" {
		if err := h.tg.DeleteWebhook(r.Context(), rec.BotToken); err != nil {
			slog.Warn("delete tenant: failed to remove webhook", "tenant", tenantID, "err", err)
		} else {
			slog.Info("webhook deleted", "tenant", tenantID)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// UpdateActivity updates last_active_at for a tenant
func (h *Handler) UpdateActivity(w http.ResponseWriter, r *http.Request) {
	tenantID := chi.URLParam(r, "tenantID")
	if err := h.reg.UpdateActivity(r.Context(), tenantID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Wake ensures a tenant pod is running and returns its IP
func (h *Handler) Wake(w http.ResponseWriter, r *http.Request) {
	tenantID := chi.URLParam(r, "tenantID")
	ctx := r.Context()

	podIP, err := h.wakeOrGet(ctx, tenantID)
	if err != nil {
		slog.Error("wake failed", "tenant", tenantID, "err", err)
		http.Error(w, "failed to wake tenant", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"pod_ip": podIP})
}

// wakeOrGet returns the pod IP, starting the pod if needed
func (h *Handler) wakeOrGet(ctx context.Context, tenantID string) (string, error) {
	if h.k8s == nil {
		return "", fmt.Errorf("k8s not available in local mode")
	}
	// Fast path: already running
	rec, err := h.reg.GetTenant(ctx, tenantID)
	if err != nil {
		return "", err
	}
	if rec != nil && rec.Status == registry.StatusRunning && rec.PodIP != "" {
		return rec.PodIP, nil
	}

	// Slow path: try to acquire wake lock
	acquired, err := h.lock.AcquireWakeLock(ctx, tenantID, h.cfg.WakeLockTTL)
	if err != nil {
		return "", fmt.Errorf("acquire lock: %w", err)
	}

	if !acquired {
		// Another replica is waking this tenant — poll until running
		return h.pollUntilRunning(ctx, tenantID)
	}
	defer h.lock.ReleaseWakeLock(ctx, tenantID)

	// We have the lock — ensure PVC exists, create pod, wait ready
	if rec == nil {
		// Auto-create tenant if not exists
		rec = &registry.TenantRecord{
			TenantID:     tenantID,
			Status:       registry.StatusProvisioning,
			Namespace:    h.cfg.Namespace,
			S3Prefix:     fmt.Sprintf("tenants/%s/", tenantID),
			CreatedAt:    time.Now().UTC(),
			LastActiveAt: time.Now().UTC(),
			IdleTimeoutS: 300,
		}
		_ = h.reg.CreateTenant(ctx, rec)
	}

	ns := h.cfg.Namespace
	if rec.Namespace != "" {
		ns = rec.Namespace
	}

	// Ensure PVC
	if err := h.k8s.CreatePVC(ctx, tenantID, ns); err != nil {
		return "", fmt.Errorf("create PVC: %w", err)
	}

	// Check for a warm pod — if one is available, delete it and pin the
	// tenant pod to the same node to skip Karpenter provisioning.
	nodeName := ""
	if warmPod, err := h.k8s.GetWarmPod(ctx, ns); err == nil && warmPod != nil {
		nodeName = warmPod.Spec.NodeName
		slog.Info("warm pool hit: reusing node", "tenant", tenantID, "node", nodeName, "warm_pod", warmPod.Name)
		// Delete the warm pod to free resources before creating tenant pod
		_ = h.k8s.DeletePod(ctx, warmPod.Name, ns, 0)
	} else {
		slog.Info("warm pool miss: cold start", "tenant", tenantID)
	}

	// Create pod (pinned to warm node if available)
	pod, err := h.k8s.CreateTenantPod(ctx, tenantID, ns, k8sclient.PVCName(tenantID), rec.BotToken, nodeName)
	if err != nil {
		return "", fmt.Errorf("create pod: %w", err)
	}

	// Wait ready
	podIP, err := h.k8s.WaitPodReady(ctx, tenantID, ns, h.cfg.PodReadyWait)
	if err != nil {
		return "", fmt.Errorf("wait pod ready: %w", err)
	}

	// Update registry
	if err := h.reg.UpdateStatus(ctx, tenantID, registry.StatusRunning, pod.Name, podIP); err != nil {
		return "", fmt.Errorf("update status: %w", err)
	}

	return podIP, nil
}

// pollUntilRunning waits for another replica to finish waking the tenant
func (h *Handler) pollUntilRunning(ctx context.Context, tenantID string) (string, error) {
	deadline := time.Now().Add(h.cfg.WakeLockTTL)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(2 * time.Second):
		}
		rec, err := h.reg.GetTenant(ctx, tenantID)
		if err != nil {
			continue
		}
		if rec != nil && rec.Status == registry.StatusRunning && rec.PodIP != "" {
			return rec.PodIP, nil
		}
	}
	return "", fmt.Errorf("timeout waiting for tenant %s to become running", tenantID)
}
