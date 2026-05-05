package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ohanyere/cluster-meter/internal/analyzer"
	"github.com/ohanyere/cluster-meter/internal/collector"
	"github.com/ohanyere/cluster-meter/internal/state"
)

func TestGenerateMissingAPIKey(t *testing.T) {
	t.Parallel()

	_, err := NewClient("").Generate(context.Background(), Input{})
	if err == nil {
		t.Fatal("expected missing API key error")
	}

	if got := Fallback(err).Lines[0]; got != "AI disabled: GEMINI_API_KEY is not set." {
		t.Fatalf("unexpected fallback: %q", got)
	}
}

func TestGenerateCallsGeminiAndParsesText(t *testing.T) {
	t.Parallel()

	var gotKey string
	var gotPrompt string
	client := NewClient("test-key")
	client.Endpoint = "https://example.test/generate"
	client.HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotKey = r.Header.Get("x-goog-api-key")

		var req generateContentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		gotPrompt = req.Contents[0].Parts[0].Text

		return jsonResponse(http.StatusOK, `{"candidates":[{"content":{"parts":[{"text":"- Explain pressure\n- Run kubectl top pods -A"}]}}]}`), nil
	})}

	insight, err := client.Generate(context.Background(), Input{
		Report: collector.CapacityReport{
			OverallPressurePercent: 81,
			RiskLevel:              "HIGH",
			CPUUsagePercent:        70,
			MemoryUsagePercent:     60,
			PodCapacityPercent:     81,
		},
		Changes: state.ChangeSummary{Lines: []string{"Pod capacity changed: 70% -> 81%"}},
		Analysis: analyzer.AnalysisResult{
			Causes:   []analyzer.Cause{{Type: analyzer.CauseTypeImpact, Message: "Cluster nearing pod capacity limits (81%)"}},
			Commands: []string{"kubectl get pods -A"},
		},
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	if gotKey != "test-key" {
		t.Fatalf("x-goog-api-key = %q, want test-key", gotKey)
	}
	if !strings.Contains(gotPrompt, "Current capacity metrics") || !strings.Contains(gotPrompt, "Pod capacity changed") {
		t.Fatalf("prompt missing expected context:\n%s", gotPrompt)
	}
	if len(insight.Lines) != 2 || insight.Lines[0] != "Explain pressure" || insight.Lines[1] != "Run kubectl top pods -A" {
		t.Fatalf("unexpected insight: %+v", insight)
	}
}

func TestGenerateAPIFailureReturnsError(t *testing.T) {
	t.Parallel()

	client := NewClient("test-key")
	client.Endpoint = "https://example.test/generate"
	client.HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusInternalServerError, `{"error":"nope"}`), nil
	})}

	_, err := client.Generate(context.Background(), Input{})
	if err == nil {
		t.Fatal("expected API error")
	}
	if !strings.Contains(Fallback(err).Lines[0], "AI insights unavailable") {
		t.Fatalf("unexpected fallback: %+v", Fallback(err))
	}
}

func TestGenerateRateLimitFallback(t *testing.T) {
	t.Parallel()

	client := NewClient("test-key")
	client.Endpoint = "https://example.test/generate"
	client.HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusTooManyRequests, `{"error":"rate limited"}`), nil
	})}

	_, err := client.Generate(context.Background(), Input{})
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("Generate() error = %v, want ErrRateLimited", err)
	}

	if got := Fallback(err).Lines[0]; got != "Gemini rate limit reached. Retry later or run without --ai." {
		t.Fatalf("unexpected fallback: %q", got)
	}
}

func TestLoadCacheHit(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "ai-cache.json")
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	if err := SaveCache(path, Insight{Lines: []string{"cached insight"}}, now.Add(-time.Minute)); err != nil {
		t.Fatalf("SaveCache() error = %v", err)
	}

	insight, ok, err := LoadCache(path, 10*time.Minute, now)
	if err != nil {
		t.Fatalf("LoadCache() error = %v", err)
	}
	if !ok {
		t.Fatal("expected cache hit")
	}
	if len(insight.Lines) != 1 || insight.Lines[0] != "cached insight" {
		t.Fatalf("unexpected cached insight: %+v", insight)
	}
}

func TestLoadCacheExpired(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "ai-cache.json")
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	if err := SaveCache(path, Insight{Lines: []string{"stale insight"}}, now.Add(-11*time.Minute)); err != nil {
		t.Fatalf("SaveCache() error = %v", err)
	}

	_, ok, err := LoadCache(path, 10*time.Minute, now)
	if err != nil {
		t.Fatalf("LoadCache() error = %v", err)
	}
	if ok {
		t.Fatal("expected expired cache miss")
	}
}

func TestGenerateTimeoutFallback(t *testing.T) {
	t.Parallel()

	client := NewClient("test-key")
	client.Endpoint = "https://example.test/generate"
	client.HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}

	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()

	_, err := client.Generate(ctx, Input{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Generate() error = %v, want deadline exceeded", err)
	}
	if got := Fallback(err).Lines[0]; got != "AI insights timed out. Rule-based recommendations are still available." {
		t.Fatalf("unexpected fallback: %q", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}
}
