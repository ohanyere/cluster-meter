package analyzer

import (
	"strings"
	"testing"

	"github.com/ohanyere/cluster-meter/internal/state"
)

func TestAnalyzeStructuredScaleUpWithContext(t *testing.T) {
	t.Parallel()

	previous := snapshotWithPods(4, map[string]int{"argocd": 2})
	current := snapshotWithPods(8, map[string]int{"argocd": 6})
	current.Workloads = map[string]state.WorkloadSnapshot{
		"deployment/argocd/argocd-server": {Kind: "Deployment", Namespace: "argocd", Name: "argocd-server", Replicas: 3},
	}

	result := Analyze(&previous, current, state.Compare(previous, current))

	requireCause(t, result.Causes, CauseTypePrimary, "New workload detected: deployment/argocd/argocd-server")
	requireContains(t, result.Recommendations, "Verify autoscaling policies for namespace 'argocd'")
	requireContains(t, result.Recommendations, "Investigate deployments in namespace 'argocd' contributing to pod growth")
	requireContains(t, result.Commands, "kubectl get pods -n argocd")
	requireContains(t, result.Commands, "kubectl describe deployment argocd-server -n argocd")
	requireContains(t, result.Commands, "kubectl scale deployment argocd-server -n argocd --replicas=2")
	requireNoPlaceholderCommands(t, result.Commands)
}

func TestAnalyzeScaleDownDetection(t *testing.T) {
	t.Parallel()

	previous := snapshotWithPods(8, map[string]int{"default": 8})
	current := snapshotWithPods(4, map[string]int{"default": 4})

	result := Analyze(&previous, current, state.Compare(previous, current))

	requireCause(t, result.Causes, CauseTypePrimary, "Workload in namespace default scaled down (-4 pods)")
	requireContains(t, result.Recommendations, "Verify autoscaling policies for namespace 'default'")
}

func TestAnalyzePodPressureImpactDetection(t *testing.T) {
	t.Parallel()

	current := state.Snapshot{PodCapacityPercent: 82}

	result := Analyze(nil, current, state.MissingSummary())

	requireCause(t, result.Causes, CauseTypeImpact, "Cluster nearing pod capacity limits (82%)")
	requireContains(t, result.Recommendations, "Scale node group or increase cluster size")
	requireContains(t, result.Commands, "kubectl get pods -A")
	requireContains(t, result.Commands, "kubectl describe node")
}

func TestAnalyzeCPUPressureImpactDetection(t *testing.T) {
	t.Parallel()

	current := state.Snapshot{CPURequestPercent: 81}

	result := Analyze(nil, current, state.MissingSummary())

	requireCause(t, result.Causes, CauseTypeImpact, "High CPU resource pressure (81%)")
	requireContains(t, result.Recommendations, "Optimize CPU requests/limits")
	requireContains(t, result.Commands, "kubectl top pods -A")
}

func TestAnalyzeMemoryPressureImpactDetection(t *testing.T) {
	t.Parallel()

	current := state.Snapshot{MemoryRequestPercent: 81}

	result := Analyze(nil, current, state.MissingSummary())

	requireCause(t, result.Causes, CauseTypeImpact, "High memory resource pressure (81%)")
	requireContains(t, result.Recommendations, "Investigate memory-heavy workloads")
	requireContains(t, result.Commands, "kubectl top pods -A")
}

func TestAnalyzeUsesNamespaceWorkloadContextForPressure(t *testing.T) {
	t.Parallel()

	previous := snapshotWithPods(4, map[string]int{"argocd": 2})
	current := snapshotWithPods(6, map[string]int{"argocd": 4})
	current.PodCapacityPercent = 85
	current.Workloads = map[string]state.WorkloadSnapshot{
		"deployment/argocd/argocd-server": {Kind: "Deployment", Namespace: "argocd", Name: "argocd-server", Replicas: 3},
	}

	result := Analyze(&previous, current, state.Compare(previous, current))

	requireCause(t, result.Causes, CauseTypeImpact, "Cluster nearing pod capacity limits (85%)")
	requireContains(t, result.Recommendations, "Consider scaling down workloads in namespace 'argocd'")
	requireContains(t, result.Recommendations, "Consider scaling down argocd-server if not required")
	requireContains(t, result.Commands, "kubectl top pods -n argocd")
	requireContains(t, result.Commands, "kubectl scale deployment argocd-server -n argocd --replicas=1")
}

