package k8s

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
)

const (
	defaultKataRuntime  = "kata-qemu"
	defaultPriorityNorm = "tenant-normal"
	defaultPriorityLow  = "tenant-low"
)

// Config holds k8s client configuration
type Config struct {
	KataRuntimeClass string
	ZeroClawImage    string
	S3Bucket         string
}

// Client wraps kubernetes.Interface with tenant-specific helpers
type Client struct {
	cs  kubernetes.Interface
	cfg Config
}

func New(cs kubernetes.Interface, cfg Config) *Client {
	if cfg.KataRuntimeClass == "" {
		cfg.KataRuntimeClass = defaultKataRuntime
	}
	return &Client{cs: cs, cfg: cfg}
}

// CreateTenantPod creates the ZeroClaw pod for a tenant.
// If nodeName is non-empty, the pod is pinned to that node (used when
// assigning from a warm pool pod to skip Karpenter provisioning).
func (c *Client) CreateTenantPod(ctx context.Context, tenantID, namespace, pvcName, botToken, nodeName string) (*corev1.Pod, error) {
	podName := podName(tenantID)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: namespace,
			Labels: map[string]string{
				"app":    "zeroclaw",
				"tenant": tenantID,
			},
		},
		Spec: corev1.PodSpec{
			RuntimeClassName:   strPtr(c.cfg.KataRuntimeClass),
			PriorityClassName:  defaultPriorityNorm,
			ServiceAccountName: "zeroclaw-tenant",
			NodeName:           nodeName, // pin to warm node if provided
			NodeSelector: map[string]string{
				"katacontainers.io/kata-runtime": "true",
			},
			Tolerations: []corev1.Toleration{
				{
					Key:      "kata-runtime",
					Value:    "true",
					Operator: corev1.TolerationOpEqual,
					Effect:   corev1.TaintEffectNoSchedule,
				},
			},
			Containers: []corev1.Container{
				{
					Name:  "zeroclaw",
					Image: c.cfg.ZeroClawImage,
					Env: []corev1.EnvVar{
						{Name: "TENANT_ID", Value: tenantID},
						{Name: "TELEGRAM_BOT_TOKEN", Value: botToken},
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100m"),
							corev1.ResourceMemory: resource.MustParse("384Mi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("500m"),
							corev1.ResourceMemory: resource.MustParse("512Mi"),
						},
					},
					VolumeMounts: []corev1.VolumeMount{
						{Name: "local-state", MountPath: "/zeroclaw-data"},
						{Name: "s3-state", MountPath: "/s3-state"},
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name:         "local-state",
					VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
				},
				{
					Name: "s3-state",
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
							ClaimName: pvcName,
						},
					},
				},
			},
			TerminationGracePeriodSeconds: int64Ptr(30),
		},
	}

	created, err := c.cs.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{})
	if errors.IsAlreadyExists(err) {
		return c.cs.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	}
	return created, err
}

// WaitPodReady polls until the pod is Running and has a PodIP, returns the IP
func (c *Client) WaitPodReady(ctx context.Context, tenantID, namespace string, timeout time.Duration) (string, error) {
	name := podName(tenantID)
	var podIP string
	err := wait.PollUntilContextTimeout(ctx, 2*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		pod, err := c.cs.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}
		if pod.Status.Phase == corev1.PodRunning && pod.Status.PodIP != "" {
			podIP = pod.Status.PodIP
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		return "", fmt.Errorf("pod %s not ready after %s: %w", name, timeout, err)
	}
	return podIP, nil
}

// DeletePod deletes a pod with the given grace period
func (c *Client) DeletePod(ctx context.Context, podName, namespace string, gracePeriod int64) error {
	err := c.cs.CoreV1().Pods(namespace).Delete(ctx, podName, metav1.DeleteOptions{
		GracePeriodSeconds: &gracePeriod,
	})
	if errors.IsNotFound(err) {
		return nil
	}
	return err
}

// CreatePVC creates an S3 CSI PVC for a tenant (idempotent)
func (c *Client) CreatePVC(ctx context.Context, tenantID, namespace string) error {
	pvcName := PVCName(tenantID)
	pvName := pvName(tenantID)
	storageClass := "s3-tenant-state"

	// Create PV first
	pv := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: pvName},
		Spec: corev1.PersistentVolumeSpec{
			Capacity: corev1.ResourceList{
				corev1.ResourceStorage: resource.MustParse("1Gi"),
			},
			AccessModes:                   []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany},
			StorageClassName:              storageClass,
			PersistentVolumeReclaimPolicy: corev1.PersistentVolumeReclaimRetain,
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				CSI: &corev1.CSIPersistentVolumeSource{
					Driver:       "s3.csi.aws.com",
					VolumeHandle: "tenant-" + tenantID,
					VolumeAttributes: map[string]string{
						"bucketName": c.cfg.S3Bucket,
						"subPath":    "tenants/" + tenantID,
					},
				},
			},
		},
	}
	_, err := c.cs.CoreV1().PersistentVolumes().Create(ctx, pv, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("create PV: %w", err)
	}

	// Create PVC
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName,
			Namespace: namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany},
			StorageClassName: &storageClass,
			VolumeName:       pvName,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse("1Gi"),
				},
			},
		},
	}
	_, err = c.cs.CoreV1().PersistentVolumeClaims(namespace).Create(ctx, pvc, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("create PVC: %w", err)
	}
	return nil
}

