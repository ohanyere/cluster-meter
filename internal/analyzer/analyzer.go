package analyzer

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ohanyere/cluster-meter/internal/state"
)

const (
	CauseTypePrimary = "primary"
	CauseTypeImpact  = "impact"
)

type Cause struct {
	Type    string
	Message string
}

type AnalysisResult struct {
	Causes          []Cause
	Recommendations []string
	Commands        []string
}

type contextSignal struct {
	namespace       string
	podDelta        int
	workload        state.WorkloadSnapshot
	hasWorkload     bool
	newWorkload     bool
	removedWorkload bool
}

func Analyze(previous *state.Snapshot, current state.Snapshot, diff state.ChangeSummary) AnalysisResult {
	signal := detectContext(previous, current)
	var result AnalysisResult

	if previous != nil {
		switch {
		case signal.newWorkload:
			result.addCause(CauseTypePrimary, fmt.Sprintf("New workload detected: %s", workloadDisplay(signal.workload)))
		case signal.removedWorkload:
			result.addCause(CauseTypePrimary, fmt.Sprintf("Workload removed: %s", workloadDisplay(signal.workload)))
		case signal.podDelta > 0:
			result.addCause(CauseTypePrimary, fmt.Sprintf("Workload %s scaled up (+%d pods)", contextName(signal), signal.podDelta))
		case signal.podDelta < 0:
			result.addCause(CauseTypePrimary, fmt.Sprintf("Workload %s scaled down (%d pods)", contextName(signal), signal.podDelta))
		}
	}

	if current.PodCapacityPercent > 80 {
		result.addCause(CauseTypeImpact, fmt.Sprintf("Cluster nearing pod capacity limits (%.0f%%)", current.PodCapacityPercent))
	}
	if current.CPURequestPercent > 80 {
		result.addCause(CauseTypeImpact, fmt.Sprintf("High CPU resource pressure (%.0f%%)", current.CPURequestPercent))
	}
	if current.MemoryRequestPercent > 80 {
		result.addCause(CauseTypeImpact, fmt.Sprintf("High memory resource pressure (%.0f%%)", current.MemoryRequestPercent))
	}

	if len(result.Causes) == 0 && noMeaningfulSignal(previous, current, diff) {
		result.addCause(CauseTypePrimary, "No significant operational change detected")
		return result
	}

	result.addRecommendations(signal, current)
	result.addCommands(signal, current)
	return result
}

func detectContext(previous *state.Snapshot, current state.Snapshot) contextSignal {
	var signal contextSignal
	if previous == nil {
		signal.workload, signal.hasWorkload = firstWorkload(current.Workloads, "")
		if signal.hasWorkload {
			signal.namespace = signal.workload.Namespace
		}
		return signal
	}

	signal.namespace, signal.podDelta = largestNamespacePodDelta(previous.Namespaces, current.Namespaces)
	if absInt(signal.podDelta) < 1 {
		signal.podDelta = 0
	}

	if workload, ok := firstNewWorkload(previous.Workloads, current.Workloads, signal.namespace); ok {
		signal.workload = workload
		signal.hasWorkload = true
		signal.newWorkload = true
		signal.namespace = workload.Namespace
		return signal
	}

	if workload, ok := firstRemovedWorkload(previous.Workloads, current.Workloads, signal.namespace); ok {
		signal.workload = workload
		signal.hasWorkload = true
		signal.removedWorkload = true
		signal.namespace = workload.Namespace
		return signal
	}

	if workload, ok := firstWorkload(current.Workloads, signal.namespace); ok {
		signal.workload = workload
		signal.hasWorkload = true
		if signal.namespace == "" {
			signal.namespace = workload.Namespace
		}
	}

	return signal
}

