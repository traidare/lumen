# Config File and Multi-Server Failover Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development
> (if subagents available) or superpowers:executing-plans to implement this plan.
> Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace env-var-only config with a YAML config file (koanf) and add
multi-server embedding failover with health checks and hot reload.

**Architecture:** koanf/v2 wraps layered config (defaults < YAML file < env
vars) in a `ConfigService` passed as dependency. `FailoverEmbedder` wraps
multiple backend embedders with ordered health-check failover. Hot reload via
file watcher in MCP server mode only. Model specs live in `internal/models/` to
avoid circular imports between `config` and `embedder`.

**Tech Stack:** koanf/v2, koanf YAML provider, koanf env provider, fsnotify
(via koanf watcher), httptest (for failover tests)

**Spec:** `docs/superpowers/specs/2026-04-07-config-file-and-multi-server-failover.md`

---

## File Structure

**Import graph**: `internal/models/` is a leaf package. Both `config` and
`embedder` import it. `embedder/failover.go` imports `config` for
`ConfigService`. `config` never imports `embedder`. No cycles.

| Action | Path | Responsibility |
|--------|------|----------------|
| Create | `internal/models/models.go` | ModelSpec, KnownModels, ModelAliases, defaults, DimensionAwareMinScore (extracted from embedder/models.go) |
| Create | `internal/models/models_test.go` | Model tests (moved from embedder/models_test.go) |
| Create | `internal/config/service.go` | ConfigService, constructor, accessors, validation, Watch/Stop, DefaultConfigPath |
| Create | `internal/config/service_test.go` | Unit tests: layering, env mapping, validation, KnownModels fallback, ServersForModel |
| Create | `internal/config/reload_test.go` | Integration tests: hot reload with real files, watcher, race detector |
| Create | `internal/config/compat_test.go` | Backward compat tests vs current Load() |
| Create | `internal/embedder/failover.go` | FailoverEmbedder implementing Embedder (imports config) |
| Create | `internal/embedder/failover_test.go` | Integration tests with httptest servers |
| Modify | `internal/embedder/embedder.go` | Add EmbedError type |
| Modify | `internal/embedder/ollama.go` | Return *EmbedError on non-200 |
| Modify | `internal/embedder/lmstudio.go` | Return *EmbedError on non-200 |
| Modify | `internal/embedder/models.go` | Re-export from internal/models for backward compat |
| Modify | `internal/config/config.go` | Add XDGConfigDir, DefaultConfigPath |
| Modify | `cmd/embedder.go` | Return FailoverEmbedder |
| Modify | `cmd/index.go` | Replace config.Load() with ConfigService |
| Modify | `cmd/search.go` | Replace config.Load() with ConfigService |
| Modify | `cmd/search_test.go` | Replace config.Load() |
| Modify | `cmd/stdio.go` | Replace config.Config with ConfigService, add Watch/Stop |
| Modify | `cmd/stdio_test.go` | Replace config.Config{} literals |
| Modify | `cmd/hook.go` | Replace config.Load() |
| Modify | `cmd/hook_test.go` | Replace config.Load() calls |
| Modify | `go.mod` | Add koanf/v2 and providers |

---

## Chunk 1: Foundation

### Task 1: Add koanf dependency

**Files:**
- Modify: `go.mod`

- [ ] **Step 1: Add koanf and providers**

```bash
cd /Users/aeneas/workspace/go/agent-index-go
go get github.com/knadh/koanf/v2@latest
go get github.com/knadh/koanf/providers/confmap@latest
go get github.com/knadh/koanf/providers/file@latest
go get github.com/knadh/koanf/parsers/yaml@latest
go get github.com/fsnotify/fsnotify@latest
```

- [ ] **Step 2: Tidy and verify**

```bash
go mod tidy && go build ./...
```

Expected: builds cleanly.

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: add koanf/v2 config library with YAML, env, confmap providers and fsnotify"
```

---

### Task 2: Extract internal/models package

**Files:**
- Create: `internal/models/models.go`
- Create: `internal/models/models_test.go`
- Modify: `internal/embedder/models.go`
- Modify: `internal/embedder/models_test.go`

This breaks the circular import: `config` needs KnownModels for dims fallback,
`embedder/failover.go` needs ConfigService. By putting model specs in
`internal/models/`, both can import it without importing each other.

- [ ] **Step 1: Create internal/models/models.go**

Move `ModelSpec`, `KnownModels`, `ModelAliases`, `DefaultOllamaModel`,
`DefaultLMStudioModel`, `DefaultModel`, `DefaultMinScore`,
`DimensionAwareMinScore()` from `internal/embedder/models.go` to
`internal/models/models.go`. Change package declaration to `package models`.

- [ ] **Step 2: Update internal/embedder/models.go to re-export**

Replace the moved code with re-exports:

```go
package embedder

import "github.com/ory/lumen/internal/models"

// Re-export from internal/models for backward compatibility.
type ModelSpec = models.ModelSpec

