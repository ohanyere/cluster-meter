package render

import (
	"fmt"
	"io"
	"strings"

	"github.com/ohanyere/cluster-meter/internal/ai"
	"github.com/ohanyere/cluster-meter/internal/analyzer"
	"github.com/ohanyere/cluster-meter/internal/collector"
	"github.com/ohanyere/cluster-meter/internal/state"
)

type MeterOptions struct {
	Color bool
}

func Capacity(w io.Writer, report collector.CapacityReport, opts ...MeterOptions) error {
	options := meterOptions(opts...)

	if _, err := fmt.Fprintln(w, sectionHeader("Summary:", options)); err != nil {
		return err
	}

	if _, err := fmt.Fprintln(w, "Cluster connection: success"); err != nil {
		return err
	}

	if report.KubeconfigPath != "" {
		if _, err := fmt.Fprintf(w, "Kubeconfig: %s\n", report.KubeconfigPath); err != nil {
			return err
		}
	}

	if _, err := fmt.Fprintf(w, "Total nodes: %d\n", len(report.Nodes)); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(w, "Ready nodes: %d\n", report.ReadyNodeCount()); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(w, "Total pods: %d\n", len(report.Pods)); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(w, "Namespaces: %d\n", len(report.Namespaces)); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(w, "Deployments: %d\n", len(report.Workloads)); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(w, "Total allocatable CPU: %dm\n", report.TotalAllocatableCPU()); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(w, "Total allocatable memory: %d bytes\n", report.TotalAllocatableMemory()); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(w, "Total allocatable pods: %d\n", report.TotalAllocatablePods()); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(w, "Total requested CPU: %dm\n", report.TotalRequestedCPU()); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(w, "Total requested memory: %d bytes\n", report.TotalRequestedMemory()); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(w, "Resource quotas: %d\n", len(report.ResourceQuotas)); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(w, "Limit ranges: %d\n", len(report.LimitRanges)); err != nil {
		return err
	}

	return nil
}

func RenderMeter(w io.Writer, report collector.CapacityReport, opts ...MeterOptions) error {
	options := meterOptions(opts...)

	if _, err := fmt.Fprintln(w, sectionHeader("Cluster Capacity Meter", options)); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}

	if err := renderMeterSection(w, "Overall Pressure", report.OverallPressurePercent, report.RiskLevel, options); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}

	if err := renderMeterSection(w, "CPU Requests", report.CPUUsagePercent, collector.RiskLevelForUsage(report.CPUUsagePercent), options); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}

	if err := renderMeterSection(w, "Memory Requests", report.MemoryUsagePercent, collector.RiskLevelForUsage(report.MemoryUsagePercent), options); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}

	return renderMeterSection(w, "Pod Capacity", report.PodCapacityPercent, collector.RiskLevelForUsage(report.PodCapacityPercent), options)
}

func WhatChanged(w io.Writer, summary state.ChangeSummary, opts ...MeterOptions) error {
	options := meterOptions(opts...)

	if _, err := fmt.Fprintln(w, sectionHeader("What Changed:", options)); err != nil {
		return err
	}

	lines := summary.Lines
	if len(lines) == 0 {
		lines = []string{"No significant changes since last snapshot."}
	}

	for _, line := range lines {
		if _, err := fmt.Fprintf(w, "* %s\n", colorizeText(line, styleChange, options)); err != nil {
			return err
		}
	}

	return nil
}

func Analysis(w io.Writer, result analyzer.AnalysisResult, opts ...MeterOptions) error {
	options := meterOptions(opts...)

	if _, err := fmt.Fprintln(w, sectionHeader("Cause:", options)); err != nil {
		return err
	}
	if err := renderCauseGroup(w, "Primary", result.Causes, analyzer.CauseTypePrimary, options); err != nil {
		return err
	}
	if err := renderCauseGroup(w, "Impact", result.Causes, analyzer.CauseTypeImpact, options); err != nil {
		return err
	}

	if len(result.Recommendations) == 0 && len(result.Commands) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, sectionHeader("Recommendations:", options)); err != nil {
		return err
	}
	if err := renderList(w, result.Recommendations, styleRecommendation, options); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}

	if _, err := fmt.Fprintln(w, sectionHeader("Suggested Commands:", options)); err != nil {
		return err
	}

	return renderList(w, result.Commands, styleCommand, options)
}

func AIInsights(w io.Writer, insight ai.Insight, opts ...MeterOptions) error {
	options := meterOptions(opts...)

	if _, err := fmt.Fprintln(w, sectionHeader("AI Insights:", options)); err != nil {
		return err
	}

	return renderList(w, insight.Lines, styleAIInsight, options)
}

