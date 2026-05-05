package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/ohanyere/cluster-meter/internal/ai"
	"github.com/ohanyere/cluster-meter/internal/analyzer"
	"github.com/ohanyere/cluster-meter/internal/collector"
	statepkg "github.com/ohanyere/cluster-meter/internal/state"
)

type healthResponse struct {
	Status string `json:"status"`
}

type readyResponse struct {
	Status string `json:"status"`
}

type capacityResponse struct {
	Metrics         metricsResponse `json:"metrics"`
	StateChanges    []string        `json:"stateChanges"`
	Cause           causeResponse   `json:"cause"`
	Recommendations []string        `json:"recommendations"`
	Commands        []string        `json:"commands"`
	AIInsights      []string        `json:"aiInsights,omitempty"`
}

type metricsResponse struct {
	OverallPressurePercent float64 `json:"overallPressurePercent"`
	CPURequestPercent      float64 `json:"cpuRequestPercent"`
	MemoryRequestPercent   float64 `json:"memoryRequestPercent"`
	PodCapacityPercent     float64 `json:"podCapacityPercent"`
	RiskLevel              string  `json:"riskLevel"`
	TotalPods              int     `json:"totalPods"`
	TotalNodes             int     `json:"totalNodes"`
	ReadyNodes             int     `json:"readyNodes"`
	TotalCPURequested      int64   `json:"totalCPURequested"`
	TotalMemoryRequested   int64   `json:"totalMemoryRequested"`
	TotalCPUAllocatable    int64   `json:"totalCPUAllocatable"`
	TotalMemoryAllocatable int64   `json:"totalMemoryAllocatable"`
	TotalPodAllocatable    int64   `json:"totalPodAllocatable"`
	Namespaces             int     `json:"namespaces"`
	Deployments            int     `json:"deployments"`
}

type causeResponse struct {
	Primary []string `json:"primary"`
	Impact  []string `json:"impact"`
}

type errorEnvelope struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{Status: "ok"})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if _, err := collector.CollectCapacity(r.Context(), s.client); err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "NOT_READY", "kubernetes collection failed")
		return
	}

	writeJSON(w, http.StatusOK, readyResponse{Status: "ready"})
}

func (s *Server) handleCapacity(w http.ResponseWriter, r *http.Request) {
	aiEnabled, ok := parseAIQuery(r)
	if !ok {
		writeAPIError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid query parameter: ai")
		return
	}

	report, err := collector.CollectCapacity(r.Context(), s.client)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "COLLECTION_FAILED", "kubernetes collection failed")
		return
	}
	report = collector.ComputeCapacity(report)

	current := statepkg.FromReport(report, s.now())
	previous, changes := s.compareState(current)
	analysis := analyzer.Analyze(previous, current, changes)

	response := capacityResponse{
		Metrics:         metricsFromReport(report),
		StateChanges:    nonNilStrings(changes.Lines),
		Cause:           causesFromAnalysis(analysis),
		Recommendations: nonNilStrings(analysis.Recommendations),
		Commands:        nonNilStrings(analysis.Commands),
	}

	if aiEnabled {
		response.AIInsights = s.generateAIInsights(r.Context(), report, changes, analysis).Lines
	}

	writeJSON(w, http.StatusOK, response)
}

func (s *Server) compareState(current statepkg.Snapshot) (*statepkg.Snapshot, statepkg.ChangeSummary) {
	if s.noState {
		return nil, statepkg.DisabledSummary()
	}

	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	previous := s.previousState
	var summary statepkg.ChangeSummary
	switch {
	case previous != nil:
		summary = statepkg.Compare(*previous, current)
	case s.stateStatus == statepkg.LoadStatusCorrupt:
		summary = statepkg.CorruptSummary()
	default:
		summary = statepkg.MissingSummary()
	}

	if err := statepkg.Save(s.stateFile, current); err != nil {
		summary.Lines = append(summary.Lines, statepkg.SaveWarningSummary(err).Lines...)
	}

	s.previousState = &current
	s.stateStatus = statepkg.LoadStatusLoaded
	return previous, summary
}

func (s *Server) generateAIInsights(ctx context.Context, report collector.CapacityReport, changes statepkg.ChangeSummary, analysis analyzer.AnalysisResult) ai.Insight {
	now := s.now()

	s.aiMu.Lock()
	defer s.aiMu.Unlock()

	if len(s.lastAIInsight.Lines) > 0 && now.Sub(s.lastAIAt) <= s.aiCacheTTL {
		return s.lastAIInsight
	}

	if !s.noAICache {
		if insight, ok, err := ai.LoadCache(s.aiCacheFile, s.aiCacheTTL, now); err == nil && ok {
			s.lastAIInsight = insight
			s.lastAIAt = now
			return insight
		}
	}

	requestCtx, cancel := context.WithTimeout(ctx, s.aiTimeout)
	defer cancel()

	client := ai.NewClient(s.aiKey)
	client.HTTPClient.Timeout = s.aiTimeout
	insight, err := client.Generate(requestCtx, ai.Input{
		Report:   report,
		Changes:  changes,
		Analysis: analysis,
	})
	if err != nil {
		insight = ai.Fallback(err)
		s.lastAIInsight = insight
		s.lastAIAt = now
		return insight
	}

	if !s.noAICache {
		_ = ai.SaveCache(s.aiCacheFile, insight, now)
	}

	s.lastAIInsight = insight
	s.lastAIAt = now
	return insight
}

func metricsFromReport(report collector.CapacityReport) metricsResponse {
	return metricsResponse{
		OverallPressurePercent: report.OverallPressurePercent,
		CPURequestPercent:      report.CPUUsagePercent,
		MemoryRequestPercent:   report.MemoryUsagePercent,
		PodCapacityPercent:     report.PodCapacityPercent,
		RiskLevel:              report.RiskLevel,
		TotalPods:              len(report.Pods),
		TotalNodes:             len(report.Nodes),
		ReadyNodes:             report.ReadyNodeCount(),
		TotalCPURequested:      report.TotalCPURequested,
		TotalMemoryRequested:   report.TotalMemoryRequested,
		TotalCPUAllocatable:    report.TotalCPUAllocatable,
		TotalMemoryAllocatable: report.TotalMemoryAllocatable,
		TotalPodAllocatable:    report.TotalAllocatablePods(),
		Namespaces:             len(report.Namespaces),
		Deployments:            len(report.Workloads),
	}
}

func causesFromAnalysis(analysis analyzer.AnalysisResult) causeResponse {
	response := causeResponse{
		Primary: []string{},
		Impact:  []string{},
	}
	for _, cause := range analysis.Causes {
		switch cause.Type {
		case analyzer.CauseTypePrimary:
			response.Primary = append(response.Primary, cause.Message)
		case analyzer.CauseTypeImpact:
			response.Impact = append(response.Impact, cause.Message)
		}
	}

	return response
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}

	return values
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeAPIError(w http.ResponseWriter, status int, code string, message string) {
	writeJSON(w, status, errorEnvelope{
		Error: apiError{
			Code:    code,
			Message: message,
		},
	})
}

func parseAIQuery(r *http.Request) (bool, bool) {
	value := r.URL.Query().Get("ai")
	switch value {
	case "", "false":
		return false, true
	case "true":
		return true, true
	default:
		return false, false
	}
}