var (
	KnownModels         = models.KnownModels
	ModelAliases        = models.ModelAliases
	DefaultOllamaModel  = models.DefaultOllamaModel
	DefaultLMStudioModel = models.DefaultLMStudioModel
	DefaultModel        = models.DefaultModel
	DefaultMinScore     = models.DefaultMinScore
	DimensionAwareMinScore = models.DimensionAwareMinScore
)
```

- [ ] **Step 3: Move tests to internal/models/models_test.go**

Move model-specific tests from `internal/embedder/models_test.go` to
`internal/models/models_test.go`. Update package to `package models`.
Keep embedder-specific tests (if any) in `internal/embedder/`.

- [ ] **Step 4: Verify everything compiles and tests pass**

```bash
go build ./... && go test ./internal/models/ ./internal/embedder/ -v
```

Expected: All PASS. All existing callers of `embedder.KnownModels` etc.
continue to work via re-exports.

- [ ] **Step 5: Commit**

```bash
git add internal/models/ internal/embedder/models.go internal/embedder/models_test.go
git commit -m "refactor: extract model specs into internal/models to break circular import"
```

---

### Task 3: Add XDGConfigDir and DefaultConfigPath

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

- [ ] **Step 1: Write failing test**

In `internal/config/config_test.go`, add:

```go
func TestXDGConfigDir(t *testing.T) {
	t.Run("uses XDG_CONFIG_HOME when set", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", "/custom/config")
		if got := XDGConfigDir(); got != "/custom/config" {
			t.Errorf("XDGConfigDir() = %q, want %q", got, "/custom/config")
		}
	})
	t.Run("falls back to ~/.config", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", "")
		home, _ := os.UserHomeDir()
		want := filepath.Join(home, ".config")
		if got := XDGConfigDir(); got != want {
			t.Errorf("XDGConfigDir() = %q, want %q", got, want)
		}
	})
}
```

- [ ] **Step 2: Run test — verify fails**

```bash
go test ./internal/config/ -run TestXDGConfigDir -v
```

Expected: FAIL — `XDGConfigDir` undefined.

- [ ] **Step 3: Implement**

In `internal/config/config.go`, after `XDGDataDir()`:

```go
// XDGConfigDir returns XDG_CONFIG_HOME or ~/.config.
func XDGConfigDir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config")
}

// DefaultConfigPath returns the default config file path.
func DefaultConfigPath() string {
	return filepath.Join(XDGConfigDir(), "lumen", "config.yaml")
}
```

- [ ] **Step 4: Run test — verify passes**

```bash
go test ./internal/config/ -run TestXDGConfigDir -v
```

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): add XDGConfigDir and DefaultConfigPath helpers"
```

---

### Task 4: ConfigService with hardcoded defaults

**Files:**
- Create: `internal/config/service.go`
- Create: `internal/config/service_test.go`

- [ ] **Step 1: Write failing test**

Create `internal/config/service_test.go`:

```go
package config

import (
	"testing"
	"time"
)

func TestDefaults_NoFileNoEnv(t *testing.T) {
	svc, err := NewConfigService("")
	if err != nil {
		t.Fatalf("NewConfigService: %v", err)
	}
	if got := svc.MaxChunkTokens(); got != 512 {
		t.Errorf("MaxChunkTokens() = %d, want 512", got)
	}
	if got := svc.FreshnessTTL(); got != 60*time.Second {
		t.Errorf("FreshnessTTL() = %v, want 60s", got)
	}
	if got := svc.ReindexTimeout(); got != 0 {
		t.Errorf("ReindexTimeout() = %v, want 0", got)
	}
	if got := svc.LogLevel(); got != "info" {
		t.Errorf("LogLevel() = %q, want %q", got, "info")
	}
	servers := svc.Servers()
	if len(servers) != 1 {
		t.Fatalf("Servers() len = %d, want 1", len(servers))
	}
	s := servers[0]
	if s.Backend != "ollama" {
		t.Errorf("Backend = %q, want %q", s.Backend, "ollama")
	}
	if s.Host != "http://localhost:11434" {
		t.Errorf("Host = %q, want %q", s.Host, "http://localhost:11434")
	}
	if s.Model != "ordis/jina-embeddings-v2-base-code" {
		t.Errorf("Model = %q, want %q", s.Model, "ordis/jina-embeddings-v2-base-code")
	}
}
```

- [ ] **Step 2: Run test — verify fails**

```bash
go test ./internal/config/ -run TestDefaults_NoFileNoEnv -v
```

- [ ] **Step 3: Implement ConfigService**

Create `internal/config/service.go`. **Note**: imports `internal/models`, NOT
`internal/embedder`.

```go
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

func defaultsMap() map[string]interface{} {
	return map[string]interface{}{
		"max_chunk_tokens": 512,
		"freshness_ttl":    "60s",
		"reindex_timeout":  "0s",
		"log_level":        "info",
		"servers": []map[string]interface{}{
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
	globals := make(map[string]interface{})

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
	serverMaps := make([]map[string]interface{}, len(servers))
	for i, s := range servers {
		serverMaps[i] = map[string]interface{}{
			"backend": s.Backend, "host": s.Host, "model": s.Model,
			"dims": s.Dims, "ctx_length": s.CtxLength, "min_score": s.MinScore,
		}
	}
	_ = k.Load(confmap.Provider(map[string]interface{}{"servers": serverMaps}, "."), nil)
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

// serverDims returns dims for server i without locking (caller must hold lock).
func (s *ConfigService) serverDims(i int) int {
	key := fmt.Sprintf("servers.%d.dims", i)
	if d := s.k.Int(key); d != 0 {
		return d
	}
	model := s.k.String(fmt.Sprintf("servers.%d.model", i))
	if spec, ok := models.KnownModels[model]; ok {
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
	key := fmt.Sprintf("servers.%d.ctx_length", i)
	if c := s.k.Int(key); c != 0 {
		return c
	}
	model := s.k.String(fmt.Sprintf("servers.%d.model", i))
	if spec, ok := models.KnownModels[model]; ok {
		return spec.CtxLength
	}
	return 0
}

// ServerMinScore returns min score for server i. Uses unlocked serverDims
// to avoid recursive RLock (which deadlocks under writer contention).
func (s *ConfigService) ServerMinScore(i int) float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key := fmt.Sprintf("servers.%d.min_score", i)
	if m := s.k.Float64(key); m != 0 {
		return m
	}
	model := s.k.String(fmt.Sprintf("servers.%d.model", i))
	if spec, ok := models.KnownModels[model]; ok && spec.MinScore != 0 {
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
		w.Close()
		return fmt.Errorf("watching %s: %w", dir, err)
	}
	s.stopCh = make(chan struct{})
	s.watcher = w
	go s.watchLoop()
	return nil
}

// Stop stops the file watcher.
func (s *ConfigService) Stop() {
	if s.stopCh != nil {
		close(s.stopCh)
	}
	if s.watcher != nil {
		s.watcher.Close()
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
```

- [ ] **Step 4: Run test — verify passes**

```bash
go test ./internal/config/ -run TestDefaults_NoFileNoEnv -v
```

- [ ] **Step 5: Commit**

