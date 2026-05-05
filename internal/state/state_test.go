package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ohanyere/cluster-meter/internal/collector"
)

func TestLoadMissingPreviousSnapshot(t *testing.T) {
	t.Parallel()

	_, status, err := Load(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if status != LoadStatusMissing {
		t.Fatalf("status = %q, want %q", status, LoadStatusMissing)
	}

	if got := strings.Join(MissingSummary().Lines, "\n"); !strings.Contains(got, "No previous snapshot found") {
		t.Fatalf("unexpected missing summary: %q", got)
	}
}

func TestLoadCorruptedPreviousSnapshot(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
		t.Fatalf("write corrupt state: %v", err)
	}

	_, status, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if status != LoadStatusCorrupt {
		t.Fatalf("status = %q, want %q", status, LoadStatusCorrupt)
	}

	if got := strings.Join(CorruptSummary().Lines, "\n"); !strings.Contains(got, "corrupted") {
		t.Fatalf("unexpected corrupt summary: %q", got)
	}
}

func TestComparePressureIncreaseAndDecrease(t *testing.T) {
	t.Parallel()

	increase := Compare(
		Snapshot{OverallPressurePercent: 52, CPURequestPercent: 20, MemoryRequestPercent: 30, PodCapacityPercent: 61},
		Snapshot{OverallPressurePercent: 73, CPURequestPercent: 25, MemoryRequestPercent: 28, PodCapacityPercent: 50},
	)
	requireLine(t, increase, "Overall pressure changed: 52% -> 73%")
	requireLine(t, increase, "CPU requests changed: 20% -> 25%")
	requireLine(t, increase, "Memory requests changed: 30% -> 28%")

	decrease := Compare(
		Snapshot{OverallPressurePercent: 73},
		Snapshot{OverallPressurePercent: 52},
	)
	requireLine(t, decrease, "Overall pressure changed: 73% -> 52%")
}

func TestCompareNamespacePodCountChange(t *testing.T) {
	t.Parallel()

	summary := Compare(
		Snapshot{Namespaces: map[string]NamespaceSnapshot{"argocd": {PodCount: 2}}},
		Snapshot{Namespaces: map[string]NamespaceSnapshot{"argocd": {PodCount: 6}}},
	)

	requireLine(t, summary, "argocd namespace added +4 pods")
}

func TestCompareNewWorkloadDetection(t *testing.T) {
	t.Parallel()

	summary := Compare(
		Snapshot{Workloads: map[string]WorkloadSnapshot{}},
		Snapshot{Workloads: map[string]WorkloadSnapshot{
			"deployment/argocd/argocd-server": {Kind: "Deployment", Namespace: "argocd", Name: "argocd-server"},
		}},
	)

	requireLine(t, summary, "New workload detected: deployment/argocd-server")
}

func TestCompareNoSignificantChanges(t *testing.T) {
	t.Parallel()

	snapshot := Snapshot{
		OverallPressurePercent: 50,
		CPURequestPercent:      20,
		MemoryRequestPercent:   30,
		PodCapacityPercent:     40,
		TotalPods:              3,
		Namespaces:             map[string]NamespaceSnapshot{"default": {PodCount: 3}},
		Workloads:              map[string]WorkloadSnapshot{"deployment/default/web": {Kind: "Deployment", Namespace: "default", Name: "web"}},
	}

	summary := Compare(snapshot, snapshot)
	if len(summary.Lines) != 1 || summary.Lines[0] != "No significant changes since last snapshot." {
		t.Fatalf("unexpected summary: %+v", summary)
	}
}

func TestFromReportStoresSafeOperationalData(t *testing.T) {
	t.Parallel()

	snapshot := FromReport(collector.CapacityReport{
		OverallPressurePercent: 73,
		CPUUsagePercent:        19,
		MemoryUsagePercent:     20,
		PodCapacityPercent:     72,
		TotalCPURequested:      100,
		TotalMemoryRequested:   420 * 1024 * 1024,
		Namespaces:             []collector.NamespaceSnapshot{{Name: "argocd"}},
		Pods: []collector.PodSnapshot{
			{Namespace: "argocd", RequestedCPU: 100, RequestedMemory: 420 * 1024 * 1024},
		},
		Workloads: []collector.WorkloadSnapshot{
			{Kind: "Deployment", Namespace: "argocd", Name: "argocd-server", Replicas: 1},
		},
	}, time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC))

	if snapshot.TotalPods != 1 || snapshot.Namespaces["argocd"].PodCount != 1 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	if _, ok := snapshot.Workloads["deployment/argocd/argocd-server"]; !ok {
		t.Fatalf("expected workload snapshot, got %+v", snapshot.Workloads)
	}
}

func requireLine(t *testing.T, summary ChangeSummary, want string) {
	t.Helper()

	for _, line := range summary.Lines {
		if line == want {
			return
		}
	}

	t.Fatalf("expected line %q in %+v", want, summary.Lines)
}
