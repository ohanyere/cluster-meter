package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/ohanyere/cluster-meter/internal/ai"
	"github.com/ohanyere/cluster-meter/internal/alert"
	"github.com/ohanyere/cluster-meter/internal/analyzer"
	"github.com/ohanyere/cluster-meter/internal/collector"
	"github.com/ohanyere/cluster-meter/internal/config"
	"github.com/ohanyere/cluster-meter/internal/render"
	statepkg "github.com/ohanyere/cluster-meter/internal/state"
	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
)

const clearTerminalSequence = "\033[H\033[2J\033[3J"
const minStateSaveInterval = time.Second

func newCapacityCommand(flags *flagValues, deps dependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "capacity",
		Short: "Validate cluster connectivity and list node readiness",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}

			if flags.watch && flags.interval <= 0 {
				return fmt.Errorf("watch interval must be greater than zero")
			}
			if flags.alert && flags.alertRepeat <= 0 {
				return fmt.Errorf("alert repeat must be greater than zero")
			}
			if flags.ai && flags.aiCacheTTL <= 0 {
				return fmt.Errorf("AI cache TTL must be greater than zero")
			}
			if flags.ai && flags.aiTimeout <= 0 {
				return fmt.Errorf("AI timeout must be greater than zero")
			}
			if !flags.noState && flags.stateFile == "" {
				stateFile, err := statepkg.DefaultPath()
				if err != nil {
					return err
				}
				flags.stateFile = stateFile
			}
			aiCacheFile := ""
			if flags.ai && !flags.noAICache {
				aiCachePath, err := ai.DefaultCachePath()
				if err != nil {
					return err
				}
				aiCacheFile = aiCachePath
			}

			cfg, err := config.Load(config.LoadOptions{
				ExplicitKubeconfig: flags.kubeconfig,
				Environment:        config.SystemEnv(),
			})
			if err != nil {
				return fmt.Errorf("load kubeconfig: %w", err)
			}

			deps.logger.Info("loading Kubernetes configuration", slog.String("kubeconfig", cfg.KubeconfigPath))

			clientset, err := deps.clientFactory(ctx, cfg.KubeconfigPath)
			if err != nil {
				return fmt.Errorf("create kubernetes client: %w", err)
			}

			deps.logger.Info(
				"cluster connectivity validated",
				slog.String("kubeconfig", cfg.KubeconfigPath),
			)

			runner := capacityRunner{
				client:            clientset,
				kubeconfigPath:    cfg.KubeconfigPath,
				stdout:            deps.stdout,
				color:             !flags.noColor,
				ai:                flags.ai,
				aiCacheTTL:        flags.aiCacheTTL,
				noAICache:         flags.noAICache,
				aiTimeout:         flags.aiTimeout,
				aiCacheFile:       aiCacheFile,
				alert:             flags.alert,
				alertRepeat:       flags.alertRepeat,
				criticalThreshold: flags.criticalThreshold,
				stateFile:         flags.stateFile,
				noState:           flags.noState,
			}
			if err := runner.loadInitialState(); err != nil {
				return err
			}

			if flags.watch {
				return runner.watch(ctx, flags.interval)
			}

			return runner.renderOnce(ctx, false)
		},
	}

	cmd.Flags().BoolVar(&flags.watch, "watch", false, "Refresh capacity output continuously")
	cmd.Flags().BoolVar(&flags.ai, "ai", false, "Augment deterministic recommendations with Gemini AI insights")
	cmd.Flags().DurationVar(&flags.aiCacheTTL, "ai-cache-ttl", ai.DefaultCacheTTL, "How long to reuse cached AI insights")
	cmd.Flags().BoolVar(&flags.noAICache, "no-ai-cache", false, "Disable AI insight cache")
	cmd.Flags().DurationVar(&flags.aiTimeout, "ai-timeout", 10*time.Second, "Timeout for Gemini AI requests")
	cmd.Flags().DurationVar(&flags.interval, "interval", 5*time.Second, "Watch refresh interval")
	cmd.Flags().BoolVar(&flags.alert, "alert", false, "Enable terminal bell alerts for critical pressure")
	cmd.Flags().DurationVar(&flags.alertRepeat, "alert-repeat", alert.DefaultRepeat, "Minimum time between repeated critical alerts")
	cmd.Flags().Float64Var(&flags.criticalThreshold, "critical-threshold", 90, "Overall pressure threshold for critical alerts")
	cmd.Flags().StringVar(&flags.stateFile, "state-file", "", "Path to the capacity state file")
	cmd.Flags().BoolVar(&flags.noState, "no-state", false, "Disable state persistence and change detection")

	return cmd
}

type capacityRunner struct {
	client            kubernetes.Interface
	kubeconfigPath    string
	stdout            ioWriter
	color             bool
	ai                bool
	aiCacheTTL        time.Duration
	noAICache         bool
	aiTimeout         time.Duration
	aiCacheFile       string
	lastAIInsight     ai.Insight
	lastAIAt          time.Time
	alert             bool
	alertRepeat       time.Duration
	criticalThreshold float64
	stateFile         string
	noState           bool
	previousState     *statepkg.Snapshot
	stateStatus       statepkg.LoadStatus
	lastStateSave     time.Time
}

type ioWriter interface {
	Write([]byte) (int, error)
}

