package collector

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestCollectCapacity(t *testing.T) {
	t.Parallel()

	client := fake.NewSimpleClientset(
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
			Status: corev1.NodeStatus{
				Allocatable: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("4Gi"),
					corev1.ResourcePods:   resource.MustParse("110"),
				},
				Conditions: []corev1.NodeCondition{
					{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
				},
			},
		},
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "node-b"},
			Status: corev1.NodeStatus{
				Allocatable: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("1500m"),
					corev1.ResourceMemory: resource.MustParse("2Gi"),
					corev1.ResourcePods:   resource.MustParse("50"),
				},
				Conditions: []corev1.NodeCondition{
					{Type: corev1.NodeReady, Status: corev1.ConditionFalse},
				},
			},
		},
		&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: "default"},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "pod-a", Namespace: "default"},
			Spec: corev1.PodSpec{
				NodeName: "node-a",
				Containers: []corev1.Container{
					{
						Name: "app",
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("250m"),
								corev1.ResourceMemory: resource.MustParse("128Mi"),
							},
							Limits: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("500m"),
								corev1.ResourceMemory: resource.MustParse("256Mi"),
							},
						},
					},
				},
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				Conditions: []corev1.PodCondition{
					{Type: corev1.PodScheduled, Status: corev1.ConditionTrue},
				},
			},
		},
		&corev1.ResourceQuota{
			ObjectMeta: metav1.ObjectMeta{Name: "quota", Namespace: "default"},
			Status: corev1.ResourceQuotaStatus{
				Hard: corev1.ResourceList{
					corev1.ResourcePods: resource.MustParse("10"),
				},
				Used: corev1.ResourceList{
					corev1.ResourcePods: resource.MustParse("1"),
				},
			},
		},
		&corev1.LimitRange{
			ObjectMeta: metav1.ObjectMeta{Name: "limits", Namespace: "default"},
			Spec: corev1.LimitRangeSpec{
				Limits: []corev1.LimitRangeItem{
					{
						Type: corev1.LimitTypeContainer,
						DefaultRequest: corev1.ResourceList{
							corev1.ResourceCPU: resource.MustParse("100m"),
						},
					},
				},
			},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default"},
			Spec: appsv1.DeploymentSpec{
				Replicas: int32Ptr(3),
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name: "web",
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("100m"),
										corev1.ResourceMemory: resource.MustParse("64Mi"),
									},
								},
							},
						},
					},
				},
			},
		},
	)

	report, err := CollectCapacity(context.Background(), client)
	if err != nil {
		t.Fatalf("CollectCapacity() error = %v", err)
	}

	if !report.ConnectionOK {
		t.Fatal("expected successful connection result")
	}

	if len(report.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(report.Nodes))
	}

	if report.ReadyNodeCount() != 1 {
		t.Fatalf("expected 1 ready node, got %d", report.ReadyNodeCount())
	}

	if report.TotalAllocatableCPU() != 3500 {
		t.Fatalf("expected 3500m allocatable CPU, got %dm", report.TotalAllocatableCPU())
	}

	if report.TotalAllocatablePods() != 160 {
		t.Fatalf("expected 160 allocatable pods, got %d", report.TotalAllocatablePods())
	}

	if report.TotalRequestedCPU() != 250 {
		t.Fatalf("expected 250m requested CPU, got %dm", report.TotalRequestedCPU())
	}

	if report.TotalCPUAllocatable != 3500 {
		t.Fatalf("expected 3500m total CPU allocatable, got %dm", report.TotalCPUAllocatable)
	}

	if report.TotalMemoryRequested != 134217728 {
		t.Fatalf("expected 134217728 bytes total memory requested, got %d", report.TotalMemoryRequested)
	}

	if report.CPUUsagePercent < 7.14 || report.CPUUsagePercent > 7.15 {
		t.Fatalf("expected CPU usage around 7.14%%, got %.2f%%", report.CPUUsagePercent)
	}

	if report.PodCapacityPercent != 0.625 {
		t.Fatalf("expected pod capacity around 0.625%%, got %.3f%%", report.PodCapacityPercent)
	}

	if report.OverallPressurePercent < 7.14 || report.OverallPressurePercent > 7.15 {
		t.Fatalf("expected overall pressure around 7.14%%, got %.2f%%", report.OverallPressurePercent)
	}

	if report.RiskLevel != "HEALTHY" {
		t.Fatalf("expected healthy risk level, got %q", report.RiskLevel)
	}

	if len(report.Namespaces) != 1 {
		t.Fatalf("expected 1 namespace, got %d", len(report.Namespaces))
	}

	if len(report.Workloads) != 1 {
		t.Fatalf("expected 1 workload, got %d", len(report.Workloads))
	}

	if workload := report.Workloads[0]; workload.Name != "web" || workload.Kind != "Deployment" || workload.Replicas != 3 || workload.RequestedCPU != 100 {
		t.Fatalf("unexpected workload snapshot: %+v", workload)
	}

	if len(report.ResourceQuotas) != 1 || report.ResourceQuotas[0].Hard["pods"] != "10" {
		t.Fatalf("unexpected resource quota snapshot: %+v", report.ResourceQuotas)
	}

	if len(report.LimitRanges) != 1 || report.LimitRanges[0].Items[0].DefaultRequest["cpu"] != "100m" {
		t.Fatalf("unexpected limit range snapshot: %+v", report.LimitRanges)
	}
}

