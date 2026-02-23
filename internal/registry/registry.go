package registry

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// TenantStatus represents the lifecycle state of a tenant
type TenantStatus string

const (
	StatusProvisioning TenantStatus = "provisioning"
	StatusRunning      TenantStatus = "running"
	StatusIdle         TenantStatus = "idle"
	StatusTerminated   TenantStatus = "terminated"
)

// TenantRecord is the DynamoDB schema for a tenant
type TenantRecord struct {
	TenantID     string       `dynamodbav:"tenant_id"`
	Status       TenantStatus `dynamodbav:"status"`
	PodName      string       `dynamodbav:"pod_name,omitempty"`
	PodIP        string       `dynamodbav:"pod_ip,omitempty"`
	Namespace    string       `dynamodbav:"namespace"`
	S3Prefix     string       `dynamodbav:"s3_prefix"`
	BotToken     string       `dynamodbav:"bot_token,omitempty"`
	CreatedAt    time.Time    `dynamodbav:"created_at"`
	LastActiveAt time.Time    `dynamodbav:"last_active_at"`
	IdleTimeoutS int64        `dynamodbav:"idle_timeout_s"`
}

// Client is the interface for tenant registry operations
type Client interface {
	GetTenant(ctx context.Context, tenantID string) (*TenantRecord, error)
	CreateTenant(ctx context.Context, record *TenantRecord) error
	UpdateStatus(ctx context.Context, tenantID string, status TenantStatus, podName, podIP string) error
	UpdateActivity(ctx context.Context, tenantID string) error
	UpdateBotToken(ctx context.Context, tenantID, botToken string) error
	UpdateIdleTimeout(ctx context.Context, tenantID string, timeoutS int64) error
	ListAll(ctx context.Context) ([]*TenantRecord, error)
	ListByStatus(ctx context.Context, status TenantStatus) ([]*TenantRecord, error)
	ListIdleTenants(ctx context.Context, olderThan time.Duration) ([]*TenantRecord, error)
	DeleteTenant(ctx context.Context, tenantID string) error
}

// DynamoClient implements Client using AWS DynamoDB
type DynamoClient struct {
	db        *dynamodb.Client
	tableName string
}

// New creates a new DynamoDB-backed registry client
func New(db *dynamodb.Client, tableName string) *DynamoClient {
	return &DynamoClient{db: db, tableName: tableName}
}

// GetTenant fetches a tenant record by ID
func (c *DynamoClient) GetTenant(ctx context.Context, tenantID string) (*TenantRecord, error) {
	out, err := c.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(c.tableName),
		Key: map[string]types.AttributeValue{
			"tenant_id": &types.AttributeValueMemberS{Value: tenantID},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("dynamodb GetItem: %w", err)
	}
	if out.Item == nil {
		return nil, nil
	}
	var rec TenantRecord
	if err := attributevalue.UnmarshalMap(out.Item, &rec); err != nil {
		return nil, fmt.Errorf("unmarshal tenant: %w", err)
	}
	return &rec, nil
}

// CreateTenant creates a new tenant record (fails if already exists)
func (c *DynamoClient) CreateTenant(ctx context.Context, record *TenantRecord) error {
	item, err := attributevalue.MarshalMap(record)
	if err != nil {
		return fmt.Errorf("marshal tenant: %w", err)
	}
	_, err = c.db.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           aws.String(c.tableName),
		Item:                item,
		ConditionExpression: aws.String("attribute_not_exists(tenant_id)"),
	})
	if err != nil {
		return fmt.Errorf("dynamodb PutItem: %w", err)
	}
	return nil
}

// UpdateStatus updates tenant status, pod name, and pod IP atomically
func (c *DynamoClient) UpdateStatus(ctx context.Context, tenantID string, status TenantStatus, podName, podIP string) error {
	_, err := c.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(c.tableName),
		Key: map[string]types.AttributeValue{
			"tenant_id": &types.AttributeValueMemberS{Value: tenantID},
		},
		UpdateExpression: aws.String("SET #s = :s, pod_name = :pn, pod_ip = :pi, last_active_at = :la"),
		ExpressionAttributeNames: map[string]string{
			"#s": "status",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":s":  &types.AttributeValueMemberS{Value: string(status)},
			":pn": &types.AttributeValueMemberS{Value: podName},
			":pi": &types.AttributeValueMemberS{Value: podIP},
			":la": &types.AttributeValueMemberS{Value: time.Now().UTC().Format(time.RFC3339)},
		},
	})
	if err != nil {
		return fmt.Errorf("dynamodb UpdateItem: %w", err)
	}
	return nil
}

