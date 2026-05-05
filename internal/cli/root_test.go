package cli

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/ohanyere/cluster-meter/internal/alert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

func TestVersionCommand(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer

	cmd := NewRootCommand(RootOptions{
		Version: "0.1.0",
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Stdout:  &stdout,
		Stderr:  io.Discard,
	})
	cmd.SetArgs([]string{"version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute command: %v", err)
	}

	if got, want := stdout.String(), "cluster-meter 0.1.0\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestCapacityCommandRejectsInvalidWatchInterval(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand(RootOptions{
		Version: "test",
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Stdout:  io.Discard,
		Stderr:  io.Discard,
	})
	cmd.SetArgs([]string{"capacity", "--watch", "--interval", "0s"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected invalid interval error")
	}

	if !strings.Contains(err.Error(), "watch interval must be greater than zero") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCapacityCommandRejectsInvalidAlertRepeatWhenAlertEnabled(t *testing.T) {
	t.Parallel()

	cmd := NewRootCommand(RootOptions{
		Version: "test",
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Stdout:  io.Discard,
		Stderr:  io.Discard,
	})
	cmd.SetArgs([]string{"capacity", "--alert", "--alert-repeat", "0s"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected invalid alert repeat error")
	}

	if !strings.Contains(err.Error(), "alert repeat must be greater than zero") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCapacityRunnerWatchReturnsNilOnCanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var stdout bytes.Buffer
	runner := capacityRunner{
		stdout: &stdout,
	}

	if err := runner.watch(ctx, time.Second); err != nil {
		t.Fatalf("watch() error = %v", err)
	}

	if stdout.Len() != 0 {
		t.Fatalf("expected no output for pre-canceled watch context, got %q", stdout.String())
	}
}

func TestCapacityRunnerWatchRenderUsesFullClearSequence(t *testing.T) {
	t.Parallel()

	client := fake.NewSimpleClientset(
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
			Status: corev1.NodeStatus{
				Allocatable: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("1Gi"),
					corev1.ResourcePods:   resource.MustParse("10"),
				},
			},
		},
	)

	var stdout bytes.Buffer
	runner := capacityRunner{
		client:  client,
		stdout:  &stdout,
		noState: true,
	}

	if err := runner.renderOnce(context.Background(), true); err != nil {
		t.Fatalf("renderOnce() error = %v", err)
	}

	if !strings.HasPrefix(stdout.String(), clearTerminalSequence) {
		t.Fatalf("expected output to start with clear sequence %q, got %q", clearTerminalSequence, stdout.String())
	}
}

func TestCapacityRunnerAlertDoesNotSpam(t *testing.T) {
	t.Parallel()

	client := fake.NewSimpleClientset(
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
			Status: corev1.NodeStatus{
				Allocatable: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("1Gi"),
					corev1.ResourcePods:   resource.MustParse("1"),
				},
			},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "pod-a", Namespace: "default"},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "app"}},
			},
		},
	)

	var stdout bytes.Buffer
	runner := capacityRunner{
		client:            client,
		stdout:            &stdout,
		alert:             true,
		criticalThreshold: 85,
		noState:           true,
	}
	monitor := alert.NewMonitor(85)

	if err := runner.renderOnceWithAlert(context.Background(), false, monitor); err != nil {
		t.Fatalf("renderOnceWithAlert() error = %v", err)
	}
	if err := runner.renderOnceWithAlert(context.Background(), false, monitor); err != nil {
		t.Fatalf("renderOnceWithAlert() error = %v", err)
	}

	output := stdout.String()
	if got := strings.Count(output, alert.Bell); got != 1 {
		t.Fatalf("expected one terminal bell, got %d in %q", got, output)
	}
	if got := strings.Count(output, "WARNING:"); got != 2 {
		t.Fatalf("expected warning on every critical render, got %d in %q", got, output)
	}
}

func TestCapacityCommandUsesClientAndRendersSummary(t *testing.T) {
	t.Setenv("KUBECONFIG", "/tmp/from-env")

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
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("2Gi"),
					corev1.ResourcePods:   resource.MustParse("50"),
				},
				Conditions: []corev1.NodeCondition{
					{Type: corev1.NodeReady, Status: corev1.ConditionFalse},
				},
			},
		},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "default"}},
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
		&corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Name: "quota", Namespace: "default"}},
		&corev1.LimitRange{ObjectMeta: metav1.ObjectMeta{Name: "limits", Namespace: "default"}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default"}},
	)

	var stdout bytes.Buffer

	cmd := NewRootCommand(RootOptions{
		Version: "test",
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Stdout:  &stdout,
		Stderr:  io.Discard,
		ClientFactory: func(_ context.Context, kubeconfigPath string) (kubernetes.Interface, error) {
			if kubeconfigPath != "/tmp/from-env" {
				t.Fatalf("expected env kubeconfig path, got %q", kubeconfigPath)
			}
			return client, nil
		},
	})
	cmd.SetArgs([]string{"capacity", "--no-color", "--no-state"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute command: %v", err)
	}

	output := stdout.String()
	for _, want := range []string{
		"Cluster connection: success",
		"Kubeconfig: /tmp/from-env",
		"Total nodes: 2",
		"Ready nodes: 1",
		"Total pods: 1",
		"Namespaces: 1",
		"Deployments: 1",
		"Total allocatable CPU: 3000m",
		"Total requested CPU: 250m",
		"Resource quotas: 1",
		"Limit ranges: 1",
		"Cluster Capacity Meter",
		"Overall Pressure:",
		"CPU Requests:",
		"Memory Requests:",
		"Pod Capacity:",
		"What Changed:",
		"Cause:",
		"Recommendations:",
		"Suggested Commands:",
		"Summary:",
		"State tracking disabled.",
		"HEALTHY",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected output to contain %q, got %q", want, output)
		}
	}

	if strings.Contains(output, "\x1b[") {
		t.Fatalf("expected --no-color output to omit ANSI color codes, got %q", output)
	}
}

func TestCapacityCommandAIFallbackWhenKeyMissing(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("KUBECONFIG", "/tmp/from-env")

	client := fake.NewSimpleClientset(
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "node-a"},
			Status: corev1.NodeStatus{
				Allocatable: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("1Gi"),
					corev1.ResourcePods:   resource.MustParse("10"),
				},
			},
		},
	)

	var stdout bytes.Buffer
	cmd := NewRootCommand(RootOptions{
		Version: "test",
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Stdout:  &stdout,
		Stderr:  io.Discard,
		ClientFactory: func(_ context.Context, _ string) (kubernetes.Interface, error) {
			return client, nil
		},
	})
	cmd.SetArgs([]string{"capacity", "--ai", "--no-ai-cache", "--no-color", "--no-state"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute command: %v", err)
	}

	output := stdout.String()
	if !strings.Contains(output, "AI Insights:") {
		t.Fatalf("expected AI Insights section, got %q", output)
	}
	if !strings.Contains(output, "AI disabled: GEMINI_API_KEY is not set.") {
		t.Fatalf("expected missing key fallback, got %q", output)
	}
}
