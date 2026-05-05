package render

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ohanyere/cluster-meter/internal/ai"
	"github.com/ohanyere/cluster-meter/internal/analyzer"
	"github.com/ohanyere/cluster-meter/internal/collector"
	"github.com/ohanyere/cluster-meter/internal/state"
)

func TestCapacity(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	err := Capacity(&buf, collector.CapacityReport{
		ConnectionOK:   true,
		KubeconfigPath: "/tmp/config",
		Nodes: []collector.NodeSnapshot{
			{Name: "node-a", Ready: true, AllocatableCPU: 2000, AllocatableMemory: 4096, AllocatablePodCount: 110},
			{Name: "node-b", Ready: false, AllocatableCPU: 1000, AllocatableMemory: 2048, AllocatablePodCount: 50},
		},
		Pods:           []collector.PodSnapshot{{Name: "pod-a", RequestedCPU: 250, RequestedMemory: 128}},
		Namespaces:     []collector.NamespaceSnapshot{{Name: "default"}, {Name: "kube-system"}},
		Workloads:      []collector.WorkloadSnapshot{{Name: "web", Kind: "Deployment"}},
		ResourceQuotas: []collector.ResourceQuotaSnapshot{{Name: "quota"}},
		LimitRanges:    []collector.LimitRangeSnapshot{{Name: "limits"}},
	})
	if err != nil {
		t.Fatalf("Capacity() error = %v", err)
	}

	expected := "Summary:\nCluster connection: success\nKubeconfig: /tmp/config\nTotal nodes: 2\nReady nodes: 1\nTotal pods: 1\nNamespaces: 2\nDeployments: 1\nTotal allocatable CPU: 3000m\nTotal allocatable memory: 6144 bytes\nTotal allocatable pods: 160\nTotal requested CPU: 250m\nTotal requested memory: 128 bytes\nResource quotas: 1\nLimit ranges: 1\n"
	if buf.String() != expected {
		t.Fatalf("unexpected render output:\n%s", buf.String())
	}
}

func TestRenderMeterNoColor(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	err := RenderMeter(&buf, collector.CapacityReport{
		CPUUsagePercent:        19,
		MemoryUsagePercent:     20,
		PodCapacityPercent:     72.7272727273,
		OverallPressurePercent: 72.7272727273,
		RiskLevel:              "MODERATE",
	})
	if err != nil {
		t.Fatalf("RenderMeter() error = %v", err)
	}

	expected := "Cluster Capacity Meter\n\nOverall Pressure:\n[███████████████░░░░░] 73% -> MODERATE\n\nCPU Requests:\n[████░░░░░░░░░░░░░░░░] 19% -> HEALTHY\n\nMemory Requests:\n[████░░░░░░░░░░░░░░░░] 20% -> HEALTHY\n\nPod Capacity:\n[███████████████░░░░░] 73% -> MODERATE\n"
	if buf.String() != expected {
		t.Fatalf("unexpected meter output:\n%s", buf.String())
	}

	if strings.Contains(buf.String(), "\x1b[") {
		t.Fatalf("expected no ANSI color codes, got %q", buf.String())
	}
}

func TestMeterBar(t *testing.T) {
	t.Parallel()

	if got, want := meterBar(50), "██████████░░░░░░░░░░"; got != want {
		t.Fatalf("meterBar(50) = %q, want %q", got, want)
	}

	if got, want := meterBar(150), "████████████████████"; got != want {
		t.Fatalf("meterBar(150) = %q, want %q", got, want)
	}
}

func TestRenderMeterColorCanBeEnabled(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	err := RenderMeter(&buf, collector.CapacityReport{
		CPUUsagePercent:        91,
		MemoryUsagePercent:     80,
		PodCapacityPercent:     40,
		OverallPressurePercent: 91,
		RiskLevel:              "CRITICAL",
	}, MeterOptions{Color: true})
	if err != nil {
		t.Fatalf("RenderMeter() error = %v", err)
	}

	output := buf.String()
	for _, want := range []string{
		"\x1b[1;36mCluster Capacity Meter\x1b[0m",
		"\x1b[31m🔥 CRITICAL\x1b[0m",
		"\x1b[31m🔥 HIGH\x1b[0m",
		"\x1b[32m✅ HEALTHY\x1b[0m",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected color output to contain %q, got %q", want, output)
		}
	}
}