func TestPodSnapshotHandlesMissingValues(t *testing.T) {
	t.Parallel()

	snapshot := podSnapshot(corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pending", Namespace: "default"},
		Status:     corev1.PodStatus{Phase: corev1.PodPending},
	})

	if snapshot.Scheduled {
		t.Fatalf("expected unscheduled pod, got %+v", snapshot)
	}

	if snapshot.SchedulingStatus != "Unknown" {
		t.Fatalf("expected Unknown scheduling status, got %q", snapshot.SchedulingStatus)
	}

	if snapshot.RequestedCPU != 0 || snapshot.RequestedMemory != 0 || snapshot.LimitCPU != 0 || snapshot.LimitMemory != 0 {
		t.Fatalf("expected missing resources to convert to zero, got %+v", snapshot)
	}
}

func TestPodSnapshotUsesEffectiveInitContainerResources(t *testing.T) {
	t.Parallel()

	snapshot := podSnapshot(corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "with-init", Namespace: "default"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "app",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("250m"),
							corev1.ResourceMemory: resource.MustParse("128Mi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("500m"),
							corev1.ResourceMemory: resource.MustParse("256Mi"),
						},
					},
				},
				{
					Name: "sidecar",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100m"),
							corev1.ResourceMemory: resource.MustParse("64Mi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("200m"),
							corev1.ResourceMemory: resource.MustParse("128Mi"),
						},
					},
				},
			},
			InitContainers: []corev1.Container{
				{
					Name: "init-light",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("200m"),
							corev1.ResourceMemory: resource.MustParse("32Mi"),
						},
					},
				},
				{
					Name: "init-heavy",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("700m"),
							corev1.ResourceMemory: resource.MustParse("256Mi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1"),
							corev1.ResourceMemory: resource.MustParse("512Mi"),
						},
					},
				},
			},
		},
	})

	if snapshot.RequestedCPU != 700 {
		t.Fatalf("expected effective request CPU to use max init CPU, got %dm", snapshot.RequestedCPU)
	}

	if snapshot.RequestedMemory != 268435456 {
		t.Fatalf("expected effective request memory to use max init memory, got %d bytes", snapshot.RequestedMemory)
	}

	if snapshot.LimitCPU != 1000 {
		t.Fatalf("expected effective limit CPU to use max init CPU, got %dm", snapshot.LimitCPU)
	}

	if snapshot.LimitMemory != 536870912 {
		t.Fatalf("expected effective limit memory to use max init memory, got %d bytes", snapshot.LimitMemory)
	}
}

