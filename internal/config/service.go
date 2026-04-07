package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"

	"github.com/ory/lumen/internal/models"
)

// ServerConfig holds per-server configuration.
type ServerConfig struct {
	Backend   string  `koanf:"backend"`
	Host      string  `koanf:"host"`
	Model     string  `koanf:"model"`
	Dims      int     `koanf:"dims"`
	CtxLength int     `koanf:"ctx_length"`
	MinScore  float64 `koanf:"min_score"`
}

// ConfigService wraps koanf and provides typed config access.
type ConfigService struct {
	k          *koanf.Koanf
	mu         sync.RWMutex
	configPath string
	watcher    *fsnotify.Watcher
	stopCh     chan struct{}
}

func defaultsMap() map[string]any {
	return map[string]any{
		"max_chunk_tokens": 512,
		"freshness_ttl":    "60s",
		"reindex_timeout":  "0s",
		"log_level":        "info",
		"servers": []map[string]any{
			{
				"backend": BackendOllama,
				"host":    "http://localhost:11434",
				"model":   models.DefaultOllamaModel,
			},
		},
	}
}

// NewConfigService creates a ConfigService. configPath is the YAML file path
// (empty string means no file). Environment variables are always loaded.
func NewConfigService(configPath string) (*ConfigService, error) {
	k := koanf.New(".")

	// Layer 1: hardcoded defaults
	if err := k.Load(confmap.Provider(defaultsMap(), "."), nil); err != nil {
		return nil, fmt.Errorf("loading defaults: %w", err)
	}

	// Layer 2: YAML config file (optional)
	if configPath != "" {
		if _, err := os.Stat(configPath); err == nil {
			if err := k.Load(file.Provider(configPath), yaml.Parser()); err != nil {
				return nil, fmt.Errorf("loading config file %s: %w", configPath, err)
			}
		}
	}

	// Layer 3: environment variable overrides
	applyEnvOverrides(k)

	svc := &ConfigService{k: k, configPath: configPath}
	if err := svc.validate(); err != nil {
		return nil, err
	}
	return svc, nil
}

// applyEnvOverrides reads legacy env vars and merges them into koanf.
func applyEnvOverrides(k *koanf.Koanf) {
	globals := make(map[string]any)

	if v := os.Getenv("LUMEN_MAX_CHUNK_TOKENS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			globals["max_chunk_tokens"] = n
		}
	}
	if v := os.Getenv("LUMEN_FRESHNESS_TTL"); v != "" {
		globals["freshness_ttl"] = v
	}
	if v := os.Getenv("LUMEN_REINDEX_TIMEOUT"); v != "" {
		globals["reindex_timeout"] = v
	}
	if v := os.Getenv("LUMEN_LOG_LEVEL"); v != "" {
		globals["log_level"] = v
	}
	if len(globals) > 0 {
		_ = k.Load(confmap.Provider(globals, "."), nil)
	}

	// Server env vars → merge into servers[0]
	backend := os.Getenv("LUMEN_BACKEND")
	model := os.Getenv("LUMEN_EMBED_MODEL")
	dims := os.Getenv("LUMEN_EMBED_DIMS")
	ctx := os.Getenv("LUMEN_EMBED_CTX")

	if backend == "" {
		backend = BackendOllama
	}
	var host string
	switch backend {
	case BackendLMStudio:
		host = os.Getenv("LM_STUDIO_HOST")
	default:
		host = os.Getenv("OLLAMA_HOST")
	}

	// Only apply if at least one server env var is explicitly set
	hasOverride := os.Getenv("LUMEN_BACKEND") != "" || model != "" || host != "" || dims != "" || ctx != ""
	if !hasOverride {
		return
	}

	// Read current servers from koanf, merge env into [0]
	var servers []ServerConfig
	_ = k.Unmarshal("servers", &servers)
	if len(servers) == 0 {
		servers = append(servers, ServerConfig{})
	}

	if os.Getenv("LUMEN_BACKEND") != "" {
		servers[0].Backend = backend
	}
	if model != "" {
		servers[0].Model = model
	}
	if host != "" {
		servers[0].Host = host
	}
	if dims != "" {
		if n, err := strconv.Atoi(dims); err == nil {
			servers[0].Dims = n
		}
	}
	if ctx != "" {
		if n, err := strconv.Atoi(ctx); err == nil {
			servers[0].CtxLength = n
		}
	}

	// Re-marshal servers back into koanf
	serverMaps := make([]map[string]any, len(servers))
	for i, s := range servers {
		serverMaps[i] = map[string]any{
			"backend": s.Backend, "host": s.Host, "model": s.Model,
			"dims": s.Dims, "ctx_length": s.CtxLength, "min_score": s.MinScore,
		}
	}
	_ = k.Load(confmap.Provider(map[string]any{"servers": serverMaps}, "."), nil)
}

func (s *ConfigService) MaxChunkTokens() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.k.Int("max_chunk_tokens")
}

func (s *ConfigService) FreshnessTTL() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, _ := time.ParseDuration(s.k.String("freshness_ttl"))
	return d
}

func (s *ConfigService) ReindexTimeout() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, _ := time.ParseDuration(s.k.String("reindex_timeout"))
	return d
}

func (s *ConfigService) LogLevel() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.k.String("log_level")
}

func (s *ConfigService) Servers() []ServerConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var servers []ServerConfig
	_ = s.k.Unmarshal("servers", &servers)
	return servers
}