```bash
git add internal/config/service.go internal/config/service_test.go
git commit -m "feat(config): add ConfigService with koanf defaults, YAML, env, validation, and hot reload"
```

---

### Task 5: ConfigService unit tests — layering, env mapping, validation

**Files:**
- Modify: `internal/config/service_test.go`

- [ ] **Step 1: Write all ConfigService tests**

Add to `internal/config/service_test.go`:

```go
import (
	"fmt"
	"os"
	"path/filepath"
)

func TestYAMLOverridesDefaults(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfgFile, []byte(`
max_chunk_tokens: 1024
log_level: debug
servers:
  - backend: lmstudio
    host: http://myhost:5555
    model: nomic-ai/nomic-embed-code-GGUF
  - backend: ollama
    host: http://other:11434
    model: ordis/jina-embeddings-v2-base-code
`), 0644)

	svc, err := NewConfigService(cfgFile)
	if err != nil {
		t.Fatalf("NewConfigService: %v", err)
	}
	if got := svc.MaxChunkTokens(); got != 1024 {
		t.Errorf("MaxChunkTokens() = %d, want 1024", got)
	}
	if got := svc.LogLevel(); got != "debug" {
		t.Errorf("LogLevel() = %q, want %q", got, "debug")
	}
	servers := svc.Servers()
	if len(servers) != 2 {
		t.Fatalf("Servers() len = %d, want 2", len(servers))
	}
	if servers[0].Backend != "lmstudio" {
		t.Errorf("server[0].Backend = %q, want %q", servers[0].Backend, "lmstudio")
	}
}

func TestEnvOverridesYAML(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfgFile, []byte(`max_chunk_tokens: 1024
servers:
  - backend: ollama
    host: http://localhost:11434
    model: ordis/jina-embeddings-v2-base-code
`), 0644)
	t.Setenv("LUMEN_MAX_CHUNK_TOKENS", "2048")
	svc, _ := NewConfigService(cfgFile)
	if got := svc.MaxChunkTokens(); got != 2048 {
		t.Errorf("MaxChunkTokens() = %d, want 2048", got)
	}
}

func TestEnvServerMapping_Ollama(t *testing.T) {
	t.Setenv("LUMEN_BACKEND", "ollama")
	t.Setenv("OLLAMA_HOST", "http://custom:9999")
	t.Setenv("LUMEN_EMBED_MODEL", "all-minilm")
	svc, _ := NewConfigService("")
	s := svc.Servers()[0]
	if s.Backend != "ollama" || s.Host != "http://custom:9999" || s.Model != "all-minilm" {
		t.Errorf("got %+v", s)
	}
}

func TestEnvServerMapping_LMStudio(t *testing.T) {
	t.Setenv("LUMEN_BACKEND", "lmstudio")
	t.Setenv("LM_STUDIO_HOST", "http://lms:2222")
	t.Setenv("LUMEN_EMBED_MODEL", "nomic-ai/nomic-embed-code-GGUF")
	svc, _ := NewConfigService("")
	if got := svc.Servers()[0].Host; got != "http://lms:2222" {
		t.Errorf("Host = %q, want %q", got, "http://lms:2222")
	}
}

func TestHostConflict_BothSet(t *testing.T) {
	t.Setenv("LUMEN_BACKEND", "lmstudio")
	t.Setenv("OLLAMA_HOST", "http://ollama:1111")
	t.Setenv("LM_STUDIO_HOST", "http://lms:2222")
	t.Setenv("LUMEN_EMBED_MODEL", "nomic-ai/nomic-embed-code-GGUF")
	svc, _ := NewConfigService("")
	if got := svc.Servers()[0].Host; got != "http://lms:2222" {
		t.Errorf("Host = %q, want %q (lmstudio should win)", got, "http://lms:2222")
	}
}

func TestDimsFallback_KnownModel(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfgFile, []byte(`
servers:
  - backend: ollama
    host: http://localhost:11434
    model: ordis/jina-embeddings-v2-base-code
`), 0644)
	svc, _ := NewConfigService(cfgFile)
	if got := svc.ServerDims(0); got != 768 {
		t.Errorf("ServerDims(0) = %d, want 768", got)
	}
	if got := svc.ServerMinScore(0); got != 0.35 {
		t.Errorf("ServerMinScore(0) = %f, want 0.35", got)
	}
}

func TestDimsExplicit_OverridesKnown(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfgFile, []byte(`
servers:
  - backend: ollama
    host: http://localhost:11434
    model: ordis/jina-embeddings-v2-base-code
    dims: 1024
    min_score: 0.5
`), 0644)
	svc, _ := NewConfigService(cfgFile)
	if got := svc.ServerDims(0); got != 1024 {
		t.Errorf("ServerDims(0) = %d, want 1024", got)
	}
	if got := svc.ServerMinScore(0); got != 0.5 {
		t.Errorf("ServerMinScore(0) = %f, want 0.5", got)
	}
}

func TestDimsUnresolvable_Error(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfgFile, []byte(`
servers:
  - backend: ollama
    host: http://localhost:11434
    model: totally-unknown-model
`), 0644)
	_, err := NewConfigService(cfgFile)
	if err == nil {
		t.Fatal("expected error for unresolvable dims")
	}
}

func TestServersForModel_Filters(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfgFile, []byte(`
servers:
  - backend: ollama
    host: http://a:11434
    model: ordis/jina-embeddings-v2-base-code
  - backend: lmstudio
    host: http://b:1234
    model: nomic-ai/nomic-embed-code-GGUF
  - backend: ollama
    host: http://c:11434
    model: ordis/jina-embeddings-v2-base-code
