package alert

import (
	"fmt"
	"io"
	"time"
)

const Bell = "\a"

const DefaultRepeat = 30 * time.Second

type Monitor struct {
	threshold float64
	repeat    time.Duration
	critical  bool
	lastAlert time.Time
	notifier  Notifier
	color     bool
}

func NewMonitor(threshold float64) *Monitor {
	return NewMonitorWithOptions(Options{
		Threshold: threshold,
		Repeat:    DefaultRepeat,
		Notifier:  TerminalBell{},
	})
}

func NewMonitorWithRepeat(threshold float64, repeat time.Duration) *Monitor {
	return NewMonitorWithOptions(Options{
		Threshold: threshold,
		Repeat:    repeat,
		Notifier:  TerminalBell{},
	})
}

func NewMonitorWithNotifier(threshold float64, notifier Notifier) *Monitor {
	return NewMonitorWithOptions(Options{
		Threshold: threshold,
		Repeat:    DefaultRepeat,
		Notifier:  notifier,
	})
}

type Options struct {
	Threshold float64
	Repeat    time.Duration
	Notifier  Notifier
	Color     bool
}

func NewMonitorWithOptions(opts Options) *Monitor {
	repeat := opts.Repeat
	if repeat <= 0 {
		repeat = DefaultRepeat
	}

	notifier := opts.Notifier
	if notifier == nil {
		notifier = TerminalBell{}
	}

	return &Monitor{
		threshold: opts.Threshold,
		repeat:    repeat,
		notifier:  notifier,
		color:     opts.Color,
	}
}

type Notifier interface {
	Trigger(io.Writer) error
}

type TerminalBell struct{}

func (TerminalBell) Trigger(w io.Writer) error {
	_, err := fmt.Fprint(w, Bell)
	return err
}

func (m *Monitor) Evaluate(w io.Writer, pressure float64) error {
	return m.EvaluateAt(w, pressure, time.Now())
}

func (m *Monitor) warningLine(pressure float64) string {
	line := fmt.Sprintf("WARNING: overall pressure %.0f%% is at or above critical threshold %.0f%%", pressure, m.threshold)
	if !m.color {
		return line
	}

	return "\x1b[1;31m🚨 " + line + "\x1b[0m"
}

func (m *Monitor) EvaluateAt(w io.Writer, pressure float64, now time.Time) error {
	isCritical := pressure >= m.threshold
	if isCritical {
		if m.shouldAlert(now) {
			if err := m.notifier.Trigger(w); err != nil {
				return err
			}
			m.lastAlert = now
		}
		if _, err := fmt.Fprintf(w, "%s\n\n", m.warningLine(pressure)); err != nil {
			return err
		}
	}

	m.critical = isCritical
	if !isCritical {
		m.lastAlert = time.Time{}
	}

	return nil
}

func (m *Monitor) shouldAlert(now time.Time) bool {
	if !m.critical {
		return true
	}
	if m.lastAlert.IsZero() {
		return true
	}

	return !now.Before(m.lastAlert.Add(m.repeat))
}