func TestComputeCapacity(t *testing.T) {
	t.Parallel()

	report := ComputeCapacity(CapacityReport{
		Nodes: []NodeSnapshot{
			{AllocatableCPU: 1000, AllocatableMemory: 1000, AllocatablePodCount: 10},
			{AllocatableCPU: 1000, AllocatableMemory: 3000, AllocatablePodCount: 12},
		},
		Pods: []PodSnapshot{
			{RequestedCPU: 500, RequestedMemory: 1000},
			{RequestedCPU: 500, RequestedMemory: 2000},
		},
	})

	if report.TotalCPUAllocatable != 2000 {
		t.Fatalf("expected 2000m CPU allocatable, got %dm", report.TotalCPUAllocatable)
	}

	if report.TotalMemoryAllocatable != 4000 {
		t.Fatalf("expected 4000 bytes memory allocatable, got %d", report.TotalMemoryAllocatable)
	}

	if report.TotalCPURequested != 1000 {
		t.Fatalf("expected 1000m CPU requested, got %dm", report.TotalCPURequested)
	}

	if report.TotalMemoryRequested != 3000 {
		t.Fatalf("expected 3000 bytes memory requested, got %d", report.TotalMemoryRequested)
	}

	if report.CPUUsagePercent != 50 {
		t.Fatalf("expected 50%% CPU usage, got %.2f%%", report.CPUUsagePercent)
	}

	if report.MemoryUsagePercent != 75 {
		t.Fatalf("expected 75%% memory usage, got %.2f%%", report.MemoryUsagePercent)
	}

	if report.PodCapacityPercent < 9.09 || report.PodCapacityPercent > 9.10 {
		t.Fatalf("expected pod capacity around 9.09%%, got %.2f%%", report.PodCapacityPercent)
	}

	if report.OverallPressurePercent != 75 {
		t.Fatalf("expected 75%% overall pressure, got %.2f%%", report.OverallPressurePercent)
	}

	if report.RiskLevel != "HIGH" {
		t.Fatalf("expected highest risk level to be HIGH, got %q", report.RiskLevel)
	}
}

func TestComputeCapacityUsesPodCapacityForOverallPressure(t *testing.T) {
	t.Parallel()

	report := ComputeCapacity(CapacityReport{
		Nodes: []NodeSnapshot{
			{AllocatableCPU: 1000, AllocatableMemory: 1000, AllocatablePodCount: 11},
			{AllocatableCPU: 1000, AllocatableMemory: 1000, AllocatablePodCount: 11},
		},
		Pods: make([]PodSnapshot, 16),
	})

	if report.PodCapacityPercent < 72.72 || report.PodCapacityPercent > 72.73 {
		t.Fatalf("expected pod capacity around 72.73%%, got %.2f%%", report.PodCapacityPercent)
	}

	if report.OverallPressurePercent < 72.72 || report.OverallPressurePercent > 72.73 {
		t.Fatalf("expected pod capacity to drive overall pressure, got %.2f%%", report.OverallPressurePercent)
	}

	if report.RiskLevel != "MODERATE" {
		t.Fatalf("expected moderate overall risk level, got %q", report.RiskLevel)
	}
}

func TestRiskLevelForUsage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		percent float64
		want    string
	}{
		{name: "below fifty", percent: 49.99, want: "HEALTHY"},
		{name: "at fifty", percent: 50, want: "MODERATE"},
		{name: "at seventy five", percent: 75, want: "HIGH"},
		{name: "at ninety", percent: 90, want: "HIGH"},
		{name: "above ninety", percent: 90.01, want: "CRITICAL"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := RiskLevelForUsage(test.percent); got != test.want {
				t.Fatalf("RiskLevelForUsage(%.2f) = %q, want %q", test.percent, got, test.want)
			}
		})
	}
}

func int32Ptr(value int32) *int32 {
	return &value
}
