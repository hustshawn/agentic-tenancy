package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/redis/go-redis/v9"
)

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

const (
	endpointCacheTTL = 5 * time.Minute
	cacheKeyPrefix   = "router:endpoint:"
	podReadyWait     = 5 * time.Minute // Karpenter cold-start (new metal node) can take 4+ minutes
)

type Router struct {
	rdb              *redis.Client
	orchestratorAddr string
	publicBaseURL    string // e.g. https://<YOUR_ROUTER_DOMAIN>
	httpClient       *http.Client
}

// ── Telegram webhook receiver ────────────────────────────────────

// webhookHandler receives Telegram updates for a specific tenant.
// Path: POST /tg/{tenantID}
func (rt *Router) webhookHandler(w http.ResponseWriter, r *http.Request) {
	tenantID := chi.URLParam(r, "tenantID")
	if tenantID == "" {
		http.Error(w, "tenantID required", http.StatusBadRequest)
		return
	}

	// Read body — we need it twice (forward to pod later)
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	// Ack Telegram immediately (must respond within 5s)
	w.WriteHeader(http.StatusOK)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	// Handle message async — Telegram doesn't wait for us
	go rt.handleTelegramUpdate(tenantID, body)
}

func (rt *Router) handleTelegramUpdate(tenantID string, body []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), podReadyWait+30*time.Second)
	defer cancel()

	// Check if pod is already running (Redis cache)
	podIP, err := rt.getCachedPodIP(ctx, tenantID)
	if err == nil && podIP != "" {
		// Pod is up — forward directly
		rt.forwardToPod(ctx, podIP, tenantID, body)
		rt.updateActivity(tenantID)
		return
	}

	// Pod not running — send "starting up" message to user via Telegram
	chatID := extractChatID(body)
	botToken := rt.getBotToken(ctx, tenantID)
	if chatID != 0 && botToken != "" {
		rt.sendTelegramMessage(botToken, chatID, "⏳ Starting up, please wait a moment...")
	}

	// Wake the pod
	podIP, err = rt.wakePod(ctx, tenantID)
	if err != nil {
		slog.Error("wake failed", "tenant", tenantID, "err", err)
		if chatID != 0 && botToken != "" {
			rt.sendTelegramMessage(botToken, chatID, "❌ Failed to start. Please try again.")
		}
		return
	}

	// Cache the new pod IP
	rt.rdb.Set(ctx, cacheKeyPrefix+tenantID, podIP, endpointCacheTTL)

	// Forward the original message
	rt.forwardToPod(ctx, podIP, tenantID, body)
	rt.updateActivity(tenantID)
}