// serverAt returns the ServerConfig for index i without acquiring s.mu.
// Callers must either hold s.mu before calling this, or call it on a struct
// that has not yet been shared with other goroutines.
func (s *ConfigService) serverAt(i int) (ServerConfig, bool) {
	var servers []ServerConfig
	_ = s.k.Unmarshal("servers", &servers)
	if i < 0 || i >= len(servers) {
		return ServerConfig{}, false
	}
	return servers[i], true
}

// serverDims returns dims for server i without locking (caller must hold lock).
func (s *ConfigService) serverDims(i int) int {
	srv, ok := s.serverAt(i)
	if !ok {
		return 0
	}
	if srv.Dims != 0 {
		return srv.Dims
	}
	if spec, ok := models.KnownModels[srv.Model]; ok {
		return spec.Dims
	}
	return 0
}

func (s *ConfigService) ServerDims(i int) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.serverDims(i)
}

func (s *ConfigService) ServerCtxLength(i int) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	srv, ok := s.serverAt(i)
	if !ok {
		return 0
	}
	if srv.CtxLength != 0 {
		return srv.CtxLength
	}
	if spec, ok := models.KnownModels[srv.Model]; ok {
		return spec.CtxLength
	}
	return 0
}

// ServerMinScore returns min score for server i. Uses unlocked serverDims
// to avoid recursive RLock (which deadlocks under writer contention).
func (s *ConfigService) ServerMinScore(i int) float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	srv, ok := s.serverAt(i)
	if !ok {
		return 0
	}
	if srv.MinScore != 0 {
		return srv.MinScore
	}
	if spec, ok := models.KnownModels[srv.Model]; ok && spec.MinScore != 0 {
		return spec.MinScore
	}
	return models.DimensionAwareMinScore(s.serverDims(i))
}

func (s *ConfigService) ServersForModel(model string) ([]int, error) {
	servers := s.Servers()
	var indices []int
	for i, srv := range servers {
		if srv.Model == model {
			indices = append(indices, i)
		}
	}
	if len(indices) == 0 {
		return nil, fmt.Errorf("no server configured for model %q", model)
	}
	return indices, nil
}

// validate checks the current configuration for correctness.
// Must be called either on a ConfigService that has not yet been shared with
// other goroutines, or on a temporary ConfigService (as in reload). Must not
// be called while holding s.mu — it acquires RLock via Servers().
func (s *ConfigService) validate() error {
	servers := s.Servers()
	if len(servers) == 0 {
		return fmt.Errorf("config: servers list is empty")
	}
	for i, srv := range servers {
		if srv.Backend == "" {
			return fmt.Errorf("config: servers[%d]: backend is required", i)
		}
		if srv.Backend != BackendOllama && srv.Backend != BackendLMStudio {
			return fmt.Errorf("config: servers[%d]: unknown backend %q", i, srv.Backend)
		}
		if srv.Model == "" {
			return fmt.Errorf("config: servers[%d]: model is required", i)
		}
		if srv.Host == "" {
			return fmt.Errorf("config: servers[%d]: host is required", i)
		}
		u, err := url.Parse(srv.Host)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("config: servers[%d]: host %q must be a valid http/https URL", i, srv.Host)
		}
		if s.serverDims(i) == 0 {
			return fmt.Errorf("config: servers[%d]: cannot resolve dims for model %q — set dims explicitly", i, srv.Model)
		}
	}
	return nil
}

// Watch starts watching the config file for changes (MCP server only).
func (s *ConfigService) Watch() error {
	if s.configPath == "" {
		return nil
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("creating watcher: %w", err)
	}
	// Watch the directory (handles file renames/recreates)
	dir := filepath.Dir(s.configPath)
	if err := w.Add(dir); err != nil {
		_ = w.Close()
		return fmt.Errorf("watching %s: %w", dir, err)
	}
	s.mu.Lock()
	s.stopCh = make(chan struct{})
	s.watcher = w
	s.mu.Unlock()
	go s.watchLoop()
	return nil
}

// Stop stops the file watcher. Must be called from the same goroutine as Watch,
// or after Watch returns.
func (s *ConfigService) Stop() {
	s.mu.RLock()
	stopCh := s.stopCh
	watcher := s.watcher
	s.mu.RUnlock()
	if stopCh != nil {
		close(stopCh)
	}
	if watcher != nil {
		_ = watcher.Close()
	}
}

func (s *ConfigService) watchLoop() {
	for {
		select {
		case <-s.stopCh:
			return
		case event, ok := <-s.watcher.Events:
			if !ok {
				return
			}
			if filepath.Base(event.Name) != filepath.Base(s.configPath) {
				continue
			}
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
				s.reload()
			}
		case _, ok := <-s.watcher.Errors:
			if !ok {
				return
			}
		}
	}
}

func (s *ConfigService) reload() {
	k := koanf.New(".")
	_ = k.Load(confmap.Provider(defaultsMap(), "."), nil)

	if s.configPath != "" {
		if _, err := os.Stat(s.configPath); err == nil {
			if err := k.Load(file.Provider(s.configPath), yaml.Parser()); err != nil {
				return // keep previous config
			}
		}
	}

	applyEnvOverrides(k)

	tmp := &ConfigService{k: k}
	if err := tmp.validate(); err != nil {
		return // keep previous config on validation error
	}

	s.mu.Lock()
	s.k = k
	s.mu.Unlock()
}
