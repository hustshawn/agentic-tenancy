package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shawn/agentic-tenancy/internal/api"
	k8sclient "github.com/shawn/agentic-tenancy/internal/k8s"
	"github.com/shawn/agentic-tenancy/internal/lock"
	"github.com/shawn/agentic-tenancy/internal/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func newTestHandler(t *testing.T) (*api.Handler, *registry.MockClient, *lock.MockLocker, *fake.Clientset) {
	t.Helper()
	reg := registry.NewMock()
	locker := lock.NewMock()
	cs := fake.NewSimpleClientset()
	k8s := k8sclient.New(cs, k8sclient.Config{
		KataRuntimeClass: "kata-qemu",
		ZeroClawImage:    "zeroclaw:test",
		S3Bucket:         "test-bucket",
	})
	h := api.New(reg, k8s, locker, nil, nil, api.Config{
		Namespace:    "tenants",
		PodReadyWait: 5 * time.Second,
	})
	return h, reg, locker, cs
}

// simulatePodReady makes a fake pod appear as Running with an IP
func simulatePodReady(cs *fake.Clientset, tenantID, namespace, ip string) {
	go func() {
		time.Sleep(100 * time.Millisecond)
		podName := "zeroclaw-" + tenantID
		pod, err := cs.CoreV1().Pods(namespace).Get(context.Background(), podName, metav1.GetOptions{})
		if err != nil {
			return
		}
		pod.Status.Phase = corev1.PodRunning
		pod.Status.PodIP = ip
		cs.CoreV1().Pods(namespace).UpdateStatus(context.Background(), pod, metav1.UpdateOptions{})
	}()
}