func TestWhatChanged(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	err := WhatChanged(&buf, state.ChangeSummary{
		Lines: []string{
			"Overall pressure changed: 52% -> 73%",
			"New workload detected: deployment/argocd-server",
		},
	})
	if err != nil {
		t.Fatalf("WhatChanged() error = %v", err)
	}

	expected := "What Changed:\n* Overall pressure changed: 52% -> 73%\n* New workload detected: deployment/argocd-server\n"
	if buf.String() != expected {
		t.Fatalf("unexpected output:\n%s", buf.String())
	}
}

func TestAnalysis(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	err := Analysis(&buf, analyzer.AnalysisResult{
		Causes:          []analyzer.Cause{{Type: analyzer.CauseTypePrimary, Message: "Workload argocd-server scaled up (+2 pods)"}, {Type: analyzer.CauseTypeImpact, Message: "Cluster nearing pod capacity limits (82%)"}},
		Recommendations: []string{"Verify autoscaling policies (HPA/Karpenter)"},
		Commands:        []string{"kubectl get hpa -A"},
	})
	if err != nil {
		t.Fatalf("Analysis() error = %v", err)
	}

	expected := "Cause:\nPrimary:\n* Workload argocd-server scaled up (+2 pods)\nImpact:\n* Cluster nearing pod capacity limits (82%)\n\nRecommendations:\n* Verify autoscaling policies (HPA/Karpenter)\n\nSuggested Commands:\n* kubectl get hpa -A\n"
	if buf.String() != expected {
		t.Fatalf("unexpected output:\n%s", buf.String())
	}
}

func TestAnalysisSuppressesEmptyRecommendationsAndCommands(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	err := Analysis(&buf, analyzer.AnalysisResult{
		Causes: []analyzer.Cause{{Type: analyzer.CauseTypePrimary, Message: "No significant operational change detected"}},
	})
	if err != nil {
		t.Fatalf("Analysis() error = %v", err)
	}

	expected := "Cause:\nPrimary:\n* No significant operational change detected\n"
	if buf.String() != expected {
		t.Fatalf("unexpected output:\n%s", buf.String())
	}
}

func TestAnalysisColorOutput(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	err := Analysis(&buf, analyzer.AnalysisResult{
		Causes:          []analyzer.Cause{{Type: analyzer.CauseTypePrimary, Message: "Workload argocd-server scaled up (+2 pods)"}, {Type: analyzer.CauseTypeImpact, Message: "Cluster nearing pod capacity limits (82%)"}},
		Recommendations: []string{"Verify autoscaling policies for namespace 'argocd'"},
		Commands:        []string{"kubectl get pods -n argocd"},
	}, MeterOptions{Color: true})
	if err != nil {
		t.Fatalf("Analysis() error = %v", err)
	}

	output := buf.String()
	for _, want := range []string{
		"\x1b[1;36mCause:\x1b[0m",
		"\x1b[1;34mPrimary:\x1b[0m",
		"\x1b[1;33mImpact:\x1b[0m",
		"\x1b[32mVerify autoscaling policies for namespace 'argocd'\x1b[0m",
		"\x1b[2;37mkubectl get pods -n argocd\x1b[0m",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected color output to contain %q, got %q", want, output)
		}
	}
}

func TestAIInsights(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	err := AIInsights(&buf, ai.Insight{Lines: []string{"Pressure is high", "Run kubectl top pods -A"}})
	if err != nil {
		t.Fatalf("AIInsights() error = %v", err)
	}

	expected := "AI Insights:\n* Pressure is high\n* Run kubectl top pods -A\n"
	if buf.String() != expected {
		t.Fatalf("unexpected output:\n%s", buf.String())
	}
}