`), 0644)
	svc, _ := NewConfigService(cfgFile)
	indices, err := svc.ServersForModel("ordis/jina-embeddings-v2-base-code")
	if err != nil {
		t.Fatal(err)
	}
	if len(indices) != 2 || indices[0] != 0 || indices[1] != 2 {
		t.Errorf("got %v, want [0, 2]", indices)
	}
}

func TestServersForModel_NoMatch(t *testing.T) {
	svc, _ := NewConfigService("")
	_, err := svc.ServersForModel("nonexistent-model")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestValidation_MissingBackend(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "config.yaml")
	os.WriteFile(f, []byte(`servers: [{host: "http://x:1", model: m}]`), 0644)
	if _, err := NewConfigService(f); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidation_UnknownBackend(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "config.yaml")
	os.WriteFile(f, []byte(`servers: [{backend: foo, host: "http://x:1", model: m}]`), 0644)
	if _, err := NewConfigService(f); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidation_EmptyServerList(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "config.yaml")
	os.WriteFile(f, []byte(`servers: []`), 0644)
	if _, err := NewConfigService(f); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidation_InvalidHost(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "config.yaml")
	os.WriteFile(f, []byte(`servers: [{backend: ollama, host: not-a-url, model: ordis/jina-embeddings-v2-base-code}]`), 0644)
	if _, err := NewConfigService(f); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidation_MissingModel(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "config.yaml")
	os.WriteFile(f, []byte(`servers: [{backend: ollama, host: "http://x:1"}]`), 0644)
	if _, err := NewConfigService(f); err == nil {
		t.Fatal("expected error")
	}
}
```

- [ ] **Step 2: Run all tests**

```bash
go test ./internal/config/ -run "Test(YAML|Env|Host|Dims|Servers|Validation)" -v
```

Expected: All PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/config/service_test.go
git commit -m "test(config): add ConfigService layering, env mapping, and validation tests"
```

---

## Chunk 2: Hot Reload

### Task 6: Hot reload integration tests

**Files:**
- Create: `internal/config/reload_test.go`

- [ ] **Step 1: Write all reload tests**

Create `internal/config/reload_test.go`. All tests use polling (not
`time.Sleep`) to avoid flakiness:

```go
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func pollUntil(deadline time.Duration, check func() bool) bool {
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		if check() {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func TestReload_FileChange(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfgFile, []byte("max_chunk_tokens: 512\nservers:\n  - backend: ollama\n    host: http://localhost:11434\n    model: ordis/jina-embeddings-v2-base-code\n"), 0644)

	svc, _ := NewConfigService(cfgFile)
	svc.Watch()
	defer svc.Stop()

	os.WriteFile(cfgFile, []byte("max_chunk_tokens: 2048\nservers:\n  - backend: ollama\n    host: http://localhost:11434\n    model: ordis/jina-embeddings-v2-base-code\n"), 0644)

	if !pollUntil(2*time.Second, func() bool { return svc.MaxChunkTokens() == 2048 }) {
		t.Errorf("MaxChunkTokens() = %d, want 2048", svc.MaxChunkTokens())
	}
}

func TestReload_EnvStillWins(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfgFile, []byte("max_chunk_tokens: 512\nservers:\n  - backend: ollama\n    host: http://localhost:11434\n    model: ordis/jina-embeddings-v2-base-code\n"), 0644)
	t.Setenv("LUMEN_MAX_CHUNK_TOKENS", "9999")
	svc, _ := NewConfigService(cfgFile)
	svc.Watch()
	defer svc.Stop()

	os.WriteFile(cfgFile, []byte("max_chunk_tokens: 1024\nservers:\n  - backend: ollama\n    host: http://localhost:11434\n    model: ordis/jina-embeddings-v2-base-code\n"), 0644)

	// Wait for reload to process, then verify env still wins
	time.Sleep(500 * time.Millisecond)
	if got := svc.MaxChunkTokens(); got != 9999 {
		t.Errorf("MaxChunkTokens() = %d, want 9999 (env wins)", got)
	}
}

func TestReload_InvalidRetainsPrevious(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfgFile, []byte("max_chunk_tokens: 512\nservers:\n  - backend: ollama\n    host: http://localhost:11434\n    model: ordis/jina-embeddings-v2-base-code\n"), 0644)
	svc, _ := NewConfigService(cfgFile)
	svc.Watch()
	defer svc.Stop()

	os.WriteFile(cfgFile, []byte("servers: []"), 0644)
	time.Sleep(500 * time.Millisecond)

	if got := svc.MaxChunkTokens(); got != 512 {
		t.Errorf("MaxChunkTokens() = %d, want 512 (retain previous)", got)
	}
}

func TestReload_ServerListChange(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfgFile, []byte("servers:\n  - backend: ollama\n    host: http://a:11434\n    model: ordis/jina-embeddings-v2-base-code\n  - backend: ollama\n    host: http://b:11434\n    model: ordis/jina-embeddings-v2-base-code\n"), 0644)
	svc, _ := NewConfigService(cfgFile)
	svc.Watch()
	defer svc.Stop()

	os.WriteFile(cfgFile, []byte("servers:\n  - backend: ollama\n    host: http://a:11434\n    model: ordis/jina-embeddings-v2-base-code\n  - backend: ollama\n    host: http://b:11434\n    model: ordis/jina-embeddings-v2-base-code\n  - backend: ollama\n    host: http://c:11434\n    model: ordis/jina-embeddings-v2-base-code\n"), 0644)

	if !pollUntil(2*time.Second, func() bool { return len(svc.Servers()) == 3 }) {
		t.Errorf("Servers() len = %d, want 3", len(svc.Servers()))
	}
}

