package collector

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type ClusterSnapshot struct {
	ConnectionOK           bool
	KubeconfigPath         string
	Nodes                  []NodeSnapshot
	Pods                   []PodSnapshot
	Namespaces             []NamespaceSnapshot
	Workloads              []WorkloadSnapshot
	ResourceQuotas         []ResourceQuotaSnapshot
	LimitRanges            []LimitRangeSnapshot
	TotalCPUAllocatable    int64
	TotalMemoryAllocatable int64
	TotalCPURequested      int64
	TotalMemoryRequested   int64
	CPUUsagePercent        float64
	MemoryUsagePercent     float64
	PodCapacityPercent     float64
	OverallPressurePercent float64
	RiskLevel              string
}

type NodeSnapshot struct {
	Name                string
	Ready               bool
	AllocatableCPU      int64
	AllocatableMemory   int64
	AllocatablePodCount int64
}

type PodSnapshot struct {
	Namespace        string
	Name             string
	NodeName         string
	Phase            string
	Scheduled        bool
	SchedulingStatus string
	RequestedCPU     int64
	RequestedMemory  int64
	LimitCPU         int64
	LimitMemory      int64
}

type NamespaceSnapshot struct {
	Name string
}

type WorkloadSnapshot struct {
	Namespace       string
	Name            string
	Kind            string
	Replicas        int32
	RequestedCPU    int64
	RequestedMemory int64
	LimitCPU        int64
	LimitMemory     int64
}

type ResourceQuotaSnapshot struct {
	Namespace string
	Name      string
	Hard      map[string]string
	Used      map[string]string
}

type LimitRangeSnapshot struct {
	Namespace string
	Name      string
	Items     []LimitRangeItemSnapshot
}

type LimitRangeItemSnapshot struct {
	Type           string
	Default        map[string]string
	DefaultRequest map[string]string
	Min            map[string]string
	Max            map[string]string
}

type CapacityReport = ClusterSnapshot

func CollectCapacity(ctx context.Context, client kubernetes.Interface) (ClusterSnapshot, error) {
	nodes, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return ClusterSnapshot{}, fmt.Errorf("list cluster nodes: %w", err)
	}

	pods, err := client.CoreV1().Pods(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		return ClusterSnapshot{}, fmt.Errorf("list cluster pods: %w", err)
	}

	namespaces, err := client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return ClusterSnapshot{}, fmt.Errorf("list cluster namespaces: %w", err)
	}

	deployments, err := client.AppsV1().Deployments(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		return ClusterSnapshot{}, fmt.Errorf("list cluster deployments: %w", err)
	}

	resourceQuotas, err := client.CoreV1().ResourceQuotas(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		return ClusterSnapshot{}, fmt.Errorf("list resource quotas: %w", err)
	}

	limitRanges, err := client.CoreV1().LimitRanges(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		return ClusterSnapshot{}, fmt.Errorf("list limit ranges: %w", err)
	}

	snapshot := ClusterSnapshot{
		ConnectionOK:   true,
		Nodes:          make([]NodeSnapshot, 0, len(nodes.Items)),
		Pods:           make([]PodSnapshot, 0, len(pods.Items)),
		Namespaces:     make([]NamespaceSnapshot, 0, len(namespaces.Items)),
		Workloads:      make([]WorkloadSnapshot, 0, len(deployments.Items)),
		ResourceQuotas: make([]ResourceQuotaSnapshot, 0, len(resourceQuotas.Items)),
		LimitRanges:    make([]LimitRangeSnapshot, 0, len(limitRanges.Items)),
	}

	for _, node := range nodes.Items {
		snapshot.Nodes = append(snapshot.Nodes, nodeSnapshot(node))
	}

	for _, pod := range pods.Items {
		snapshot.Pods = append(snapshot.Pods, podSnapshot(pod))
	}

	for _, namespace := range namespaces.Items {
		snapshot.Namespaces = append(snapshot.Namespaces, NamespaceSnapshot{
			Name: namespace.Name,
		})
	}

	for _, deployment := range deployments.Items {
		snapshot.Workloads = append(snapshot.Workloads, deploymentSnapshot(deployment))
	}

	for _, quota := range resourceQuotas.Items {
		snapshot.ResourceQuotas = append(snapshot.ResourceQuotas, ResourceQuotaSnapshot{
			Namespace: quota.Namespace,
			Name:      quota.Name,
			Hard:      resourceListSnapshot(quota.Status.Hard),
			Used:      resourceListSnapshot(quota.Status.Used),
		})
	}

	for _, limitRange := range limitRanges.Items {
		snapshot.LimitRanges = append(snapshot.LimitRanges, limitRangeSnapshot(limitRange))
	}

	return ComputeCapacity(snapshot), nil
}