func renderCauseGroup(w io.Writer, label string, causes []analyzer.Cause, causeType string, options MeterOptions) error {
	var lines []string
	for _, cause := range causes {
		if cause.Type == causeType {
			lines = append(lines, cause.Message)
		}
	}
	if len(lines) == 0 {
		return nil
	}

	if _, err := fmt.Fprintln(w, colorizeText(label+":", causeLabelStyle(causeType), options)); err != nil {
		return err
	}
	return renderList(w, lines, causeMessageStyle(causeType), options)
}

func renderList(w io.Writer, lines []string, style string, options MeterOptions) error {
	if len(lines) == 0 {
		lines = []string{"None"}
	}

	for _, line := range lines {
		if _, err := fmt.Fprintf(w, "* %s\n", colorizeText(line, style, options)); err != nil {
			return err
		}
	}

	return nil
}

func renderMeterSection(w io.Writer, label string, percent float64, riskLevel string, options MeterOptions) error {
	if riskLevel == "" {
		riskLevel = collector.RiskLevelForUsage(percent)
	}

	if _, err := fmt.Fprintln(w, colorizeText(label+":", styleSubtleHeader, options)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "[%s] %.0f%% -> %s\n", styledMeterBar(percent, riskLevel, options), percent, riskLabel(riskLevel, options)); err != nil {
		return err
	}

	return nil
}

func meterBar(percent float64) string {
	const width = 20

	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}

	filled := int(percent/100*width + 0.5)
	if filled > width {
		filled = width
	}

	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

func styledMeterBar(percent float64, riskLevel string, options MeterOptions) string {
	const width = 20

	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}

	filled := int(percent/100*width + 0.5)
	if filled > width {
		filled = width
	}

	filledBar := strings.Repeat("█", filled)
	emptyBar := strings.Repeat("░", width-filled)
	return colorizeText(filledBar, riskStyle(riskLevel), options) + colorizeText(emptyBar, styleMuted, options)
}

func meterOptions(opts ...MeterOptions) MeterOptions {
	if len(opts) == 0 {
		return MeterOptions{}
	}

	return opts[0]
}

func riskLabel(riskLevel string, options MeterOptions) string {
	label := riskLevel
	if options.Color {
		if icon := riskIcon(riskLevel); icon != "" {
			label = icon + " " + label
		}
	}

	return colorizeText(label, riskStyle(riskLevel), options)
}

func sectionHeader(value string, options MeterOptions) string {
	return colorizeText(value, styleSection, options)
}

func colorizeText(value string, style string, options MeterOptions) string {
	if !options.Color {
		return value
	}

	code := styleCode(style)
	if code == "" {
		return value
	}

	return "\x1b[" + code + "m" + value + "\x1b[0m"
}

const (
	styleSection        = "section"
	styleSubtleHeader   = "subtle-header"
	styleMuted          = "muted"
	styleChange         = "change"
	stylePrimaryCause   = "primary-cause"
	styleImpactCause    = "impact-cause"
	styleRecommendation = "recommendation"
	styleCommand        = "command"
	styleAIInsight      = "ai-insight"
	styleHealthy        = "healthy"
	styleModerate       = "moderate"
	styleHigh           = "high"
	styleCritical       = "critical"
)

func styleCode(style string) string {
	switch style {
	case styleSection:
		return "1;36"
	case styleSubtleHeader:
		return "36"
	case styleMuted:
		return "2;37"
	case styleChange:
		return "37"
	case stylePrimaryCause:
		return "1;34"
	case styleImpactCause:
		return "1;33"
	case styleRecommendation:
		return "32"
	case styleCommand:
		return "2;37"
	case styleAIInsight:
		return "35"
	case styleHealthy:
		return "32"
	case styleModerate:
		return "33"
	case styleHigh, styleCritical:
		return "31"
	default:
		return ""
	}
}

func riskStyle(riskLevel string) string {
	switch riskLevel {
	case "HEALTHY":
		return styleHealthy
	case "MODERATE":
		return styleModerate
	case "HIGH":
		return styleHigh
	case "CRITICAL":
		return styleCritical
	default:
		return ""
	}
}

func riskIcon(riskLevel string) string {
	switch riskLevel {
	case "HEALTHY":
		return "✅"
	case "MODERATE":
		return "⚠️"
	case "HIGH", "CRITICAL":
		return "🔥"
	default:
		return ""
	}
}

func causeLabelStyle(causeType string) string {
	if causeType == analyzer.CauseTypeImpact {
		return styleImpactCause
	}

	return stylePrimaryCause
}

func causeMessageStyle(causeType string) string {
	if causeType == analyzer.CauseTypeImpact {
		return styleImpactCause
	}

	return stylePrimaryCause
}
