package integration_test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamotypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/redis/go-redis/v9"
	"github.com/shawn/agentic-tenancy/internal/api"
	k8sclient "github.com/shawn/agentic-tenancy/internal/k8s"
	"github.com/shawn/agentic-tenancy/internal/lifecycle"
	"github.com/shawn/agentic-tenancy/internal/lock"
	"github.com/shawn/agentic-tenancy/internal/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"net/http"
	"encoding/json"
)

const tableName = "tenant-registry-test"

// setupDynamoDB starts a DynamoDB Local container and returns a client + cleanup fn
func setupDynamoDB(ctx context.Context, t *testing.T) (*dynamodb.Client, func()) {
	t.Helper()
	req := testcontainers.ContainerRequest{
		Image:        "amazon/dynamodb-local:latest",
		ExposedPorts: []string{"8000/tcp"},
		Cmd:          []string{"-jar", "DynamoDBLocal.jar", "-inMemory"},
		WaitingFor:   wait.ForListeningPort("8000/tcp"),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err)

	host, _ := c.Host(ctx)
	port, _ := c.MappedPort(ctx, "8000/tcp")
	endpoint := fmt.Sprintf("http://%s:%s", host, port.Port())

	cfg, _ := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	db := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})

	// Create table
	_, err = db.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName:   aws.String(tableName),
		KeySchema:   []dynamotypes.KeySchemaElement{{AttributeName: aws.String("tenant_id"), KeyType: dynamotypes.KeyTypeHash}},
		AttributeDefinitions: []dynamotypes.AttributeDefinition{{AttributeName: aws.String("tenant_id"), AttributeType: dynamotypes.ScalarAttributeTypeS}},
		BillingMode: dynamotypes.BillingModePayPerRequest,
	})
	require.NoError(t, err)

	return db, func() { c.Terminate(ctx) }
}

// setupRedis starts a Redis container and returns a client + cleanup fn
func setupRedis(ctx context.Context, t *testing.T) (*redis.Client, func()) {
	t.Helper()
	req := testcontainers.ContainerRequest{
		Image:        "redis:7-alpine",
		ExposedPorts: []string{"6379/tcp"},
		WaitingFor:   wait.ForListeningPort("6379/tcp"),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err)

	host, _ := c.Host(ctx)
	port, _ := c.MappedPort(ctx, "6379/tcp")

	rdb := redis.NewClient(&redis.Options{
		Addr: fmt.Sprintf("%s:%s", host, port.Port()),
	})
	return rdb, func() { c.Terminate(ctx) }
}

// TestIntegration_WakeIdleWakeCycle tests the full lifecycle:
// wake (new) → idle timeout → wake again (restore)
func TestIntegration_WakeIdleWakeCycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()

	db, cleanDB := setupDynamoDB(ctx, t)
	defer cleanDB()

	rdb, cleanRedis := setupRedis(ctx, t)
	defer cleanRedis()

	cs := fake.NewSimpleClientset()
	reg := registry.New(db, tableName)
	locker := lock.New(rdb)
	k8s := k8sclient.New(cs, k8sclient.Config{
		KataRuntimeClass: "kata-qemu",
		ZeroClawImage:    "zeroclaw:test",
		S3Bucket:         "test-bucket",
	})

	h := api.New(reg, k8s, locker, nil, nil, api.Config{
		Namespace:    "tenants",
		PodReadyWait: 10 * time.Second,
	})
	srv := httptest.NewServer(h.Router())
	defer srv.Close()

	tenantID := "integration-tenant"

	// Helper: make pod appear Ready
	makePodReady := func(ip string) {
		go func() {
			time.Sleep(200 * time.Millisecond)
			podName := "zeroclaw-" + tenantID
			pod, err := cs.CoreV1().Pods("tenants").Get(ctx, podName, metav1.GetOptions{})
			if err != nil {
				return
			}
			pod.Status.Phase = corev1.PodRunning
			pod.Status.PodIP = ip
			cs.CoreV1().Pods("tenants").UpdateStatus(ctx, pod, metav1.UpdateOptions{})
		}()
	}

	// Step 1: Wake new tenant
	makePodReady("10.1.0.1")
	resp, err := http.Post(srv.URL+"/wake/"+tenantID, "application/json", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var wakeResult map[string]string
	json.NewDecoder(resp.Body).Decode(&wakeResult)
	resp.Body.Close()
	assert.Equal(t, "10.1.0.1", wakeResult["pod_ip"])

	// Verify registry state
	rec, err := reg.GetTenant(ctx, tenantID)
	require.NoError(t, err)
	require.NotNil(t, rec)
	assert.Equal(t, registry.StatusRunning, rec.Status)

	// Step 2: Simulate idle timeout — mark last_active_at as old and run controller
	require.NoError(t, reg.UpdateStatus(ctx, tenantID, registry.StatusRunning, "zeroclaw-"+tenantID, "10.1.0.1"))

	// Manually set last_active_at to old value via mock-style workaround:
	// use DynamoDB update directly
	_, err = db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(tableName),
		Key: map[string]dynamotypes.AttributeValue{
			"tenant_id": &dynamotypes.AttributeValueMemberS{Value: tenantID},
		},
		UpdateExpression: aws.String("SET last_active_at = :old"),
		ExpressionAttributeValues: map[string]dynamotypes.AttributeValue{
			":old": &dynamotypes.AttributeValueMemberS{Value: time.Now().Add(-10 * time.Minute).Format(time.RFC3339)},
		},
	})
	require.NoError(t, err)

	// Run idle check
	ctrl := lifecycle.NewForTest(reg, k8s)
	ctrl.CheckIdleTenants(ctx)

	// Verify pod deleted and status = idle
	rec, err = reg.GetTenant(ctx, tenantID)
	require.NoError(t, err)
	assert.Equal(t, registry.StatusIdle, rec.Status)
	assert.Empty(t, rec.PodIP)

	_, err = cs.CoreV1().Pods("tenants").Get(ctx, "zeroclaw-"+tenantID, metav1.GetOptions{})
	assert.Error(t, err, "pod should have been deleted")

	// Step 3: Wake again (from idle) — should restore and return new IP
	makePodReady("10.1.0.2")
	resp2, err := http.Post(srv.URL+"/wake/"+tenantID, "application/json", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp2.StatusCode)

	var wakeResult2 map[string]string
	json.NewDecoder(resp2.Body).Decode(&wakeResult2)
	resp2.Body.Close()
	assert.Equal(t, "10.1.0.2", wakeResult2["pod_ip"])

	rec2, err := reg.GetTenant(ctx, tenantID)
	require.NoError(t, err)
	assert.Equal(t, registry.StatusRunning, rec2.Status)
	assert.Equal(t, "10.1.0.2", rec2.PodIP)
}