// UpdateActivity updates the last_active_at timestamp
func (c *DynamoClient) UpdateActivity(ctx context.Context, tenantID string) error {
	_, err := c.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(c.tableName),
		Key: map[string]types.AttributeValue{
			"tenant_id": &types.AttributeValueMemberS{Value: tenantID},
		},
		UpdateExpression: aws.String("SET last_active_at = :la"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":la": &types.AttributeValueMemberS{Value: time.Now().UTC().Format(time.RFC3339)},
		},
	})
	return err
}

// UpdateBotToken updates the bot_token for a tenant
func (c *DynamoClient) UpdateBotToken(ctx context.Context, tenantID, botToken string) error {
	_, err := c.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(c.tableName),
		Key: map[string]types.AttributeValue{
			"tenant_id": &types.AttributeValueMemberS{Value: tenantID},
		},
		UpdateExpression: aws.String("SET bot_token = :bt"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":bt": &types.AttributeValueMemberS{Value: botToken},
		},
		ConditionExpression: aws.String("attribute_exists(tenant_id)"),
	})
	return err
}

// UpdateIdleTimeout updates the idle_timeout_s for a tenant
func (c *DynamoClient) UpdateIdleTimeout(ctx context.Context, tenantID string, timeoutS int64) error {
	_, err := c.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(c.tableName),
		Key: map[string]types.AttributeValue{
			"tenant_id": &types.AttributeValueMemberS{Value: tenantID},
		},
		UpdateExpression: aws.String("SET idle_timeout_s = :t"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":t": &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", timeoutS)},
		},
		ConditionExpression: aws.String("attribute_exists(tenant_id)"),
	})
	return err
}

// ListAll returns all tenant records (excluding internal warm-pool metadata).
func (c *DynamoClient) ListAll(ctx context.Context) ([]*TenantRecord, error) {
	out, err := c.db.Scan(ctx, &dynamodb.ScanInput{
		TableName:        aws.String(c.tableName),
		FilterExpression: aws.String("attribute_exists(#s) AND tenant_id <> :meta"),
		ExpressionAttributeNames: map[string]string{
			"#s": "status",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":meta": &types.AttributeValueMemberS{Value: "warm-pool-meta"},
		},
	})
	if err != nil {
		return nil, err
	}
	var records []*TenantRecord
	for _, item := range out.Items {
		var rec TenantRecord
		if err := attributevalue.UnmarshalMap(item, &rec); err != nil {
			continue
		}
		records = append(records, &rec)
	}
	return records, nil
}

// ListByStatus returns all tenants with the given status
func (c *DynamoClient) ListByStatus(ctx context.Context, status TenantStatus) ([]*TenantRecord, error) {
	out, err := c.db.Scan(ctx, &dynamodb.ScanInput{
		TableName:        aws.String(c.tableName),
		FilterExpression: aws.String("#s = :status"),
		ExpressionAttributeNames: map[string]string{
			"#s": "status",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":status": &types.AttributeValueMemberS{Value: string(status)},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("dynamodb Scan: %w", err)
	}
	var records []*TenantRecord
	for _, item := range out.Items {
		var rec TenantRecord
		if err := attributevalue.UnmarshalMap(item, &rec); err != nil {
			continue
		}
		records = append(records, &rec)
	}
	return records, nil
}

// ListIdleTenants returns running tenants whose last_active_at is older than olderThan
func (c *DynamoClient) ListIdleTenants(ctx context.Context, olderThan time.Duration) ([]*TenantRecord, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
	out, err := c.db.Scan(ctx, &dynamodb.ScanInput{
		TableName:        aws.String(c.tableName),
		FilterExpression: aws.String("#s = :running AND last_active_at < :cutoff"),
		ExpressionAttributeNames: map[string]string{
			"#s": "status",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":running": &types.AttributeValueMemberS{Value: string(StatusRunning)},
			":cutoff":  &types.AttributeValueMemberS{Value: cutoff},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("dynamodb Scan: %w", err)
	}
	var records []*TenantRecord
	for _, item := range out.Items {
		var rec TenantRecord
		if err := attributevalue.UnmarshalMap(item, &rec); err != nil {
			continue
		}
		records = append(records, &rec)
	}
	return records, nil
}

// DeleteTenant removes a tenant record
func (c *DynamoClient) DeleteTenant(ctx context.Context, tenantID string) error {
	_, err := c.db.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(c.tableName),
		Key: map[string]types.AttributeValue{
			"tenant_id": &types.AttributeValueMemberS{Value: tenantID},
		},
	})
	return err
}