func noMeaningfulSignal(previous *state.Snapshot, current state.Snapshot, diff state.ChangeSummary) bool {
	if previous == nil {
		return len(diff.Lines) == 1 && strings.Contains(diff.Lines[0], "No previous snapshot")
	}
	if len(diff.Lines) == 1 && diff.Lines[0] == "No significant changes since last snapshot." {
		return true
	}

	return absFloat(current.OverallPressurePercent-previous.OverallPressurePercent) < 3 &&
		absFloat(current.CPURequestPercent-previous.CPURequestPercent) < 3 &&
		absFloat(current.MemoryRequestPercent-previous.MemoryRequestPercent) < 3 &&
		absFloat(current.PodCapacityPercent-previous.PodCapacityPercent) < 3 &&
		absInt(current.TotalPods-previous.TotalPods) < 1
}

func (r *AnalysisResult) addRecommendations(signal contextSignal, current state.Snapshot) {
	if signal.namespace != "" {
		if signal.podDelta != 0 || signal.newWorkload || signal.removedWorkload {
			r.addRecommendation(fmt.Sprintf("Verify autoscaling policies for namespace '%s'", signal.namespace))
			r.addRecommendation(fmt.Sprintf("Investigate deployments in namespace '%s' contributing to pod growth", signal.namespace))
		}
		if current.PodCapacityPercent > 80 {
			r.addRecommendation(fmt.Sprintf("Consider scaling down workloads in namespace '%s'", signal.namespace))
		}
	} else if current.PodCapacityPercent > 80 {
		r.addRecommendation("Scale node group or increase cluster size")
	}

	if signal.hasWorkload && !signal.removedWorkload {
		if signal.podDelta > 0 || current.PodCapacityPercent > 80 {
			r.addRecommendation(fmt.Sprintf("Consider scaling down %s if not required", signal.workload.Name))
		}
	}

	if current.CPURequestPercent > 80 {
		if signal.namespace != "" {
			r.addRecommendation(fmt.Sprintf("Review CPU requests for workloads in namespace '%s'", signal.namespace))
		} else {
			r.addRecommendation("Optimize CPU requests/limits")
		}
	}

	if current.MemoryRequestPercent > 80 {
		if signal.namespace != "" {
			r.addRecommendation(fmt.Sprintf("Investigate memory-heavy workloads in namespace '%s'", signal.namespace))
		} else {
			r.addRecommendation("Investigate memory-heavy workloads")
		}
	}
}

func (r *AnalysisResult) addCommands(signal contextSignal, current state.Snapshot) {
	namespace := signal.namespace
	if namespace != "" {
		r.addCommand(commandInspect, fmt.Sprintf("kubectl get pods -n %s", namespace))
		r.addCommand(commandInspect, fmt.Sprintf("kubectl top pods -n %s", namespace))
		r.addCommand(commandInspect, fmt.Sprintf("kubectl get hpa -n %s", namespace))
	} else if hasPressure(current) {
		r.addCommand(commandInspect, "kubectl get pods -A")
		r.addCommand(commandInspect, "kubectl top pods -A")
		r.addCommand(commandInspect, "kubectl get hpa -A")
	}

	if signal.hasWorkload && strings.EqualFold(signal.workload.Kind, "Deployment") {
		r.addCommand(commandDiagnose, fmt.Sprintf("kubectl describe deployment %s -n %s", signal.workload.Name, signal.workload.Namespace))
		if !signal.removedWorkload {
			r.addCommand(commandAct, fmt.Sprintf("kubectl scale deployment %s -n %s --replicas=%d", signal.workload.Name, signal.workload.Namespace, suggestedReplicas(signal.workload, signal.podDelta)))
		}
	} else if current.PodCapacityPercent > 80 {
		r.addCommand(commandDiagnose, "kubectl describe node")
	}

	r.limitCommands(6)
}

type commandKind int

const (
	commandInspect commandKind = iota
	commandDiagnose
	commandAct
)

type commandSuggestion struct {
	kind  commandKind
	value string
}

func (r *AnalysisResult) addCommand(kind commandKind, value string) {
	for _, existing := range r.Commands {
		if strings.EqualFold(existing, value) {
			return
		}
	}

	r.Commands = append(r.Commands, value)
}