func TestReload_ConcurrentReads(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfgFile, []byte("max_chunk_tokens: 512\nservers:\n  - backend: ollama\n    host: http://localhost:11434\n    model: ordis/jina-embeddings-v2-base-code\n"), 0644)
	svc, _ := NewConfigService(cfgFile)
	svc.Watch()
	defer svc.Stop()

	done := make(chan struct{})
	for i := 0; i < 100; i++ {
		go func() {
			for {
				select {
				case <-done:
					return
				default:
					_ = svc.MaxChunkTokens()
					_ = svc.Servers()
					_ = svc.ServerDims(0)
				}
			}
		}()
	}

	for i := 0; i < 10; i++ {
		os.WriteFile(cfgFile, []byte(fmt.Sprintf("max_chunk_tokens: %d\nservers:\n  - backend: ollama\n    host: http://localhost:11434\n    model: ordis/jina-embeddings-v2-base-code\n", 512+i)), 0644)
		time.Sleep(100 * time.Millisecond)
	}
	close(done)
}
```

- [ ] **Step 2: Run with race detector**

```bash
go test ./internal/config/ -run "TestReload_" -v -race -timeout 30s
```

Expected: All PASS, no races.

- [ ] **Step 3: Commit**

```bash
git add internal/config/reload_test.go
git commit -m "test(config): add hot reload integration tests with race detection"
```

---

## Chunk 3: FailoverEmbedder

### Task 7: Add EmbedError type and update existing embedders

**Files:**
- Modify: `internal/embedder/embedder.go`
- Modify: `internal/embedder/ollama.go`
- Modify: `internal/embedder/lmstudio.go`

This must happen BEFORE the FailoverEmbedder so `isTransientError` can
distinguish 4xx from 5xx.

- [ ] **Step 1: Add EmbedError to embedder.go**

```go
// EmbedError wraps an HTTP error from an embedding API.
type EmbedError struct {
	StatusCode int
	Message    string
}

func (e *EmbedError) Error() string {
	return fmt.Sprintf("embed error (HTTP %d): %s", e.StatusCode, e.Message)
}
```

- [ ] **Step 2: Update ollama.go and lmstudio.go**

Find the non-200 response handling in both files. Replace `fmt.Errorf(...)` with
`&EmbedError{StatusCode: resp.StatusCode, Message: body}`.

- [ ] **Step 3: Run existing tests**

```bash
go test ./internal/embedder/ -v
```

Expected: All PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/embedder/embedder.go internal/embedder/ollama.go internal/embedder/lmstudio.go
git commit -m "refactor(embedder): add EmbedError type for status-code-aware failover"
```

---

### Task 8: FailoverEmbedder implementation

**Files:**
- Create: `internal/embedder/failover.go`

- [ ] **Step 1: Implement FailoverEmbedder**

Create `internal/embedder/failover.go`:

```go
package embedder

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/ory/lumen/internal/config"
)

const healthCheckTimeout = 5 * time.Second

type FailoverEmbedder struct {
	cfg     *config.ConfigService
	mu      sync.Mutex
	servers []serverEntry
	active  int
	checked bool
}

type serverEntry struct {
	idx     int
	emb     Embedder
	healthy bool
}

func NewFailoverEmbedder(cfg *config.ConfigService) *FailoverEmbedder {
	return &FailoverEmbedder{cfg: cfg, active: -1}
}

func (f *FailoverEmbedder) ActiveServerIndex() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.active
}

func (f *FailoverEmbedder) Dimensions() int {
	f.mu.Lock()
	idx := f.active
	f.mu.Unlock()
	if idx < 0 {
		idx = 0
	}
	return f.cfg.ServerDims(idx)
}

func (f *FailoverEmbedder) ModelName() string {
	f.mu.Lock()
	idx := f.active
	f.mu.Unlock()
	if idx < 0 {
		idx = 0
	}
	servers := f.cfg.Servers()
	if idx < len(servers) {
		return servers[idx].Model
	}
	return ""
}

func (f *FailoverEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	f.mu.Lock()
	if !f.checked {
		f.initServers()
		f.checked = true
	}
	f.mu.Unlock()

	if f.active < 0 {
		return nil, fmt.Errorf("all embedding servers are unhealthy")
	}

	// Try active server, then iterate through remaining on transient failure
	for {
		f.mu.Lock()
		active := f.active
		if active < 0 || active >= len(f.servers) {
			f.mu.Unlock()
			return nil, fmt.Errorf("all embedding servers exhausted")
		}
		emb := f.servers[active].emb
		f.mu.Unlock()

		result, err := emb.Embed(ctx, texts)
		if err == nil {
			return result, nil
		}

		if !isTransientError(err) {
			return nil, err // 4xx = config error, don't failover
		}

		// Mark current as unhealthy, find next
		f.mu.Lock()
		f.servers[active].healthy = false
		next := f.findNextHealthy(active)
		if next < 0 {
			f.mu.Unlock()
			return nil, fmt.Errorf("all embedding servers exhausted after failover: last error: %w", err)
		}
		f.active = next
		if err := f.ensureEmbedder(next); err != nil {
			f.mu.Unlock()
			return nil, fmt.Errorf("failed to initialize fallback server %d: %w", next, err)
		}
		f.mu.Unlock()
		// Loop continues with new active server
	}
}

func (f *FailoverEmbedder) initServers() {
	servers := f.cfg.Servers()
	f.servers = make([]serverEntry, len(servers))
	for i := range servers {
		f.servers[i] = serverEntry{idx: i}
	}
	for i := range f.servers {
		if f.probeHealth(i) {
			f.servers[i].healthy = true
			f.active = i
			_ = f.ensureEmbedder(i) // best-effort at init
			return
		}
	}
}

func (f *FailoverEmbedder) findNextHealthy(after int) int {
	for i := after + 1; i < len(f.servers); i++ {
		if f.probeHealth(i) {
			f.servers[i].healthy = true
			return i
		}
	}
	return -1
}

func (f *FailoverEmbedder) probeHealth(i int) bool {
	servers := f.cfg.Servers()
	if i >= len(servers) {
		return false
	}
	srv := servers[i]
	endpoint := srv.Host + "/"
	if srv.Backend == "lmstudio" {
		endpoint = srv.Host + "/v1/models"
	}
	client := &http.Client{Timeout: healthCheckTimeout}
	resp, err := client.Get(endpoint)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (f *FailoverEmbedder) ensureEmbedder(i int) error {
	if f.servers[i].emb != nil {
		return nil
	}
	servers := f.cfg.Servers()
	srv := servers[i]
	dims := f.cfg.ServerDims(i)
	ctxLen := f.cfg.ServerCtxLength(i)

	var emb Embedder
	var err error
	switch srv.Backend {
	case "ollama":
		emb, err = NewOllama(srv.Model, dims, ctxLen, srv.Host)
	case "lmstudio":
		emb, err = NewLMStudio(srv.Model, dims, srv.Host)
	default:
		return fmt.Errorf("unknown backend %q", srv.Backend)
	}
	if err != nil {
		return err
	}
	f.servers[i].emb = emb
	return nil
}

func isTransientError(err error) bool {
	var ee *EmbedError
	if errors.As(err, &ee) {
		return ee.StatusCode >= 500
	}
	return true // network errors are transient
}
```