// DeletePVC deletes a tenant's PVC and PV
func (c *Client) DeletePVC(ctx context.Context, tenantID, namespace string) error {
	err := c.cs.CoreV1().PersistentVolumeClaims(namespace).Delete(ctx, PVCName(tenantID), metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return err
	}
	err = c.cs.CoreV1().PersistentVolumes().Delete(ctx, pvName(tenantID), metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return err
	}
	return nil
}

// EnsureWarmPoolDeployment creates or updates the warm-pool Deployment to the desired replica count.
// Warm pods run the real ZeroClaw image at low priority with label warm=true.
// When a warm pod is consumed by a tenant, call ClaimWarmPod to detach it from the Deployment.
func (c *Client) EnsureWarmPoolDeployment(ctx context.Context, namespace string, replicas int32) error {
	const name = "warm-pool"
	labels := map[string]string{
		"app":  "warm-pool",
		"warm": "true",
	}

	podSpec := corev1.PodSpec{
		RuntimeClassName:   strPtr(c.cfg.KataRuntimeClass),
		PriorityClassName:  defaultPriorityLow,
		ServiceAccountName: "zeroclaw-tenant",
		NodeSelector: map[string]string{
			"katacontainers.io/kata-runtime": "true",
		},
		Tolerations: []corev1.Toleration{
			{
				Key:      "kata-runtime",
				Value:    "true",
				Operator: corev1.TolerationOpEqual,
				Effect:   corev1.TaintEffectNoSchedule,
			},
		},
		Containers: []corev1.Container{
			{
				Name:  "zeroclaw",
				Image: c.cfg.ZeroClawImage,
				Env: []corev1.EnvVar{
					{Name: "TELEGRAM_BOT_TOKEN", Value: ""},
				},
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100m"),
						corev1.ResourceMemory: resource.MustParse("384Mi"),
					},
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("500m"),
						corev1.ResourceMemory: resource.MustParse("512Mi"),
					},
				},
			},
		},
		TerminationGracePeriodSeconds: int64Ptr(10),
	}

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app":  "warm-pool",
					"warm": "true",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec:       podSpec,
			},
		},
	}

	existing, err := c.cs.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		_, err = c.cs.AppsV1().Deployments(namespace).Create(ctx, deploy, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}

	// Update replicas and image if changed
	existing.Spec.Replicas = &replicas
	existing.Spec.Template.Spec.Containers[0].Image = c.cfg.ZeroClawImage
	_, err = c.cs.AppsV1().Deployments(namespace).Update(ctx, existing, metav1.UpdateOptions{})
	return err
}

// ScaleWarmPool sets the warm-pool Deployment replica count.
func (c *Client) ScaleWarmPool(ctx context.Context, namespace string, replicas int32) error {
	existing, err := c.cs.AppsV1().Deployments(namespace).Get(ctx, "warm-pool", metav1.GetOptions{})
	if err != nil {
		return err
	}
	existing.Spec.Replicas = &replicas
	_, err = c.cs.AppsV1().Deployments(namespace).Update(ctx, existing, metav1.UpdateOptions{})
	return err
}

// GetWarmPod finds a running warm pod and atomically detaches it from the
// Deployment by removing the "warm=true" label (so the Deployment no longer
// manages it). Returns nil if no warm pod is available.
func (c *Client) GetWarmPod(ctx context.Context, namespace string) (*corev1.Pod, error) {
	list, err := c.cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=warm-pool,warm=true",
	})
	if err != nil {
		return nil, err
	}
	for i := range list.Items {
		p := &list.Items[i]
		if p.Status.Phase != corev1.PodRunning ||
			p.Status.PodIP == "" ||
			p.DeletionTimestamp != nil {
			continue
		}
		// Detach from Deployment by changing warm=true → warm=consuming
		// The Deployment selector requires warm=true, so this pod is now orphaned.
		pCopy := p.DeepCopy()
		pCopy.Labels["warm"] = "consuming"
		updated, err := c.cs.CoreV1().Pods(namespace).Update(ctx, pCopy, metav1.UpdateOptions{})
		if err != nil {
			// Another orchestrator replica claimed this pod first — try the next one
			continue
		}
		return updated, nil
	}
	return nil, nil
}

// CountWarmPods returns the number of active (not being consumed) warm pool pods.
func (c *Client) CountWarmPods(ctx context.Context, namespace string) (int, error) {
	list, err := c.cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=warm-pool,warm=true",
	})
	if err != nil {
		return 0, err
	}
	count := 0
	for _, p := range list.Items {
		if p.Status.Phase != corev1.PodFailed && p.DeletionTimestamp == nil {
			count++
		}
	}
	return count, nil
}

// PodExists checks whether a pod with the given name exists in the namespace
func (c *Client) PodExists(ctx context.Context, name, namespace string) (bool, error) {
	_, err := c.cs.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Helpers
func podName(tenantID string) string  { return "zeroclaw-" + tenantID }
func PVCName(tenantID string) string  { return "pvc-tenant-" + tenantID }
func pvName(tenantID string) string   { return "pv-tenant-" + tenantID }
func strPtr(s string) *string         { return &s }
func int64Ptr(i int64) *int64         { return &i }