func (rt *Router) forwardToPod(ctx context.Context, podIP, tenantID string, body []byte) {
	// Parse Telegram Update and extract message text
	text := extractMessageText(body)
	if text == "" {
		slog.Info("no text in update, skipping forward", "tenant", tenantID)
		return
	}

	// ZeroClaw /webhook expects {"message": "..."}
	payload, _ := json.Marshal(map[string]string{"message": text})

	url := fmt.Sprintf("http://%s:3000/webhook", podIP)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		slog.Error("build forward request", "tenant", tenantID, "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := rt.httpClient.Do(req)
	if err != nil {
		slog.Warn("forward to pod failed, invalidating cache", "tenant", tenantID, "err", err)
		rt.rdb.Del(ctx, cacheKeyPrefix+tenantID)
		return
	}
	defer resp.Body.Close()

	// Read response from ZeroClaw and send back to Telegram
	var result struct {
		Response string `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err == nil && result.Response != "" {
		chatID := extractChatID(body)
		botToken := rt.getBotToken(ctx, tenantID)
		if chatID != 0 && botToken != "" {
			rt.sendTelegramMessage(botToken, chatID, result.Response)
		}
	}
	slog.Info("forwarded to pod", "tenant", tenantID, "pod_ip", podIP, "status", resp.StatusCode)
}

func (rt *Router) getCachedPodIP(ctx context.Context, tenantID string) (string, error) {
	return rt.rdb.Get(ctx, cacheKeyPrefix+tenantID).Result()
}

func (rt *Router) wakePod(ctx context.Context, tenantID string) (string, error) {
	resp, err := rt.httpClient.Post(
		fmt.Sprintf("%s/wake/%s", rt.orchestratorAddr, tenantID),
		"application/json", nil,
	)
	if err != nil {
		return "", fmt.Errorf("orchestrator wake: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("wake status %d: %s", resp.StatusCode, body)
	}
	var result struct {
		PodIP string `json:"pod_ip"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode wake response: %w", err)
	}
	return result.PodIP, nil
}

func (rt *Router) getBotToken(ctx context.Context, tenantID string) string {
	resp, err := rt.httpClient.Get(fmt.Sprintf("%s/tenants/%s/bot_token", rt.orchestratorAddr, tenantID))
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var rec struct {
		BotToken string `json:"BotToken"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rec); err != nil {
		return ""
	}
	return rec.BotToken
}

func (rt *Router) sendTelegramMessage(botToken string, chatID int64, text string) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", botToken)
	payload, _ := json.Marshal(map[string]any{
		"chat_id": chatID,
		"text":    text,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rt.httpClient.Do(req)
}

func (rt *Router) updateActivity(tenantID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPut,
		fmt.Sprintf("%s/tenants/%s/activity", rt.orchestratorAddr, tenantID), nil)
	rt.httpClient.Do(req)
}

// extractChatID extracts chat.id from a Telegram Update JSON body.
func extractChatID(body []byte) int64 {
	var update struct {
		Message *struct {
			Chat struct {
				ID int64 `json:"id"`
			} `json:"chat"`
		} `json:"message"`
		CallbackQuery *struct {
			Message *struct {
				Chat struct {
					ID int64 `json:"id"`
				} `json:"chat"`
			} `json:"message"`
		} `json:"callback_query"`
	}
	if err := json.Unmarshal(body, &update); err != nil {
		return 0
	}
	if update.Message != nil {
		return update.Message.Chat.ID
	}
	if update.CallbackQuery != nil && update.CallbackQuery.Message != nil {
		return update.CallbackQuery.Message.Chat.ID
	}
	return 0
}

// extractMessageText extracts the text from a Telegram Update message.
func extractMessageText(body []byte) string {
	var update struct {
		Message *struct {
			Text    string `json:"text"`
			Caption string `json:"caption"`
		} `json:"message"`
	}
	if err := json.Unmarshal(body, &update); err != nil {
		return ""
	}
	if update.Message != nil {
		if update.Message.Text != "" {
			return update.Message.Text
		}
		return update.Message.Caption
	}
	return ""
}

// ── Webhook registration ─────────────────────────────────────────

// RegisterWebhook tells Telegram to push updates to our router for a tenant.
func (rt *Router) RegisterWebhook(botToken, tenantID string) error {
	webhookURL := fmt.Sprintf("%s/tg/%s", rt.publicBaseURL, tenantID)
	payload, _ := json.Marshal(map[string]any{
		"url":             webhookURL,
		"drop_pending_updates": true,
	})
	url := fmt.Sprintf("https://api.telegram.org/bot%s/setWebhook", botToken)
	resp, err := rt.httpClient.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	if ok, _ := result["ok"].(bool); !ok {
		return fmt.Errorf("setWebhook failed: %v", result)
	}
	slog.Info("webhook registered", "tenant", tenantID, "url", webhookURL)
	return nil
}

// registerWebhookHandler is a management endpoint: POST /admin/webhook/{tenantID}
func (rt *Router) registerWebhookHandler(w http.ResponseWriter, r *http.Request) {
	tenantID := chi.URLParam(r, "tenantID")
	botToken := rt.getBotToken(r.Context(), tenantID)
	if botToken == "" {
		http.Error(w, "tenant not found or no bot_token", http.StatusNotFound)
		return
	}
	if err := rt.RegisterWebhook(botToken, tenantID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"ok":true,"webhook_url":"%s/tg/%s"}`, rt.publicBaseURL, tenantID)
}

// ── Main ─────────────────────────────────────────────────────────

func main() {
	redisAddr := getenv("REDIS_ADDR", "localhost:6379")
	orchestratorAddr := getenv("ORCHESTRATOR_ADDR", "http://localhost:8080")
	publicBaseURL := getenv("PUBLIC_BASE_URL", "https://<YOUR_ROUTER_DOMAIN>")
	port := getenv("PORT", "9090")

	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})

	rt := &Router{
		rdb:              rdb,
		orchestratorAddr: orchestratorAddr,
		publicBaseURL:    publicBaseURL,
		httpClient:       &http.Client{Timeout: 320 * time.Second}, // must exceed podReadyWait (5m) + LLM response time
	}

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	// Telegram webhook receiver — one URL per tenant
	r.Post("/tg/{tenantID}", rt.webhookHandler)

	// Admin: register webhook for a tenant
	r.Post("/admin/webhook/{tenantID}", rt.registerWebhookHandler)

	srv := &http.Server{Addr: ":" + port, Handler: r}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	go func() {
		slog.Info("router listening", "port", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("router error", "err", err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	srv.Shutdown(shutdownCtx)
}