func (r *AnalysisResult) limitCommands(limit int) {
	if len(r.Commands) <= limit {
		return
	}

	r.Commands = r.Commands[:limit]
}

func (r *AnalysisResult) addCause(causeType string, message string) {
	for _, cause := range r.Causes {
		if cause.Type == causeType && strings.EqualFold(cause.Message, message) {
			return
		}
	}

	r.Causes = append(r.Causes, Cause{Type: causeType, Message: message})
}

func (r *AnalysisResult) addRecommendation(value string) {
	for _, existing := range r.Recommendations {
		if strings.EqualFold(existing, value) {
			return
		}
	}

	r.Recommendations = append(r.Recommendations, value)
}

func largestNamespacePodDelta(previous map[string]state.NamespaceSnapshot, current map[string]state.NamespaceSnapshot) (string, int) {
	names := map[string]struct{}{}
	for name := range previous {
		names[name] = struct{}{}
	}
	for name := range current {
		names[name] = struct{}{}
	}

	var selected string
	var selectedDelta int
	for name := range names {
		delta := current[name].PodCount - previous[name].PodCount
		if absInt(delta) > absInt(selectedDelta) || selected == "" {
			selected = name
			selectedDelta = delta
		}
	}

	return selected, selectedDelta
}

func firstNewWorkload(previous map[string]state.WorkloadSnapshot, current map[string]state.WorkloadSnapshot, namespace string) (state.WorkloadSnapshot, bool) {
	for _, key := range sortedWorkloadKeys(current) {
		workload := current[key]
		if _, ok := previous[key]; !ok && (namespace == "" || workload.Namespace == namespace) {
			return workload, true
		}
	}

	return state.WorkloadSnapshot{}, false
}

func firstRemovedWorkload(previous map[string]state.WorkloadSnapshot, current map[string]state.WorkloadSnapshot, namespace string) (state.WorkloadSnapshot, bool) {
	for _, key := range sortedWorkloadKeys(previous) {
		workload := previous[key]
		if _, ok := current[key]; !ok && (namespace == "" || workload.Namespace == namespace) {
			return workload, true
		}
	}

	return state.WorkloadSnapshot{}, false
}

func firstWorkload(workloads map[string]state.WorkloadSnapshot, namespace string) (state.WorkloadSnapshot, bool) {
	for _, key := range sortedWorkloadKeys(workloads) {
		workload := workloads[key]
		if namespace == "" || workload.Namespace == namespace {
			return workload, true
		}
	}

	return state.WorkloadSnapshot{}, false
}

func sortedWorkloadKeys(workloads map[string]state.WorkloadSnapshot) []string {
	keys := make([]string, 0, len(workloads))
	for key := range workloads {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func contextName(signal contextSignal) string {
	if signal.hasWorkload {
		return fmt.Sprintf("%s in namespace %s", signal.workload.Name, signal.workload.Namespace)
	}
	if signal.namespace != "" {
		return fmt.Sprintf("in namespace %s", signal.namespace)
	}

	return "in cluster"
}

func workloadDisplay(workload state.WorkloadSnapshot) string {
	return strings.ToLower(workload.Kind) + "/" + workload.Namespace + "/" + workload.Name
}

func suggestedReplicas(workload state.WorkloadSnapshot, podDelta int) int32 {
	if podDelta > 0 && workload.Replicas > int32(podDelta) {
		return workload.Replicas - int32(podDelta)
	}
	if workload.Replicas > 1 {
		return workload.Replicas - 1
	}

	return 1
}

func hasPressure(snapshot state.Snapshot) bool {
	return snapshot.PodCapacityPercent > 80 || snapshot.CPURequestPercent > 80 || snapshot.MemoryRequestPercent > 80
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}

	return value
}

func absFloat(value float64) float64 {
	if value < 0 {
		return -value
	}

	return value
}