func ComputeCapacity(report CapacityReport) CapacityReport {
	report.TotalCPUAllocatable = report.TotalAllocatableCPU()
	report.TotalMemoryAllocatable = report.TotalAllocatableMemory()
	report.TotalCPURequested = report.TotalRequestedCPU()
	report.TotalMemoryRequested = report.TotalRequestedMemory()
	report.CPUUsagePercent = usagePercent(report.TotalCPURequested, report.TotalCPUAllocatable)
	report.MemoryUsagePercent = usagePercent(report.TotalMemoryRequested, report.TotalMemoryAllocatable)
	report.PodCapacityPercent = usagePercent(int64(len(report.Pods)), report.TotalAllocatablePods())
	report.OverallPressurePercent = maxFloat64(report.CPUUsagePercent, report.MemoryUsagePercent, report.PodCapacityPercent)
	report.RiskLevel = RiskLevelForUsage(report.OverallPressurePercent)

	return report
}

func RiskLevelForUsage(percent float64) string {
	switch {
	case percent > 90:
		return "CRITICAL"
	case percent >= 75:
		return "HIGH"
	case percent >= 50:
		return "MODERATE"
	default:
		return "HEALTHY"
	}
}

func usagePercent(requested int64, allocatable int64) float64 {
	if allocatable <= 0 {
		return 0
	}

	return float64(requested) / float64(allocatable) * 100
}

func maxFloat64(values ...float64) float64 {
	var max float64
	for _, value := range values {
		if value > max {
			max = value
		}
	}

	return max
}

func (s ClusterSnapshot) ReadyNodeCount() int {
	var count int
	for _, node := range s.Nodes {
		if node.Ready {
			count++
		}
	}

	return count
}

func (s ClusterSnapshot) TotalAllocatableCPU() int64 {
	var total int64
	for _, node := range s.Nodes {
		total += node.AllocatableCPU
	}

	return total
}

func (s ClusterSnapshot) TotalAllocatableMemory() int64 {
	var total int64
	for _, node := range s.Nodes {
		total += node.AllocatableMemory
	}

	return total
}

func (s ClusterSnapshot) TotalAllocatablePods() int64 {
	var total int64
	for _, node := range s.Nodes {
		total += node.AllocatablePodCount
	}

	return total
}

func (s ClusterSnapshot) TotalRequestedCPU() int64 {
	var total int64
	for _, pod := range s.Pods {
		total += pod.RequestedCPU
	}

	return total
}

func (s ClusterSnapshot) TotalRequestedMemory() int64 {
	var total int64
	for _, pod := range s.Pods {
		total += pod.RequestedMemory
	}

	return total
}

func nodeSnapshot(node corev1.Node) NodeSnapshot {
	return NodeSnapshot{
		Name:                node.Name,
		Ready:               isNodeReady(node.Status.Conditions),
		AllocatableCPU:      quantityMilliValue(node.Status.Allocatable[corev1.ResourceCPU]),
		AllocatableMemory:   quantityValue(node.Status.Allocatable[corev1.ResourceMemory]),
		AllocatablePodCount: quantityValue(node.Status.Allocatable[corev1.ResourcePods]),
	}
}

func isNodeReady(conditions []corev1.NodeCondition) bool {
	for _, condition := range conditions {
		if condition.Type == corev1.NodeReady {
			return condition.Status == corev1.ConditionTrue
		}
	}

	return false
}

func podSnapshot(pod corev1.Pod) PodSnapshot {
	requests, limits := podResources(pod.Spec.Containers, pod.Spec.InitContainers)

	return PodSnapshot{
		Namespace:        pod.Namespace,
		Name:             pod.Name,
		NodeName:         pod.Spec.NodeName,
		Phase:            string(pod.Status.Phase),
		Scheduled:        isPodScheduled(pod),
		SchedulingStatus: podSchedulingStatus(pod),
		RequestedCPU:     requests.CPU,
		RequestedMemory:  requests.Memory,
		LimitCPU:         limits.CPU,
		LimitMemory:      limits.Memory,
	}
}