- [ ] **Step 2: Verify it compiles**

```bash
go build ./internal/embedder/
```

- [ ] **Step 3: Commit**

```bash
git add internal/embedder/failover.go
git commit -m "feat(embedder): add FailoverEmbedder with health-check-based server failover"
```

---

### Task 9: FailoverEmbedder integration tests

**Files:**
- Create: `internal/embedder/failover_test.go`

- [ ] **Step 1: Write all failover tests**

Create `internal/embedder/failover_test.go`:

```go
package embedder

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ory/lumen/internal/config"
)

func newTestOllamaServer(t *testing.T, healthy bool, embedStatus int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/":
			if healthy {
				fmt.Fprint(w, "Ollama is running")
			} else {
				w.WriteHeader(503)
			}
		case r.Method == "POST" && r.URL.Path == "/api/embed":
			w.WriteHeader(embedStatus)
			if embedStatus == 200 {
				fmt.Fprint(w, `{"embeddings":[[0.1,0.2,0.3]]}`)
			} else {
				fmt.Fprintf(w, `{"error":"status %d"}`, embedStatus)
			}
		default:
			w.WriteHeader(404)
		}
	}))
}

func testConfigService(t *testing.T, servers ...config.ServerConfig) *config.ConfigService {
	t.Helper()
	y := "servers:\n"
	for _, s := range servers {
		y += fmt.Sprintf("  - backend: %s\n    host: %s\n    model: %s\n    dims: %d\n",
			s.Backend, s.Host, s.Model, s.Dims)
	}
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfgFile, []byte(y), 0644)
	svc, err := config.NewConfigService(cfgFile)
	if err != nil {
		t.Fatalf("NewConfigService: %v", err)
	}
	return svc
}

func TestFailover_FirstHealthy(t *testing.T) {
	down := newTestOllamaServer(t, false, 200)
	defer down.Close()
	up1 := newTestOllamaServer(t, true, 200)
	defer up1.Close()
	up2 := newTestOllamaServer(t, true, 200)
	defer up2.Close()

	cfg := testConfigService(t,
		config.ServerConfig{Backend: "ollama", Host: down.URL, Model: "test", Dims: 3},
		config.ServerConfig{Backend: "ollama", Host: up1.URL, Model: "test", Dims: 3},
		config.ServerConfig{Backend: "ollama", Host: up2.URL, Model: "test", Dims: 3},
	)
	fe := NewFailoverEmbedder(cfg)
	_, err := fe.Embed(context.Background(), []string{"hello"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if fe.ActiveServerIndex() != 1 {
		t.Errorf("active = %d, want 1", fe.ActiveServerIndex())
	}
}

func TestFailover_OnEmbedError(t *testing.T) {
	srv1 := newTestOllamaServer(t, true, 500)
	defer srv1.Close()
	srv2 := newTestOllamaServer(t, true, 200)
	defer srv2.Close()

	cfg := testConfigService(t,
		config.ServerConfig{Backend: "ollama", Host: srv1.URL, Model: "test", Dims: 3},
		config.ServerConfig{Backend: "ollama", Host: srv2.URL, Model: "test", Dims: 3},
	)
	fe := NewFailoverEmbedder(cfg)
	_, err := fe.Embed(context.Background(), []string{"hello"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if fe.ActiveServerIndex() != 1 {
		t.Errorf("active = %d, want 1", fe.ActiveServerIndex())
	}
}

func TestFailover_4xxNoFailover(t *testing.T) {
	srv1 := newTestOllamaServer(t, true, 400)
	defer srv1.Close()
	srv2 := newTestOllamaServer(t, true, 200)
	defer srv2.Close()

	cfg := testConfigService(t,
		config.ServerConfig{Backend: "ollama", Host: srv1.URL, Model: "test", Dims: 3},
		config.ServerConfig{Backend: "ollama", Host: srv2.URL, Model: "test", Dims: 3},
	)
	fe := NewFailoverEmbedder(cfg)
	_, err := fe.Embed(context.Background(), []string{"hello"})
	if err == nil {
		t.Fatal("expected error for 400")
	}
	if fe.ActiveServerIndex() != 0 {
		t.Errorf("active = %d, want 0 (4xx should not failover)", fe.ActiveServerIndex())
	}
}

func TestFailover_AllExhausted(t *testing.T) {
	d1 := newTestOllamaServer(t, false, 200)
	defer d1.Close()
	d2 := newTestOllamaServer(t, false, 200)
	defer d2.Close()

	cfg := testConfigService(t,
		config.ServerConfig{Backend: "ollama", Host: d1.URL, Model: "test", Dims: 3},
		config.ServerConfig{Backend: "ollama", Host: d2.URL, Model: "test", Dims: 3},
	)
	fe := NewFailoverEmbedder(cfg)
	_, err := fe.Embed(context.Background(), []string{"hello"})
	if err == nil {
		t.Fatal("expected error when all down")
	}
}

func TestFailover_DimensionsReflectActive(t *testing.T) {
	down := newTestOllamaServer(t, false, 200)
	defer down.Close()
	up := newTestOllamaServer(t, true, 200)
	defer up.Close()

	cfg := testConfigService(t,
		config.ServerConfig{Backend: "ollama", Host: down.URL, Model: "a", Dims: 768},
		config.ServerConfig{Backend: "ollama", Host: up.URL, Model: "b", Dims: 1024},
	)
	fe := NewFailoverEmbedder(cfg)
	if got := fe.Dimensions(); got != 768 {
		t.Errorf("before embed: Dimensions() = %d, want 768", got)
	}
	fe.Embed(context.Background(), []string{"hello"})
	if got := fe.Dimensions(); got != 1024 {
		t.Errorf("after failover: Dimensions() = %d, want 1024", got)
	}
	if got := fe.ModelName(); got != "b" {
		t.Errorf("ModelName() = %q, want %q", got, "b")
	}
}

func TestFailover_SingleServer(t *testing.T) {
	srv := newTestOllamaServer(t, true, 200)
	defer srv.Close()
	cfg := testConfigService(t,
		config.ServerConfig{Backend: "ollama", Host: srv.URL, Model: "test", Dims: 3},
	)
	fe := NewFailoverEmbedder(cfg)
	_, err := fe.Embed(context.Background(), []string{"hello"})
	if err != nil {
		t.Fatal(err)
	}
	if fe.ActiveServerIndex() != 0 {
		t.Errorf("active = %d, want 0", fe.ActiveServerIndex())
	}
}

func TestFailover_LazyInit(t *testing.T) {
	srv1 := newTestOllamaServer(t, true, 200)
	defer srv1.Close()
	srv2 := newTestOllamaServer(t, true, 200)
	defer srv2.Close()
	cfg := testConfigService(t,
		config.ServerConfig{Backend: "ollama", Host: srv1.URL, Model: "test", Dims: 3},
		config.ServerConfig{Backend: "ollama", Host: srv2.URL, Model: "test", Dims: 3},
	)
	fe := NewFailoverEmbedder(cfg)
	fe.Embed(context.Background(), []string{"hello"})
	fe.mu.Lock()
	if fe.servers[0].emb == nil {
		t.Error("server[0].emb should be initialized")
	}
	if fe.servers[1].emb != nil {
		t.Error("server[1].emb should be nil (lazy)")
	}
	fe.mu.Unlock()
}

func TestFailover_ReloadPicksUpNewServers(t *testing.T) {
	down := newTestOllamaServer(t, false, 200)
	defer down.Close()
	up := newTestOllamaServer(t, true, 200)
	defer up.Close()

	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfgFile, []byte(fmt.Sprintf("servers:\n  - backend: ollama\n    host: %s\n    model: test\n    dims: 3\n", down.URL)), 0644)

	cfg, _ := config.NewConfigService(cfgFile)
	cfg.Watch()
	defer cfg.Stop()

	fe := NewFailoverEmbedder(cfg)
	_, err := fe.Embed(context.Background(), []string{"hello"})
	if err == nil {
		t.Fatal("expected error with only down server")
	}

	// Hot reload adds the healthy server
	os.WriteFile(cfgFile, []byte(fmt.Sprintf("servers:\n  - backend: ollama\n    host: %s\n    model: test\n    dims: 3\n  - backend: ollama\n    host: %s\n    model: test\n    dims: 3\n", down.URL, up.URL)), 0644)
	time.Sleep(500 * time.Millisecond)

	// Reset checked flag to force re-read on next Embed
	fe.mu.Lock()
	fe.checked = false
	fe.mu.Unlock()

	_, err = fe.Embed(context.Background(), []string{"hello"})
	if err != nil {
		t.Fatalf("Embed after reload: %v", err)
	}
}
```