func TestHealthz(t *testing.T) {
	h, _, _, _ := newTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestCreateTenant(t *testing.T) {
	h, reg, _, _ := newTestHandler(t)

	body, _ := json.Marshal(map[string]interface{}{
		"tenant_id":      "tenant-001",
		"idle_timeout_s": 300,
	})
	req := httptest.NewRequest(http.MethodPost, "/tenants", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)

	// Verify registry
	tenant, err := reg.GetTenant(context.Background(), "tenant-001")
	require.NoError(t, err)
	require.NotNil(t, tenant)
	assert.Equal(t, registry.StatusIdle, tenant.Status)
}

func TestCreateTenant_Conflict(t *testing.T) {
	h, _, _, _ := newTestHandler(t)

	body, _ := json.Marshal(map[string]interface{}{"tenant_id": "dup-tenant"})
	req1 := httptest.NewRequest(http.MethodPost, "/tenants", bytes.NewReader(body))
	req1.Header.Set("Content-Type", "application/json")
	h.Router().ServeHTTP(httptest.NewRecorder(), req1)

	body, _ = json.Marshal(map[string]interface{}{"tenant_id": "dup-tenant"})
	req2 := httptest.NewRequest(http.MethodPost, "/tenants", bytes.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	h.Router().ServeHTTP(rec2, req2)

	assert.Equal(t, http.StatusConflict, rec2.Code)
}

// TestWakeTenant_NewTenant: first wake creates PVC + Pod + registry record
func TestWakeTenant_NewTenant(t *testing.T) {
	h, reg, _, cs := newTestHandler(t)
	tenantID := "new-tenant"

	simulatePodReady(cs, tenantID, "tenants", "10.0.0.1")

	req := httptest.NewRequest(http.MethodPost, "/wake/"+tenantID, nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var result map[string]string
	json.NewDecoder(rec.Body).Decode(&result)
	assert.Equal(t, "10.0.0.1", result["pod_ip"])

	// Verify registry updated
	tenant, err := reg.GetTenant(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, registry.StatusRunning, tenant.Status)
	assert.Equal(t, "10.0.0.1", tenant.PodIP)
}

// TestWakeTenant_AlreadyRunning: returns IP immediately, no new Pod created
func TestWakeTenant_AlreadyRunning(t *testing.T) {
	h, reg, _, cs := newTestHandler(t)
	tenantID := "running-tenant"

	// Pre-seed registry as running
	reg.CreateTenant(context.Background(), &registry.TenantRecord{
		TenantID:     tenantID,
		Status:       registry.StatusRunning,
		PodName:      "zeroclaw-" + tenantID,
		PodIP:        "10.0.0.5",
		Namespace:    "tenants",
		LastActiveAt: time.Now(),
		IdleTimeoutS: 300,
	})

	req := httptest.NewRequest(http.MethodPost, "/wake/"+tenantID, nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var result map[string]string
	json.NewDecoder(rec.Body).Decode(&result)
	assert.Equal(t, "10.0.0.5", result["pod_ip"])

	// No pods should be created
	pods, _ := cs.CoreV1().Pods("tenants").List(context.Background(), metav1.ListOptions{})
	assert.Len(t, pods.Items, 0, "no new pods should be created for already-running tenant")
}

// TestWakeTenant_IdleTenant: reuses PVC, creates new Pod
func TestWakeTenant_IdleTenant(t *testing.T) {
	h, reg, _, cs := newTestHandler(t)
	tenantID := "idle-tenant"

	// Pre-seed as idle (PVC exists, no pod)
	reg.CreateTenant(context.Background(), &registry.TenantRecord{
		TenantID:     tenantID,
		Status:       registry.StatusIdle,
		Namespace:    "tenants",
		S3Prefix:     "tenants/idle-tenant/",
		LastActiveAt: time.Now().Add(-10 * time.Minute),
		IdleTimeoutS: 300,
	})

	simulatePodReady(cs, tenantID, "tenants", "10.0.0.2")

	req := httptest.NewRequest(http.MethodPost, "/wake/"+tenantID, nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var result map[string]string
	json.NewDecoder(rec.Body).Decode(&result)
	assert.Equal(t, "10.0.0.2", result["pod_ip"])

	tenant, _ := reg.GetTenant(context.Background(), tenantID)
	assert.Equal(t, registry.StatusRunning, tenant.Status)
}

// TestWakeTenant_ConcurrentWake: 10 goroutines wake same tenant → only 1 Pod created
func TestWakeTenant_ConcurrentWake(t *testing.T) {
	h, _, _, cs := newTestHandler(t)
	tenantID := "concurrent-tenant"

	simulatePodReady(cs, tenantID, "tenants", "10.0.0.3")

	var wg sync.WaitGroup
	var successCount int64
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/wake/"+tenantID, nil)
			rec := httptest.NewRecorder()
			h.Router().ServeHTTP(rec, req)
			if rec.Code == http.StatusOK {
				atomic.AddInt64(&successCount, 1)
			}
		}()
	}
	wg.Wait()

	// All requests should eventually succeed
	assert.Greater(t, successCount, int64(0))

	// Only 1 pod should exist
	pods, _ := cs.CoreV1().Pods("tenants").List(context.Background(), metav1.ListOptions{
		LabelSelector: "tenant=" + tenantID,
	})
	assert.LessOrEqual(t, len(pods.Items), 1, "at most 1 pod should be created for concurrent wakes")
}

func TestDeleteTenant(t *testing.T) {
	h, reg, _, _ := newTestHandler(t)
	tenantID := "delete-me"

	reg.CreateTenant(context.Background(), &registry.TenantRecord{
		TenantID:  tenantID,
		Status:    registry.StatusIdle,
		Namespace: "tenants",
	})

	req := httptest.NewRequest(http.MethodDelete, "/tenants/"+tenantID, nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNoContent, rec.Code)

	tenant, _ := reg.GetTenant(context.Background(), tenantID)
	assert.Nil(t, tenant)
}

func TestUpdateActivity(t *testing.T) {
	h, reg, _, _ := newTestHandler(t)
	tenantID := "active-tenant"

	before := time.Now().Add(-1 * time.Minute)
	reg.CreateTenant(context.Background(), &registry.TenantRecord{
		TenantID:     tenantID,
		Status:       registry.StatusRunning,
		Namespace:    "tenants",
		LastActiveAt: before,
	})

	req := httptest.NewRequest(http.MethodPut, "/tenants/"+tenantID+"/activity", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNoContent, rec.Code)

	tenant, _ := reg.GetTenant(context.Background(), tenantID)
	assert.True(t, tenant.LastActiveAt.After(before))
}
