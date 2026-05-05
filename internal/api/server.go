package api

import (
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/ohanyere/cluster-meter/internal/ai"
	statepkg "github.com/ohanyere/cluster-meter/internal/state"
	"k8s.io/client-go/kubernetes"
)

type Server struct {
	client kubernetes.Interface

	stateFile     string
	noState       bool
	previousState *statepkg.Snapshot
	stateStatus   statepkg.LoadStatus
	stateMu       sync.Mutex

	aiKey         string
	aiCacheFile   string
	aiCacheTTL    time.Duration
	noAICache     bool
	aiTimeout     time.Duration
	lastAIInsight ai.Insight
	lastAIAt      time.Time
	aiMu          sync.Mutex

	now func() time.Time
}

type Options struct {
	Client      kubernetes.Interface
	StateFile   string
	NoState     bool
	AIKey       string
	AICacheFile string
	AICacheTTL  time.Duration
	NoAICache   bool
	AITimeout   time.Duration
	Now         func() time.Time
}

func NewServer(opts Options) (*Server, error) {
	stateFile := opts.StateFile
	if !opts.NoState && stateFile == "" {
		resolved, err := statepkg.DefaultPath()
		if err != nil {
			return nil, err
		}
		stateFile = resolved
	}

	aiCacheFile := opts.AICacheFile
	if !opts.NoAICache && aiCacheFile == "" {
		resolved, err := ai.DefaultCachePath()
		if err != nil {
			return nil, err
		}
		aiCacheFile = resolved
	}

	aiCacheTTL := opts.AICacheTTL
	if aiCacheTTL <= 0 {
		aiCacheTTL = ai.DefaultCacheTTL
	}

	aiTimeout := opts.AITimeout
	if aiTimeout <= 0 {
		aiTimeout = 10 * time.Second
	}

	now := opts.Now
	if now == nil {
		now = time.Now
	}

	server := &Server{
		client:      opts.Client,
		stateFile:   stateFile,
		noState:     opts.NoState,
		aiKey:       opts.AIKey,
		aiCacheFile: aiCacheFile,
		aiCacheTTL:  aiCacheTTL,
		noAICache:   opts.NoAICache,
		aiTimeout:   aiTimeout,
		now:         now,
	}

	if server.aiKey == "" {
		server.aiKey = os.Getenv("GEMINI_API_KEY")
	}

	if err := server.loadInitialState(); err != nil {
		return nil, err
	}

	return server, nil
}

func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(10 * time.Second))

	r.Get("/health", s.handleHealth)
	r.Get("/ready", s.handleReady)
	r.Get("/capacity", s.handleCapacity)

	return r
}

func (s *Server) loadInitialState() error {
	if s.noState {
		return nil
	}

	previous, status, err := statepkg.Load(s.stateFile)
	if err != nil {
		return err
	}
	s.stateStatus = status
	if status == statepkg.LoadStatusLoaded {
		s.previousState = &previous
	}

	return nil
}
