package alert

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
)

func TestMonitorAlertsImmediatelyOnCriticalCrossing(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	monitor := NewMonitorWithRepeat(85, 30*time.Second)

	for _, pressure := range []float64{80, 86} {
		if err := monitor.EvaluateAt(&buf, pressure, now); err != nil {
			t.Fatalf("Evaluate() error = %v", err)
		}
	}

	if got := strings.Count(buf.String(), Bell); got != 1 {
		t.Fatalf("expected 1 terminal bell, got %d in %q", got, buf.String())
	}

	if got := strings.Count(buf.String(), "WARNING:"); got != 1 {
		t.Fatalf("expected 1 warning line, got %d in %q", got, buf.String())
	}
}

func TestMonitorDoesNotRepeatBeforeInterval(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	monitor := NewMonitorWithRepeat(85, 30*time.Second)

	for _, at := range []time.Time{now, now.Add(29 * time.Second)} {
		if err := monitor.EvaluateAt(&buf, 90, at); err != nil {
			t.Fatalf("Evaluate() error = %v", err)
		}
	}

	if got := strings.Count(buf.String(), Bell); got != 1 {
		t.Fatalf("expected 1 terminal bell, got %d in %q", got, buf.String())
	}

	if got := strings.Count(buf.String(), "WARNING:"); got != 2 {
		t.Fatalf("expected warning on every critical evaluation, got %d in %q", got, buf.String())
	}
}

func TestMonitorRepeatsAfterInterval(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	monitor := NewMonitorWithRepeat(85, 30*time.Second)

	for _, at := range []time.Time{now, now.Add(30 * time.Second)} {
		if err := monitor.EvaluateAt(&buf, 90, at); err != nil {
			t.Fatalf("Evaluate() error = %v", err)
		}
	}

	if got := strings.Count(buf.String(), Bell); got != 2 {
		t.Fatalf("expected 2 terminal bells, got %d in %q", got, buf.String())
	}
}

func TestMonitorResetsAfterRecovery(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	monitor := NewMonitorWithRepeat(85, 30*time.Second)

	tests := []struct {
		pressure float64
		at       time.Time
	}{
		{pressure: 90, at: now},
		{pressure: 70, at: now.Add(5 * time.Second)},
		{pressure: 90, at: now.Add(6 * time.Second)},
	}

	for _, test := range tests {
		if err := monitor.EvaluateAt(&buf, test.pressure, test.at); err != nil {
			t.Fatalf("Evaluate() error = %v", err)
		}
	}

	if got := strings.Count(buf.String(), Bell); got != 2 {
		t.Fatalf("expected 2 terminal bells, got %d in %q", got, buf.String())
	}
}

func TestMonitorUsesCustomNotifier(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	notifier := recordingNotifier{message: "custom-alert"}
	monitor := NewMonitorWithNotifier(50, notifier)

	if err := monitor.Evaluate(&buf, 51); err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}

	if !strings.Contains(buf.String(), "custom-alert") {
		t.Fatalf("expected custom notifier output, got %q", buf.String())
	}
}

func TestMonitorColorWarning(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	monitor := NewMonitorWithOptions(Options{
		Threshold: 85,
		Repeat:    30 * time.Second,
		Color:     true,
	})

	if err := monitor.EvaluateAt(&buf, 90, time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("EvaluateAt() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "\x1b[1;31m🚨 WARNING: overall pressure 90% is at or above critical threshold 85%\x1b[0m") {
		t.Fatalf("expected styled warning, got %q", output)
	}
}

type recordingNotifier struct {
	message string
}

func (n recordingNotifier) Trigger(w io.Writer) error {
	_, err := w.Write([]byte(n.message))
	return err
}