- [ ] **Step 2: Run all failover tests**

```bash
go test ./internal/embedder/ -run "TestFailover_" -v -race
```

Expected: All PASS, no races.

- [ ] **Step 3: Commit**

```bash
git add internal/embedder/failover_test.go
git commit -m "test(embedder): add FailoverEmbedder integration tests with httptest servers"
```

---

## Chunk 4: Command Integration

All command migrations are combined into a single atomic commit to avoid broken
intermediate states.

### Task 10: Migrate all commands to ConfigService

**Files:**
- Modify: `cmd/embedder.go`
- Modify: `cmd/index.go`
- Modify: `cmd/search.go`
- Modify: `cmd/search_test.go`
- Modify: `cmd/stdio.go`
- Modify: `cmd/stdio_test.go`
- Modify: `cmd/hook.go`
- Modify: `cmd/hook_test.go`

- [ ] **Step 1: Update cmd/embedder.go**

Replace the current `newEmbedder(cfg config.Config)` with:

```go
func newEmbedder(cfg *config.ConfigService) *embedder.FailoverEmbedder {
	return embedder.NewFailoverEmbedder(cfg)
}
```

- [ ] **Step 2: Update cmd/index.go**

- Replace `cfg, err := config.Load()` with
  `cfg, err := config.NewConfigService(config.DefaultConfigPath())`
- Replace `applyModelFlag(cmd, &cfg)` — use `cfg.ServersForModel(model)` and
  pass filtered ConfigService or filter index to FailoverEmbedder
- Replace `cfg.MaxChunkTokens` → `cfg.MaxChunkTokens()`
- Replace `config.DBPathForProject(root, cfg.Model)` →
  `config.DBPathForProject(root, emb.ModelName())`
- Replace `setupIndexer(*config.Config, ...)` signature →
  `setupIndexer(*config.ConfigService, ...)`

- [ ] **Step 3: Update cmd/search.go**

Same pattern as index.go:
- `config.Load()` → `config.NewConfigService(config.DefaultConfigPath())`
- Field access → method calls
- `emb.ModelName()` for DB paths

- [ ] **Step 4: Update cmd/search_test.go**

Replace `config.Load()` at line 126 with
`config.NewConfigService(config.DefaultConfigPath())` or a test-specific
ConfigService.

- [ ] **Step 5: Update cmd/stdio.go**

- Replace `cfg config.Config` in `indexerCache` with
  `cfg *config.ConfigService`
- Replace `ic.cfg.MaxChunkTokens` → `ic.cfg.MaxChunkTokens()`
- Replace `ic.cfg.Backend` → read from active server
- Replace `ic.embedder` (single) with FailoverEmbedder
- Replace `ic.model` → `ic.embedder.ModelName()`
- Add `cfg.Watch()` at startup, `cfg.Stop()` at shutdown
- Remove `ic.freshnessTTL` and `ic.reindexTimeout` — read from
  `cfg.FreshnessTTL()` and `cfg.ReindexTimeout()` directly
