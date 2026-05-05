package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ohanyere/cluster-meter/internal/analyzer"
	"github.com/ohanyere/cluster-meter/internal/collector"
	"github.com/ohanyere/cluster-meter/internal/state"
)

const (
	DefaultModel    = "gemini-2.0-flash"
	DefaultCacheTTL = 10 * time.Minute
	defaultEndpoint = "https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent"
)

var ErrMissingAPIKey = errors.New("GEMINI_API_KEY is not set")
var ErrRateLimited = errors.New("Gemini rate limit reached")

type Insight struct {
	Lines []string
}

type Input struct {
	Report   collector.CapacityReport
	Changes  state.ChangeSummary
	Analysis analyzer.AnalysisResult
}

type Client struct {
	APIKey     string
	Model      string
	Endpoint   string
	HTTPClient *http.Client
}

func NewClient(apiKey string) *Client {
	return &Client{
		APIKey: apiKey,
		Model:  DefaultModel,
		HTTPClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (c *Client) Generate(ctx context.Context, input Input) (Insight, error) {
	if strings.TrimSpace(c.APIKey) == "" {
		return Insight{}, ErrMissingAPIKey
	}

	body := generateContentRequest{
		Contents: []content{{
			Role: "user",
			Parts: []part{{
				Text: buildPrompt(input),
			}},
		}},
		GenerationConfig: generationConfig{
			Temperature:     0.2,
			MaxOutputTokens: 512,
		},
	}

	data, err := json.Marshal(body)
	if err != nil {
		return Insight{}, fmt.Errorf("marshal Gemini request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(), bytes.NewReader(data))
	if err != nil {
		return Insight{}, fmt.Errorf("create Gemini request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", c.APIKey)

	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return Insight{}, fmt.Errorf("call Gemini API: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return Insight{}, fmt.Errorf("read Gemini response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		if resp.StatusCode == http.StatusTooManyRequests {
			return Insight{}, ErrRateLimited
		}
		return Insight{}, fmt.Errorf("Gemini API returned %s", resp.Status)
	}

	var parsed generateContentResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return Insight{}, fmt.Errorf("decode Gemini response: %w", err)
	}

	text := parsed.Text()
	if strings.TrimSpace(text) == "" {
		return Insight{}, errors.New("Gemini response did not include text")
	}

	return Insight{Lines: normalizeLines(text)}, nil
}

func Fallback(err error) Insight {
	if errors.Is(err, ErrMissingAPIKey) {
		return Insight{Lines: []string{"AI disabled: GEMINI_API_KEY is not set."}}
	}
	if errors.Is(err, ErrRateLimited) {
		return Insight{Lines: []string{"Gemini rate limit reached. Retry later or run without --ai."}}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return Insight{Lines: []string{"AI insights timed out. Rule-based recommendations are still available."}}
	}

	return Insight{Lines: []string{fmt.Sprintf("AI insights unavailable: %v", err)}}
}

type CacheEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Lines     []string  `json:"lines"`
}

func DefaultCachePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}

	return filepath.Join(home, ".cluster-meter", "ai-cache.json"), nil
}

func LoadCache(path string, ttl time.Duration, now time.Time) (Insight, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Insight{}, false, nil
		}

		return Insight{}, false, fmt.Errorf("read AI cache: %w", err)
	}

	var entry CacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return Insight{}, false, nil
	}
	if len(entry.Lines) == 0 || now.Sub(entry.Timestamp) > ttl {
		return Insight{}, false, nil
	}

	return Insight{Lines: entry.Lines}, true, nil
}

func SaveCache(path string, insight Insight, now time.Time) error {
	if len(insight.Lines) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create AI cache directory: %w", err)
	}

	data, err := json.MarshalIndent(CacheEntry{
		Timestamp: now.UTC(),
		Lines:     insight.Lines,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal AI cache: %w", err)
	}
	data = append(data, '\n')

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return fmt.Errorf("write AI cache: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("replace AI cache: %w", err)
	}

	return nil
}

func buildPrompt(input Input) string {
	var b strings.Builder
	b.WriteString("You are assisting a Kubernetes platform engineer using cluster-meter.\n")
	b.WriteString("Explain the situation briefly, suggest actionable fixes, and provide kubectl commands where applicable.\n")
	b.WriteString("Keep the response concise, CLI-friendly, and safe. Do not suggest destructive commands or auto-execution.\n")
	b.WriteString("Return 3-6 bullet lines total. Prefix each line with '- '.\n\n")

	report := input.Report
	fmt.Fprintf(&b, "Current capacity metrics:\n")
	fmt.Fprintf(&b, "- Overall pressure: %.0f%% (%s)\n", report.OverallPressurePercent, report.RiskLevel)
	fmt.Fprintf(&b, "- CPU requests: %.0f%%\n", report.CPUUsagePercent)
	fmt.Fprintf(&b, "- Memory requests: %.0f%%\n", report.MemoryUsagePercent)
	fmt.Fprintf(&b, "- Pod capacity: %.0f%%\n", report.PodCapacityPercent)
	fmt.Fprintf(&b, "- Total pods: %d\n", len(report.Pods))
	fmt.Fprintf(&b, "- Total requested CPU: %dm\n", report.TotalCPURequested)
	fmt.Fprintf(&b, "- Total requested memory: %d bytes\n", report.TotalMemoryRequested)
	fmt.Fprintf(&b, "- Nodes: %d total, %d ready\n", len(report.Nodes), report.ReadyNodeCount())
	fmt.Fprintf(&b, "- Namespaces: %d\n", len(report.Namespaces))
	fmt.Fprintf(&b, "- Deployments: %d\n\n", len(report.Workloads))

	b.WriteString("What Changed:\n")
	writeLines(&b, input.Changes.Lines)
	b.WriteString("\nCause analysis:\n")
	for _, cause := range input.Analysis.Causes {
		fmt.Fprintf(&b, "- %s: %s\n", cause.Type, cause.Message)
	}
	b.WriteString("\nDeterministic recommendations:\n")
	writeLines(&b, input.Analysis.Recommendations)
	b.WriteString("\nSuggested commands:\n")
	writeLines(&b, input.Analysis.Commands)

	return b.String()
}

func writeLines(b *strings.Builder, lines []string) {
	if len(lines) == 0 {
		b.WriteString("- None\n")
		return
	}
	for _, line := range lines {
		fmt.Fprintf(b, "- %s\n", line)
	}
}

func normalizeLines(text string) []string {
	raw := strings.Split(text, "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "-")
		line = strings.TrimPrefix(line, "*")
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}

	return lines
}

func (c *Client) endpoint() string {
	if c.Endpoint != "" {
		return c.Endpoint
	}
	model := c.Model
	if model == "" {
		model = DefaultModel
	}

	return fmt.Sprintf(defaultEndpoint, model)
}

type generateContentRequest struct {
	Contents         []content        `json:"contents"`
	GenerationConfig generationConfig `json:"generationConfig"`
}

type generationConfig struct {
	Temperature     float64 `json:"temperature"`
	MaxOutputTokens int     `json:"maxOutputTokens"`
}

type content struct {
	Role  string `json:"role,omitempty"`
	Parts []part `json:"parts"`
}

type part struct {
	Text string `json:"text"`
}

type generateContentResponse struct {
	Candidates []struct {
		Content content `json:"content"`
	} `json:"candidates"`
}

func (r generateContentResponse) Text() string {
	var b strings.Builder
	for _, candidate := range r.Candidates {
		for _, part := range candidate.Content.Parts {
			b.WriteString(part.Text)
		}
	}

	return b.String()
}