func TestAnalyzeNoiseFiltering(t *testing.T) {
	t.Parallel()

	previous := snapshotWithPods(4, map[string]int{"default": 4})
	previous.OverallPressurePercent = 50
	previous.CPURequestPercent = 20
	current := previous
	current.OverallPressurePercent = 52
	current.CPURequestPercent = 22

	result := Analyze(&previous, current, state.Compare(previous, current))

	requireCause(t, result.Causes, CauseTypePrimary, "No significant operational change detected")
	if len(result.Recommendations) != 0 {
		t.Fatalf("expected no recommendations, got %+v", result.Recommendations)
	}
	if len(result.Commands) != 0 {
		t.Fatalf("expected no commands, got %+v", result.Commands)
	}
}

func TestAnalyzeNoChangeScenarioSuppressesRecommendations(t *testing.T) {
	t.Parallel()

	previous := snapshotWithPods(4, map[string]int{"default": 4})
	current := previous

	result := Analyze(&previous, current, state.Compare(previous, current))

	requireCause(t, result.Causes, CauseTypePrimary, "No significant operational change detected")
	if len(result.Recommendations) != 0 {
		t.Fatalf("expected no recommendations, got %+v", result.Recommendations)
	}
	if len(result.Commands) != 0 {
		t.Fatalf("expected no commands, got %+v", result.Commands)
	}
}

func TestAnalyzeDeduplicatesAndOrdersCommandSuggestions(t *testing.T) {
	t.Parallel()

	current := state.Snapshot{
		CPURequestPercent:    85,
		MemoryRequestPercent: 90,
	}

	result := Analyze(nil, current, state.MissingSummary())

	if countOccurrences(result.Commands, "kubectl top pods -A") != 1 {
		t.Fatalf("expected kubectl top pods -A once, got %+v", result.Commands)
	}
	requireCommandOrder(t, result.Commands, "kubectl get pods -A", "kubectl top pods -A")
	requireNoPlaceholderCommands(t, result.Commands)
}

func snapshotWithPods(totalPods int, namespaces map[string]int) state.Snapshot {
	snapshot := state.Snapshot{
		TotalPods:  totalPods,
		Namespaces: map[string]state.NamespaceSnapshot{},
		Workloads:  map[string]state.WorkloadSnapshot{},
	}
	for namespace, podCount := range namespaces {
		snapshot.Namespaces[namespace] = state.NamespaceSnapshot{PodCount: podCount}
	}

	return snapshot
}

func requireCause(t *testing.T, causes []Cause, causeType string, message string) {
	t.Helper()

	for _, cause := range causes {
		if cause.Type == causeType && cause.Message == message {
			return
		}
	}

	t.Fatalf("expected %s cause %q in %+v", causeType, message, causes)
}

func requireContains(t *testing.T, values []string, want string) {
	t.Helper()

	for _, value := range values {
		if value == want {
			return
		}
	}

	t.Fatalf("expected %q in %+v", want, values)
}

func requireNoPlaceholderCommands(t *testing.T, commands []string) {
	t.Helper()

	for _, command := range commands {
		if strings.Contains(command, "<") || strings.Contains(command, ">") {
			t.Fatalf("expected concrete commands, got %+v", commands)
		}
	}
}

func requireCommandOrder(t *testing.T, commands []string, before string, after string) {
	t.Helper()

	beforeIndex := -1
	afterIndex := -1
	for i, command := range commands {
		if command == before {
			beforeIndex = i
		}
		if command == after {
			afterIndex = i
		}
	}
	if beforeIndex == -1 || afterIndex == -1 || beforeIndex > afterIndex {
		t.Fatalf("expected %q before %q in %+v", before, after, commands)
	}
}

func countOccurrences(values []string, want string) int {
	var count int
	for _, value := range values {
		if value == want {
			count++
		}
	}

	return count
}