- Update `config.Load()` at line 1199

- [ ] **Step 6: Update cmd/stdio_test.go**

Replace all `config.Config{}` struct literals (~10 places) with ConfigService
instances. Use `config.NewConfigService("")` for default config or create test
YAML files as needed.

- [ ] **Step 7: Update cmd/hook.go**

Replace `config.Load()` at line 128 with
`config.NewConfigService(config.DefaultConfigPath())`.
Replace `cfg.Model` → `emb.ModelName()` or read from ConfigService.

- [ ] **Step 8: Update cmd/hook_test.go**

Replace all `config.Load()` calls (lines 35, 252, 276, 348, 395) with
`config.NewConfigService("")` or test ConfigService.

- [ ] **Step 9: Build and run all tests**

```bash
go build ./... && go test ./... -short -race
```

Expected: All PASS.

- [ ] **Step 10: Commit**

```bash
git add cmd/
git commit -m "refactor(cmd): migrate all commands to ConfigService and FailoverEmbedder"
```

---

## Chunk 5: Backward Compatibility and Cleanup

### Task 11: Backward compatibility tests

**Files:**
- Create: `internal/config/compat_test.go`

- [ ] **Step 1: Write compat tests**

Create `internal/config/compat_test.go`:

```go
package config

import (
	"testing"
	"time"

	"github.com/ory/lumen/internal/models"
)

func TestCompat_ZeroConfig(t *testing.T) {
	for _, key := range []string{
		"LUMEN_BACKEND", "LUMEN_EMBED_MODEL", "LUMEN_EMBED_DIMS",
		"LUMEN_EMBED_CTX", "LUMEN_MAX_CHUNK_TOKENS", "OLLAMA_HOST",
		"LM_STUDIO_HOST", "LUMEN_FRESHNESS_TTL", "LUMEN_REINDEX_TIMEOUT",
	} {
		t.Setenv(key, "")
	}
	svc, _ := NewConfigService("")
	if got := svc.MaxChunkTokens(); got != 512 {
		t.Errorf("MaxChunkTokens = %d, want 512", got)
	}
	if got := svc.FreshnessTTL(); got != 60*time.Second {
		t.Errorf("FreshnessTTL = %v, want 60s", got)
	}
	s := svc.Servers()[0]
	if s.Backend != BackendOllama || s.Host != "http://localhost:11434" || s.Model != models.DefaultOllamaModel {
		t.Errorf("default server = %+v", s)
	}
}

func TestCompat_AllEnvVars(t *testing.T) {
	t.Setenv("LUMEN_BACKEND", "lmstudio")
	t.Setenv("LUMEN_EMBED_MODEL", "nomic-ai/nomic-embed-code-GGUF")
	t.Setenv("LM_STUDIO_HOST", "http://myhost:5555")
	t.Setenv("LUMEN_EMBED_DIMS", "3584")
	t.Setenv("LUMEN_EMBED_CTX", "8192")
	t.Setenv("LUMEN_MAX_CHUNK_TOKENS", "1024")
	t.Setenv("LUMEN_FRESHNESS_TTL", "30s")
	t.Setenv("LUMEN_REINDEX_TIMEOUT", "5m")
	svc, _ := NewConfigService("")
	if got := svc.MaxChunkTokens(); got != 1024 {
		t.Errorf("MaxChunkTokens = %d", got)
	}
	if got := svc.FreshnessTTL(); got != 30*time.Second {
		t.Errorf("FreshnessTTL = %v", got)
	}
	if got := svc.ReindexTimeout(); got != 5*time.Minute {
		t.Errorf("ReindexTimeout = %v", got)
	}
	s := svc.Servers()[0]
	if s.Backend != "lmstudio" || s.Host != "http://myhost:5555" || s.Model != "nomic-ai/nomic-embed-code-GGUF" {
		t.Errorf("server = %+v", s)
	}
	if got := svc.ServerDims(0); got != 3584 {
		t.Errorf("ServerDims(0) = %d, want 3584", got)
	}
}

func TestCompat_ModelFlagOverride(t *testing.T) {
	svc, _ := NewConfigService("")
	// Default server has jina model
	indices, err := svc.ServersForModel(models.DefaultOllamaModel)
	if err != nil {
		t.Fatal(err)
	}
	if len(indices) != 1 || indices[0] != 0 {
		t.Errorf("indices = %v, want [0]", indices)
	}
	// Non-existent model returns error
	_, err = svc.ServersForModel("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent model")
	}
}
```

- [ ] **Step 2: Run compat tests**

```bash
go test ./internal/config/ -run "TestCompat_" -v
```

- [ ] **Step 3: Commit**

```bash
git add internal/config/compat_test.go
git commit -m "test(config): add backward compatibility tests"
```

---

### Task 12: Remove old config.Load() and clean up

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

- [ ] **Step 1: Remove Load() and helpers**

Remove `Load()`, `EnvOrDefault()`, `EnvOrDefaultInt()`,
`EnvOrDefaultDuration()` from `config.go`. Remove the `Config` struct.
Keep: `XDGDataDir()`, `XDGConfigDir()`, `DefaultConfigPath()`,
`DBPathForProject()`, `DBPathForProjectBase()`, `IndexVersion`,
`BackendOllama`, `BackendLMStudio`.

- [ ] **Step 2: Remove old tests**

Remove tests for `Load()` in `config_test.go` that are now covered by
`service_test.go` and `compat_test.go`. Keep `TestXDGConfigDir`,
`TestDBPathForProject`, etc.

- [ ] **Step 3: Build and run full test suite**

```bash
go build ./... && go test ./... -race -v
```

Expected: All PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "refactor(config): remove old Load() and Config struct in favor of ConfigService"
```

---

### Task 13: Final verification

- [ ] **Step 1: Full test suite**

```bash
go test ./... -race -v
```

- [ ] **Step 2: Linter**

```bash
golangci-lint run
```

- [ ] **Step 3: Format check**

```bash
goimports -l .
```

Expected: zero issues, no formatting needed.