func isPodScheduled(pod corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodScheduled {
			return condition.Status == corev1.ConditionTrue
		}
	}

	return pod.Spec.NodeName != ""
}

func podSchedulingStatus(pod corev1.Pod) string {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodScheduled {
			if condition.Reason != "" {
				return condition.Reason
			}

			return string(condition.Status)
		}
	}

	if pod.Spec.NodeName != "" {
		return "Scheduled"
	}

	return "Unknown"
}

func deploymentSnapshot(deployment appsv1.Deployment) WorkloadSnapshot {
	requests, limits := podResources(
		deployment.Spec.Template.Spec.Containers,
		deployment.Spec.Template.Spec.InitContainers,
	)

	replicas := int32(1)
	if deployment.Spec.Replicas != nil {
		replicas = *deployment.Spec.Replicas
	}

	return WorkloadSnapshot{
		Namespace:       deployment.Namespace,
		Name:            deployment.Name,
		Kind:            "Deployment",
		Replicas:        replicas,
		RequestedCPU:    requests.CPU,
		RequestedMemory: requests.Memory,
		LimitCPU:        limits.CPU,
		LimitMemory:     limits.Memory,
	}
}

type resourceTotals struct {
	CPU    int64
	Memory int64
}

func podResources(containers []corev1.Container, initContainers []corev1.Container) (resourceTotals, resourceTotals) {
	var requests resourceTotals
	var limits resourceTotals

	for _, container := range containers {
		addContainerResources(container, &requests, &limits)
	}

	var initRequests resourceTotals
	var initLimits resourceTotals
	for _, container := range initContainers {
		containerRequests, containerLimits := containerResources(container)
		initRequests = maxResourceTotals(initRequests, containerRequests)
		initLimits = maxResourceTotals(initLimits, containerLimits)
	}

	return maxResourceTotals(requests, initRequests), maxResourceTotals(limits, initLimits)
}

func addContainerResources(container corev1.Container, requests *resourceTotals, limits *resourceTotals) {
	containerRequests, containerLimits := containerResources(container)
	requests.CPU += containerRequests.CPU
	requests.Memory += containerRequests.Memory
	limits.CPU += containerLimits.CPU
	limits.Memory += containerLimits.Memory
}

func containerResources(container corev1.Container) (resourceTotals, resourceTotals) {
	return resourceTotals{
			CPU:    quantityMilliValue(container.Resources.Requests[corev1.ResourceCPU]),
			Memory: quantityValue(container.Resources.Requests[corev1.ResourceMemory]),
		}, resourceTotals{
			CPU:    quantityMilliValue(container.Resources.Limits[corev1.ResourceCPU]),
			Memory: quantityValue(container.Resources.Limits[corev1.ResourceMemory]),
		}
}

func maxResourceTotals(left resourceTotals, right resourceTotals) resourceTotals {
	if right.CPU > left.CPU {
		left.CPU = right.CPU
	}
	if right.Memory > left.Memory {
		left.Memory = right.Memory
	}

	return left
}

func limitRangeSnapshot(limitRange corev1.LimitRange) LimitRangeSnapshot {
	snapshot := LimitRangeSnapshot{
		Namespace: limitRange.Namespace,
		Name:      limitRange.Name,
		Items:     make([]LimitRangeItemSnapshot, 0, len(limitRange.Spec.Limits)),
	}

	for _, item := range limitRange.Spec.Limits {
		snapshot.Items = append(snapshot.Items, LimitRangeItemSnapshot{
			Type:           string(item.Type),
			Default:        resourceListSnapshot(item.Default),
			DefaultRequest: resourceListSnapshot(item.DefaultRequest),
			Min:            resourceListSnapshot(item.Min),
			Max:            resourceListSnapshot(item.Max),
		})
	}

	return snapshot
}

func resourceListSnapshot(resources corev1.ResourceList) map[string]string {
	values := make(map[string]string, len(resources))
	for name, quantity := range resources {
		values[string(name)] = quantity.String()
	}

	return values
}

func quantityMilliValue(quantity resource.Quantity) int64 {
	if quantity.IsZero() {
		return 0
	}

	return quantity.MilliValue()
}

func quantityValue(quantity resource.Quantity) int64 {
	if quantity.IsZero() {
		return 0
	}

	return quantity.Value()
}
