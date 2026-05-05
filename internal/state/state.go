package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ohanyere/cluster-meter/internal/collector"
)

type LoadStatus string

const (
	LoadStatusLoaded  LoadStatus = "loaded"
	LoadStatusMissing LoadStatus = "missing"
	LoadStatusCorrupt LoadStatus = "corrupt"
)

type Snapshot struct {
	Timestamp              time.Time                    `json:"timestamp"`
	OverallPressurePercent float64                      `json:"overallPressurePercent"`
	CPURequestPercent      float64                      `json:"cpuRequestPercent"`
	MemoryRequestPercent   float64                      `json:"memoryRequestPercent"`
	PodCapacityPercent     float64                      `json:"podCapacityPercent"`
	TotalPods              int                          `json:"totalPods"`
	TotalRequestedCPU      int64                        `json:"totalRequestedCPU"`
	TotalRequestedMemory   int64                        `json:"totalRequestedMemory"`
	Namespaces             map[string]NamespaceSnapshot `json:"namespaces"`
	Workloads              map[string]WorkloadSnapshot  `json:"workloads"`
}

type NamespaceSnapshot struct {
	PodCount        int   `json:"podCount"`
	RequestedCPU    int64 `json:"requestedCPU"`
	RequestedMemory int64 `json:"requestedMemory"`
}

type WorkloadSnapshot struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Replicas  int32  `json:"replicas"`
}

type ChangeSummary struct {
	Lines []string
}

func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}

	return filepath.Join(home, ".cluster-meter", "state.json"), nil
}

func Load(path string) (Snapshot, LoadStatus, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Snapshot{}, LoadStatusMissing, nil
		}

		return Snapshot{}, "", fmt.Errorf("read state file: %w", err)
	}

	var snapshot Snapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return Snapshot{}, LoadStatusCorrupt, nil
	}
	if snapshot.Namespaces == nil {
		snapshot.Namespaces = map[string]NamespaceSnapshot{}
	}
	if snapshot.Workloads == nil {
		snapshot.Workloads = map[string]WorkloadSnapshot{}
	}

	return snapshot, LoadStatusLoaded, nil
}

func Save(path string, snapshot Snapshot) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}

	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state snapshot: %w", err)
	}
	data = append(data, '\n')

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return fmt.Errorf("write state file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("replace state file: %w", err)
	}

	return nil
}

func FromReport(report collector.CapacityReport, timestamp time.Time) Snapshot {
	snapshot := Snapshot{
		Timestamp:              timestamp.UTC(),
		OverallPressurePercent: report.OverallPressurePercent,
		CPURequestPercent:      report.CPUUsagePercent,
		MemoryRequestPercent:   report.MemoryUsagePercent,
		PodCapacityPercent:     report.PodCapacityPercent,
		TotalPods:              len(report.Pods),
		TotalRequestedCPU:      report.TotalCPURequested,
		TotalRequestedMemory:   report.TotalMemoryRequested,
		Namespaces:             map[string]NamespaceSnapshot{},
		Workloads:              map[string]WorkloadSnapshot{},
	}

	for _, namespace := range report.Namespaces {
		if _, ok := snapshot.Namespaces[namespace.Name]; !ok {
			snapshot.Namespaces[namespace.Name] = NamespaceSnapshot{}
		}
	}

	for _, pod := range report.Pods {
		ns := snapshot.Namespaces[pod.Namespace]
		ns.PodCount++
		ns.RequestedCPU += pod.RequestedCPU
		ns.RequestedMemory += pod.RequestedMemory
		snapshot.Namespaces[pod.Namespace] = ns
	}

	for _, workload := range report.Workloads {
		key := workloadKey(workload.Kind, workload.Namespace, workload.Name)
		snapshot.Workloads[key] = WorkloadSnapshot{
			Kind:      workload.Kind,
			Namespace: workload.Namespace,
			Name:      workload.Name,
			Replicas:  workload.Replicas,
		}
	}

	return snapshot
}

func Compare(previous Snapshot, current Snapshot) ChangeSummary {
	var lines []string

	lines = appendPercentChange(lines, "Overall pressure", previous.OverallPressurePercent, current.OverallPressurePercent)
	lines = appendPercentChange(lines, "CPU requests", previous.CPURequestPercent, current.CPURequestPercent)
	lines = appendPercentChange(lines, "Memory requests", previous.MemoryRequestPercent, current.MemoryRequestPercent)
	lines = appendPercentChange(lines, "Pod capacity", previous.PodCapacityPercent, current.PodCapacityPercent)

	if previous.TotalPods != current.TotalPods {
		lines = append(lines, fmt.Sprintf("Pod count changed: %d -> %d", previous.TotalPods, current.TotalPods))
	}

	lines = append(lines, namespaceChanges(previous.Namespaces, current.Namespaces)...)
	lines = append(lines, requestTotalChanges(previous, current)...)
	lines = append(lines, workloadChanges(previous.Workloads, current.Workloads)...)

	if len(lines) == 0 {
		lines = append(lines, "No significant changes since last snapshot.")
	}

	return ChangeSummary{Lines: lines}
}

