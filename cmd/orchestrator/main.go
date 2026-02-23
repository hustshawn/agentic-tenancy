package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/redis/go-redis/v9"
	"github.com/shawn/agentic-tenancy/internal/api"
	k8sclient "github.com/shawn/agentic-tenancy/internal/k8s"
	"github.com/shawn/agentic-tenancy/internal/lifecycle"
	"github.com/shawn/agentic-tenancy/internal/lock"
	"github.com/shawn/agentic-tenancy/internal/reconciler"
	"github.com/shawn/agentic-tenancy/internal/registry"
	"github.com/shawn/agentic-tenancy/internal/telegram"
	"github.com/shawn/agentic-tenancy/internal/warmpool"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	// Config from env
	dynamoTable := getenv("DYNAMODB_TABLE", "tenant-registry")
	dynamoEndpoint := os.Getenv("DYNAMODB_ENDPOINT")
	redisAddr := getenv("REDIS_ADDR", "localhost:6379")
	namespace := getenv("K8S_NAMESPACE", "tenants")
	s3Bucket := getenv("S3_BUCKET", "zeroclaw-tenant-state")
	warmTarget, _ := strconv.Atoi(getenv("WARM_POOL_TARGET", "10"))
	zeroClawImage := getenv("ZEROCLAW_IMAGE", "zeroclaw:latest")
	kataRuntime := getenv("KATA_RUNTIME_CLASS", "kata-qemu")
	leaderID := getenv("LEADER_ELECTION_ID", "orchestrator-"+os.Getenv("POD_NAME"))
	port := getenv("PORT", "8080")
	localMode := os.Getenv("LOCAL_MODE") == "true" || dynamoEndpoint != ""
	routerPublicURL := os.Getenv("ROUTER_PUBLIC_URL") // e.g. https://zeroclaw-router.example.com

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	// AWS DynamoDB
	var awsOptFns []func(*config.LoadOptions) error
	if localMode {
		// Use static credentials for local DynamoDB
		awsOptFns = append(awsOptFns,
			config.WithRegion("us-east-1"),
			config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
				getenv("AWS_ACCESS_KEY_ID", "test"),
				getenv("AWS_SECRET_ACCESS_KEY", "test"),
				"",
			)),
		)
	}
	awsCfg, err := config.LoadDefaultConfig(ctx, awsOptFns...)
	if err != nil {
		slog.Error("load AWS config", "err", err)
		os.Exit(1)
	}
	var dynamoOpts []func(*dynamodb.Options)
	if dynamoEndpoint != "" {
		dynamoOpts = append(dynamoOpts, func(o *dynamodb.Options) {
			o.BaseEndpoint = &dynamoEndpoint
		})
	}
	db := dynamodb.NewFromConfig(awsCfg, dynamoOpts...)

	// Redis
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})

	// Clients
	reg := registry.New(db, dynamoTable)
	locker := lock.New(rdb)

	var k8s *k8sclient.Client
	var cs kubernetes.Interface

	if localMode {
		// Local mode: use fake k8s or kubeconfig if available
		slog.Info("running in local mode — k8s operations will be skipped or use kubeconfig")
		cs = tryKubeconfig()
		if cs == nil {
			slog.Warn("no kubeconfig found, k8s operations disabled")
		}
	} else {
		k8sCfg, err := rest.InClusterConfig()
		if err != nil {
			slog.Error("k8s in-cluster config", "err", err)
			os.Exit(1)
		}
		cs, err = kubernetes.NewForConfig(k8sCfg)
		if err != nil {
			slog.Error("k8s clientset", "err", err)
			os.Exit(1)
		}
	}

	if cs != nil {
		k8s = k8sclient.New(cs, k8sclient.Config{
			KataRuntimeClass: kataRuntime,
			ZeroClawImage:    zeroClawImage,
			S3Bucket:         s3Bucket,
		})

		// Warm pool manager (only when k8s available)
		if warmTarget > 0 {
			wp := warmpool.New(k8s, namespace, warmTarget)
			go wp.Run(ctx)
		}

		// Lifecycle controller (leader election + idle timeout)
		lc := lifecycle.New(reg, k8s, cs, namespace, leaderID)
		go lc.Run(ctx)

		// Lifecycle reconciler (detects state drift between DynamoDB and k8s)
		rec := reconciler.New(reg, k8s, rdb, namespace)
		go rec.Run(ctx)
	}

	// HTTP API (works with nil k8s in local mode — wake will return error if k8s unavailable)
	var apiK8s *k8sclient.Client
	if k8s != nil {
		apiK8s = k8s
	}
	h := api.New(reg, apiK8s, locker, rdb, telegamClient(routerPublicURL), api.Config{
		Namespace: namespace,
		S3Bucket:  s3Bucket,
	})

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: h.Router(),
	}

	go func() {
		slog.Info("orchestrator listening", "port", port, "local_mode", localMode)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "err", err)
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()
	srv.Shutdown(shutdownCtx)
}

func tryKubeconfig() kubernetes.Interface {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	cfg, err := clientcmd.BuildConfigFromFlags("", rules.GetDefaultFilename())
	if err != nil {
		return nil
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil
	}
	return cs
}

func telegamClient(routerPublicURL string) *telegram.Client {
	if routerPublicURL == "" {
		return nil
	}
	return telegram.New(routerPublicURL)
}