func (r *capacityRunner) watch(ctx context.Context, interval time.Duration) error {
	if err := ctx.Err(); err != nil {
		if isContextCanceled(err) {
			return nil
		}

		return err
	}

	monitor := alert.NewMonitorWithOptions(alert.Options{
		Threshold: r.criticalThreshold,
		Repeat:    r.alertRepeat,
		Color:     r.color,
	})

	if err := r.renderOnceWithAlert(ctx, true, monitor); err != nil {
		if isContextCanceled(err) {
			return nil
		}

		return err
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := r.renderOnceWithAlert(ctx, true, monitor); err != nil {
				if isContextCanceled(err) {
					return nil
				}

				return err
			}
		}
	}
}

func (r *capacityRunner) renderOnce(ctx context.Context, clear bool) error {
	return r.renderOnceWithAlert(ctx, clear, nil)
}

func (r *capacityRunner) renderOnceWithAlert(ctx context.Context, clear bool, monitor *alert.Monitor) error {
	report, err := collector.CollectCapacity(ctx, r.client)
	if err != nil {
		return fmt.Errorf("collect capacity: %w", err)
	}
	report.KubeconfigPath = r.kubeconfigPath
	report = collector.ComputeCapacity(report)
	currentState := statepkg.FromReport(report, time.Now())
	previousState := r.previousState
	changeSummary := r.compareState(currentState)
	analysis := analyzer.Analyze(previousState, currentState, changeSummary)
	aiInsight := r.generateAIInsight(ctx, report, changeSummary, analysis)

	if clear {
		if _, err := fmt.Fprint(r.stdout, clearTerminalSequence); err != nil {
			return err
		}
	}

	if r.alert && monitor != nil {
		if err := monitor.Evaluate(r.stdout, report.OverallPressurePercent); err != nil {
			return err
		}
	}

	if err := render.RenderMeter(r.stdout, report, render.MeterOptions{Color: r.color}); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(r.stdout); err != nil {
		return err
	}

	options := render.MeterOptions{Color: r.color}
	if err := render.WhatChanged(r.stdout, changeSummary, options); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(r.stdout); err != nil {
		return err
	}

	if err := render.Analysis(r.stdout, analysis, options); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(r.stdout); err != nil {
		return err
	}

	if r.ai {
		if err := render.AIInsights(r.stdout, aiInsight, options); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(r.stdout); err != nil {
			return err
		}
	}

	return render.Capacity(r.stdout, report, options)
}

func isContextCanceled(err error) bool {
	return errors.Is(err, context.Canceled)
}

func (r *capacityRunner) loadInitialState() error {
	if r.noState {
		return nil
	}

	previous, status, err := statepkg.Load(r.stateFile)
	if err != nil {
		return err
	}
	r.stateStatus = status
	if status == statepkg.LoadStatusLoaded {
		r.previousState = &previous
	}

	return nil
}

func (r *capacityRunner) compareState(current statepkg.Snapshot) statepkg.ChangeSummary {
	if r.noState {
		return statepkg.DisabledSummary()
	}

	var summary statepkg.ChangeSummary
	switch {
	case r.previousState != nil:
		summary = statepkg.Compare(*r.previousState, current)
	case r.stateStatus == statepkg.LoadStatusCorrupt:
		summary = statepkg.CorruptSummary()
	default:
		summary = statepkg.MissingSummary()
	}

	if err := r.saveState(current); err != nil {
		summary.Lines = append(summary.Lines, statepkg.SaveWarningSummary(err).Lines...)
	}

	r.previousState = &current
	r.stateStatus = statepkg.LoadStatusLoaded
	return summary
}

func (r *capacityRunner) saveState(current statepkg.Snapshot) error {
	now := time.Now()
	if !r.lastStateSave.IsZero() && now.Sub(r.lastStateSave) < minStateSaveInterval {
		return nil
	}

	if err := statepkg.Save(r.stateFile, current); err != nil {
		return err
	}
	r.lastStateSave = now
	return nil
}

func (r *capacityRunner) generateAIInsight(ctx context.Context, report collector.CapacityReport, changes statepkg.ChangeSummary, analysis analyzer.AnalysisResult) ai.Insight {
	if !r.ai {
		return ai.Insight{}
	}

	now := time.Now()
	if len(r.lastAIInsight.Lines) > 0 && now.Sub(r.lastAIAt) <= r.aiCacheTTL {
		return r.lastAIInsight
	}

	if !r.noAICache {
		if insight, ok, err := ai.LoadCache(r.aiCacheFile, r.aiCacheTTL, now); err == nil && ok {
			r.lastAIInsight = insight
			r.lastAIAt = now
			return insight
		}
	}

	requestCtx, cancel := context.WithTimeout(ctx, r.aiTimeout)
	defer cancel()
	client := ai.NewClient(os.Getenv("GEMINI_API_KEY"))
	client.HTTPClient.Timeout = r.aiTimeout
	insight, err := client.Generate(requestCtx, ai.Input{
		Report:   report,
		Changes:  changes,
		Analysis: analysis,
	})
	if err != nil {
		insight = ai.Fallback(err)
		r.lastAIInsight = insight
		r.lastAIAt = now
		return insight
	}

	if !r.noAICache {
		_ = ai.SaveCache(r.aiCacheFile, insight, now)
	}

	r.lastAIInsight = insight
	r.lastAIAt = now
	return insight
}