func MissingSummary() ChangeSummary {
	return ChangeSummary{Lines: []string{"No previous snapshot found. Current state saved."}}
}

func CorruptSummary() ChangeSummary {
	return ChangeSummary{Lines: []string{"Previous snapshot was corrupted. Current state saved."}}
}

func DisabledSummary() ChangeSummary {
	return ChangeSummary{Lines: []string{"State tracking disabled."}}
}

func SaveWarningSummary(err error) ChangeSummary {
	return ChangeSummary{Lines: []string{fmt.Sprintf("Warning: could not save current state: %v", err)}}
}

func appendPercentChange(lines []string, label string, previous float64, current float64) []string {
	if roundedPercent(previous) == roundedPercent(current) {
		return lines
	}

	return append(lines, fmt.Sprintf("%s changed: %.0f%% -> %.0f%%", label, previous, current))
}

func namespaceChanges(previous map[string]NamespaceSnapshot, current map[string]NamespaceSnapshot) []string {
	names := sortedNamespaceNames(previous, current)
	lines := make([]string, 0)

	for _, name := range names {
		prev := previous[name]
		curr := current[name]
		if prev.PodCount != curr.PodCount {
			delta := curr.PodCount - prev.PodCount
			lines = append(lines, fmt.Sprintf("%s namespace %s %s pods", name, podVerb(delta), signedInt(delta)))
		}
		if prev.RequestedCPU != curr.RequestedCPU {
			lines = append(lines, fmt.Sprintf("%s namespace CPU requests %s by %dm", name, changeVerb(curr.RequestedCPU-prev.RequestedCPU), absInt64(curr.RequestedCPU-prev.RequestedCPU)))
		}
		if prev.RequestedMemory != curr.RequestedMemory {
			lines = append(lines, fmt.Sprintf("%s namespace memory requests %s by %s", name, changeVerb(curr.RequestedMemory-prev.RequestedMemory), formatBytes(absInt64(curr.RequestedMemory-prev.RequestedMemory))))
		}
	}

	return lines
}

func requestTotalChanges(previous Snapshot, current Snapshot) []string {
	var lines []string
	if previous.TotalRequestedCPU != current.TotalRequestedCPU {
		delta := current.TotalRequestedCPU - previous.TotalRequestedCPU
		lines = append(lines, fmt.Sprintf("CPU requests %s by %dm", changeVerb(delta), absInt64(delta)))
	}
	if previous.TotalRequestedMemory != current.TotalRequestedMemory {
		delta := current.TotalRequestedMemory - previous.TotalRequestedMemory
		lines = append(lines, fmt.Sprintf("Memory requests %s by %s", changeVerb(delta), formatBytes(absInt64(delta))))
	}

	return lines
}

func workloadChanges(previous map[string]WorkloadSnapshot, current map[string]WorkloadSnapshot) []string {
	var lines []string

	for _, key := range sortedWorkloadKeys(current) {
		if _, ok := previous[key]; !ok {
			lines = append(lines, fmt.Sprintf("New workload detected: %s", workloadDisplay(current[key])))
		}
	}
	for _, key := range sortedWorkloadKeys(previous) {
		if _, ok := current[key]; !ok {
			lines = append(lines, fmt.Sprintf("Workload removed: %s", workloadDisplay(previous[key])))
		}
	}

	return lines
}

func sortedNamespaceNames(left map[string]NamespaceSnapshot, right map[string]NamespaceSnapshot) []string {
	seen := map[string]struct{}{}
	for name := range left {
		seen[name] = struct{}{}
	}
	for name := range right {
		seen[name] = struct{}{}
	}

	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedWorkloadKeys(workloads map[string]WorkloadSnapshot) []string {
	keys := make([]string, 0, len(workloads))
	for key := range workloads {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func workloadKey(kind string, namespace string, name string) string {
	return strings.ToLower(kind) + "/" + namespace + "/" + name
}

func workloadDisplay(workload WorkloadSnapshot) string {
	return strings.ToLower(workload.Kind) + "/" + workload.Name
}

func roundedPercent(value float64) int {
	if value < 0 {
		return int(value - 0.5)
	}

	return int(value + 0.5)
}

func podVerb(delta int) string {
	if delta >= 0 {
		return "added"
	}

	return "removed"
}

func signedInt(value int) string {
	if value >= 0 {
		return fmt.Sprintf("+%d", value)
	}

	return fmt.Sprintf("%d", value)
}

func changeVerb(delta int64) string {
	if delta >= 0 {
		return "increased"
	}

	return "decreased"
}

func absInt64(value int64) int64 {
	if value < 0 {
		return -value
	}

	return value
}

func formatBytes(value int64) string {
	const mib = 1024 * 1024
	if value%mib == 0 {
		return fmt.Sprintf("%dMi", value/mib)
	}

	return fmt.Sprintf("%d bytes", value)
}
