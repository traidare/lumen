# Config File and Multi-Server Failover Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development
> (if subagents available) or superpowers:executing-plans to implement this plan.
> Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace env-var-only config with a YAML config file (koanf) and add
multi-server embedding failover with health checks and hot reload.

**Architecture:** koanf/v2 wraps layered config (defaults < YAML file < env
vars) in a `ConfigService` passed as dependency. `FailoverEmbedder` wraps
multiple backend embedders with ordered health-check failover. Hot reload via
file watcher in MCP server mode only.

**Tech Stack:** koanf/v2, koanf YAML provider, koanf env provider, fsnotify
(via koanf watcher), httptest (for failover tests)

**Spec:** `docs/superpowers/specs/2026-04-07-config-file-and-multi-server-failover.md`

---

## Critical Amendments (read before implementing)

These amendments override the task details below where they conflict.

### A1: Break circular import with `internal/models/` package

`config` imports `embedder` for KnownModels/defaults. `embedder/failover.go`
imports `config` for ConfigService. This creates `config -> embedder -> config`.

**Fix**: Before Task 3, add **Task 2.5: Extract models package**.
- Create `internal/models/models.go` — move `ModelSpec`, `KnownModels`,
  `ModelAliases`, `DefaultOllamaModel`, `DefaultLMStudioModel`,
  `DefaultMinScore`, `DimensionAwareMinScore()` from `internal/embedder/models.go`
- Update `internal/embedder/models.go` to re-export from `internal/models/` for
  backward compat (type aliases and var assignments)
- Update all imports: `embedder.KnownModels` → `models.KnownModels` in
  `config/service.go`
- Run `go test ./...` to verify nothing breaks
- Commit: `refactor: extract model specs into internal/models to break circular import`

In Task 3's `service.go` code, replace all `embedder.X` references with
`models.X` and import `"github.com/ory/lumen/internal/models"` instead of
`"github.com/ory/lumen/internal/embedder"`.

### A2: Reorder Task 15 (EmbedError) before Task 11 (FailoverEmbedder)

Task 11's `isTransientError` uses `*EmbedError` and `errors.As`. Without
EmbedError defined and existing embedders updated to return it, failover tests
cannot distinguish 4xx from 5xx. Implement Task 15 first, then Tasks 11-14.

### A3: Add Task 2.6: DefaultConfigPath helper (before Task 3)

Tasks 17-19 call `config.DefaultConfigPath()` but it's defined in Task 20
(after 17-19). Move it earlier. Add to `internal/config/service.go` alongside
Task 2:

```go
func DefaultConfigPath() string {
	return filepath.Join(XDGConfigDir(), "lumen", "config.yaml")
}
```

Delete Task 20 (now redundant).

### A4: Fix non-compiling intermediate commit in Task 16

Task 16 changes `newEmbedder` signature which breaks all callers. Do NOT commit
Task 16 separately. Combine Tasks 16-19 into a single atomic commit:
`refactor(cmd): migrate all commands to ConfigService and FailoverEmbedder`

### A5: Missing file migrations in command integration

Task 22 removes `config.Load()` and `Config` struct. These additional files also
use them and must be migrated (add to Tasks 17-19 scope or create Task 19.5):

- `cmd/hook.go:128` — calls `config.Load()`, uses `cfg.Model`
- `cmd/hook_test.go:35,252,276,348,395` — calls `config.Load()`
- `cmd/search_test.go:126` — calls `config.Load()`
- `cmd/stdio_test.go` — ~10 places constructing `config.Config{}` literals
- `cmd/stdio.go:1199` — additional `config.Load()` call beyond indexerCache

### A6: Fix code issues in Task 11 FailoverEmbedder

1. **`errorAs` → `errors.As`**: Use `errors.As(err, &ee)` (stdlib), not
   `errorAs(err, &ee)`
2. **`ensureEmbedder` must handle errors**: Replace `emb, _ = NewOllama(...)` with
   proper error handling. Return error from `ensureEmbedder`, propagate to caller.
3. **Health probe must check status 200, not <500**: The spec says `200` response.
   Change `resp.StatusCode >= 200 && resp.StatusCode < 500` to
   `resp.StatusCode == http.StatusOK`.
4. **`embedWithFailover` must iterate ALL remaining servers**: Current code tries
   only one fallback. Loop through all remaining servers after the active one fails.
5. **`ServerMinScore` double-lock**: Extract unlocked `serverDimsLocked(i)` helper
   to avoid recursive RLock acquisition.

### A7: Fix env var merge strategy in Task 5

The plan shows two conflicting approaches for `buildEnvOverrides`. Use this
single approach: `buildEnvOverrides()` returns a flat `map[string]interface{}`
with only the global settings. Server env vars are handled separately in
`NewConfigService` by reading `Servers()` from koanf after file loading, merging
env var overrides into `servers[0]`, and re-loading the merged server list into
koanf via `confmap.Provider`. Do not use `_server_env_overrides` sentinel keys.

### A8: Missing tests from spec

Add these tests to the plan:

1. **`TestFailover_ReloadPicksUpNewServers`** (add to Task 14): Start with 1
   server (down). Hot reload adds server 2 (up). Trigger failover → should find
   new server. Verifies FailoverEmbedder re-reads from ConfigService on failover.

2. **`TestCompat_ModelFlagOverride`** (add to Task 21): Set `--model` flag
   equivalent. Call `ServersForModel(model)`. Verify it returns the correct
   filtered indices matching current `applyModelFlag()` behavior.

### A9: Use polling in all reload tests

`TestReload_EnvStillWins` uses `time.Sleep(500ms)`. Replace with the same
polling pattern used in `TestReload_FileChange` (loop until value changes or 2s
deadline expires). Avoids flaky tests on slow CI.

---

## File Structure

| Action | Path | Responsibility |
|--------|------|----------------|
| Create | `internal/models/models.go` | KnownModels, ModelSpec, ModelAliases, defaults (extracted from embedder/models.go to break circular import: config->models<-embedder) |
| Create | `internal/models/models_test.go` | Existing model tests (moved from embedder/models_test.go) |
| Create | `internal/config/service.go` | ConfigService struct, constructor, global accessors, server accessors with models.KnownModels fallback |
| Create | `internal/config/service_test.go` | Unit tests for ConfigService (layering, env mapping, validation, KnownModels fallback) |
| Create | `internal/config/reload_test.go` | Integration tests for hot reload (real files, real watcher, race detector) |
| Create | `internal/config/compat_test.go` | Backward compat tests comparing ConfigService to current Load() |
| Create | `internal/embedder/failover.go` | FailoverEmbedder struct implementing Embedder interface (imports config for ConfigService) |
| Create | `internal/embedder/failover_test.go` | Integration tests with httptest servers |
| Modify | `internal/embedder/embedder.go` | Add EmbedError type for status-code-aware failover decisions |
| Modify | `internal/embedder/ollama.go` | Return *EmbedError on non-200 HTTP responses |
| Modify | `internal/embedder/lmstudio.go` | Return *EmbedError on non-200 HTTP responses |
| Modify | `internal/embedder/models.go` | Re-export from internal/models for backward compat during migration |
| Modify | `internal/config/config.go:115-121` | Add XDGConfigDir() alongside existing XDGDataDir() |
| Modify | `cmd/embedder.go:25-34` | Update newEmbedder to return FailoverEmbedder |
| Modify | `cmd/index.go:42-242` | Replace config.Load() with ConfigService, update setupIndexer |
| Modify | `cmd/search.go:94-211` | Replace config.Load() with ConfigService |
| Modify | `cmd/search_test.go:126` | Replace config.Load() with ConfigService |
| Modify | `cmd/stdio.go:115-148,1199` | Replace config.Config in indexerCache with ConfigService, add Watch/Stop |
| Modify | `cmd/stdio_test.go` | Replace ~10 config.Config{} struct literals with ConfigService |
| Modify | `cmd/hook.go:128` | Replace config.Load() with ConfigService |
| Modify | `cmd/hook_test.go:35,252,276,348,395` | Replace config.Load() calls with ConfigService |
| Modify | `go.mod` | Add koanf/v2 and providers |

---

## Chunk 1: ConfigService Foundation

### Task 1: Add koanf dependency

**Files:**
- Modify: `go.mod`

- [ ] **Step 1: Add koanf and providers**

```bash
cd /Users/aeneas/workspace/go/agent-index-go
go get github.com/knadh/koanf/v2@latest
go get github.com/knadh/koanf/providers/confmap@latest
go get github.com/knadh/koanf/providers/file@latest
go get github.com/knadh/koanf/providers/env@latest
go get github.com/knadh/koanf/parsers/yaml@latest
```

- [ ] **Step 2: Tidy and verify**

```bash
go mod tidy
go build ./...
```

Expected: builds cleanly with new dependencies.

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: add koanf/v2 config library with YAML, env, and confmap providers"
```

---

### Task 2: Add XDGConfigDir helper

**Files:**
- Modify: `internal/config/config.go:115-121`
- Test: `internal/config/config_test.go` (add test case)

- [ ] **Step 1: Write failing test**

In `internal/config/config_test.go`, add:

```go
func TestXDGConfigDir(t *testing.T) {
	t.Run("uses XDG_CONFIG_HOME when set", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", "/custom/config")
		got := XDGConfigDir()
		if got != "/custom/config" {
			t.Errorf("XDGConfigDir() = %q, want %q", got, "/custom/config")
		}
	})

	t.Run("falls back to ~/.config", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", "")
		got := XDGConfigDir()
		home, _ := os.UserHomeDir()
		want := filepath.Join(home, ".config")
		if got != want {
			t.Errorf("XDGConfigDir() = %q, want %q", got, want)
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/config/ -run TestXDGConfigDir -v
```

Expected: FAIL — `XDGConfigDir` undefined.

- [ ] **Step 3: Implement XDGConfigDir**

In `internal/config/config.go`, add after `XDGDataDir()` (line ~121):

```go
// XDGConfigDir returns XDG_CONFIG_HOME or ~/.config.
func XDGConfigDir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config")
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/config/ -run TestXDGConfigDir -v
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): add XDGConfigDir helper for config file location"
```

---

### Task 3: ConfigService with hardcoded defaults (no file, no env)

**Files:**
- Create: `internal/config/service.go`
- Create: `internal/config/service_test.go`

- [ ] **Step 1: Write failing test — defaults with no file, no env**

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
		t.Errorf("server[0].Backend = %q, want %q", s.Backend, "ollama")
	}
	if s.Host != "http://localhost:11434" {
		t.Errorf("server[0].Host = %q, want %q", s.Host, "http://localhost:11434")
	}
	if s.Model != "ordis/jina-embeddings-v2-base-code" {
		t.Errorf("server[0].Model = %q, want %q", s.Model, "ordis/jina-embeddings-v2-base-code")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/config/ -run TestDefaults_NoFileNoEnv -v
```

Expected: FAIL — `NewConfigService` undefined.

- [ ] **Step 3: Implement ConfigService with defaults**

Create `internal/config/service.go`:

```go
package config

import (
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/v2"

	"github.com/ory/lumen/internal/embedder"
)

// ServerConfig holds per-server configuration read from koanf.
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
	k  *koanf.Koanf
	mu sync.RWMutex
}

// NewConfigService creates a ConfigService. configPath is the YAML file path
// (empty string means no file). Environment variables are always loaded.
func NewConfigService(configPath string) (*ConfigService, error) {
	k := koanf.New(".")

	// Layer 1: hardcoded defaults
	defaults := map[string]interface{}{
		"max_chunk_tokens": 512,
		"freshness_ttl":    "60s",
		"reindex_timeout":  "0s",
		"log_level":        "info",
		"servers": []map[string]interface{}{
			{
				"backend": BackendOllama,
				"host":    "http://localhost:11434",
				"model":   embedder.DefaultOllamaModel,
			},
		},
	}
	if err := k.Load(confmap.Provider(defaults, "."), nil); err != nil {
		return nil, fmt.Errorf("loading defaults: %w", err)
	}

	svc := &ConfigService{k: k}

	if err := svc.validate(); err != nil {
		return nil, err
	}

	return svc, nil
}

// MaxChunkTokens returns the max_chunk_tokens setting.
func (s *ConfigService) MaxChunkTokens() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.k.Int("max_chunk_tokens")
}

// FreshnessTTL returns the freshness_ttl setting.
func (s *ConfigService) FreshnessTTL() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, _ := time.ParseDuration(s.k.String("freshness_ttl"))
	return d
}

// ReindexTimeout returns the reindex_timeout setting.
func (s *ConfigService) ReindexTimeout() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, _ := time.ParseDuration(s.k.String("reindex_timeout"))
	return d
}

// LogLevel returns the log_level setting.
func (s *ConfigService) LogLevel() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.k.String("log_level")
}

// Servers returns the ordered server list.
func (s *ConfigService) Servers() []ServerConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var servers []ServerConfig
	_ = s.k.Unmarshal("servers", &servers)
	return servers
}

// ServerDims returns dimensions for server at index i.
// Config value takes precedence; falls back to KnownModels.
func (s *ConfigService) ServerDims(i int) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key := fmt.Sprintf("servers.%d.dims", i)
	if d := s.k.Int(key); d != 0 {
		return d
	}
	model := s.k.String(fmt.Sprintf("servers.%d.model", i))
	if spec, ok := embedder.KnownModels[model]; ok {
		return spec.Dims
	}
	return 0
}

// ServerCtxLength returns context length for server at index i.
// Config value takes precedence; falls back to KnownModels.
func (s *ConfigService) ServerCtxLength(i int) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key := fmt.Sprintf("servers.%d.ctx_length", i)
	if c := s.k.Int(key); c != 0 {
		return c
	}
	model := s.k.String(fmt.Sprintf("servers.%d.model", i))
	if spec, ok := embedder.KnownModels[model]; ok {
		return spec.CtxLength
	}
	return 0
}

// ServerMinScore returns min score for server at index i.
// Config value takes precedence; falls back to KnownModels, then DimensionAwareMinScore.
func (s *ConfigService) ServerMinScore(i int) float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key := fmt.Sprintf("servers.%d.min_score", i)
	if m := s.k.Float64(key); m != 0 {
		return m
	}
	model := s.k.String(fmt.Sprintf("servers.%d.model", i))
	if spec, ok := embedder.KnownModels[model]; ok && spec.MinScore != 0 {
		return spec.MinScore
	}
	return embedder.DimensionAwareMinScore(s.ServerDims(i))
}

// ServersForModel returns indices of servers matching the given model.
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

// validate checks that the current config is valid.
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
		if s.ServerDims(i) == 0 {
			return fmt.Errorf("config: servers[%d]: cannot resolve dims for model %q — set dims explicitly", i, srv.Model)
		}
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/config/ -run TestDefaults_NoFileNoEnv -v
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config/service.go internal/config/service_test.go
git commit -m "feat(config): add ConfigService with koanf defaults provider"
```

---

### Task 4: YAML file provider

**Files:**
- Modify: `internal/config/service.go` (update NewConfigService)
- Modify: `internal/config/service_test.go`

- [ ] **Step 1: Write failing test — YAML overrides defaults**

Add to `internal/config/service_test.go`:

```go
func TestYAMLOverridesDefaults(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	yaml := `
max_chunk_tokens: 1024
log_level: debug
servers:
  - backend: lmstudio
    host: http://myhost:5555
    model: nomic-ai/nomic-embed-code-GGUF
  - backend: ollama
    host: http://other:11434
    model: ordis/jina-embeddings-v2-base-code
`
	if err := os.WriteFile(cfgFile, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

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
	if servers[0].Host != "http://myhost:5555" {
		t.Errorf("server[0].Host = %q, want %q", servers[0].Host, "http://myhost:5555")
	}
}
```

(Add `"os"` and `"path/filepath"` to imports.)

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/config/ -run TestYAMLOverridesDefaults -v
```

Expected: FAIL — YAML not loaded, defaults still returned.

- [ ] **Step 3: Add YAML file loading to NewConfigService**

In `internal/config/service.go`, add imports:

```go
"github.com/knadh/koanf/parsers/yaml"
"github.com/knadh/koanf/providers/file"
```

In `NewConfigService`, after loading defaults and before validation:

```go
	// Layer 2: YAML config file (optional)
	if configPath != "" {
		if _, err := os.Stat(configPath); err == nil {
			if err := k.Load(file.Provider(configPath), yaml.Parser()); err != nil {
				return nil, fmt.Errorf("loading config file %s: %w", configPath, err)
			}
		}
	}
```

Add `"os"` to imports.

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/config/ -run TestYAMLOverridesDefaults -v
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config/service.go internal/config/service_test.go
git commit -m "feat(config): add YAML file provider to ConfigService"
```

---

### Task 5: Environment variable provider

**Files:**
- Modify: `internal/config/service.go`
- Modify: `internal/config/service_test.go`

- [ ] **Step 1: Write failing tests — env overrides YAML, env server mapping**

Add to `internal/config/service_test.go`:

```go
func TestEnvOverridesYAML(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	yaml := `max_chunk_tokens: 1024`
	os.WriteFile(cfgFile, []byte(yaml), 0644)

	t.Setenv("LUMEN_MAX_CHUNK_TOKENS", "2048")

	svc, err := NewConfigService(cfgFile)
	if err != nil {
		t.Fatalf("NewConfigService: %v", err)
	}

	if got := svc.MaxChunkTokens(); got != 2048 {
		t.Errorf("MaxChunkTokens() = %d, want 2048", got)
	}
}

func TestEnvServerMapping_Ollama(t *testing.T) {
	t.Setenv("LUMEN_BACKEND", "ollama")
	t.Setenv("OLLAMA_HOST", "http://custom:9999")
	t.Setenv("LUMEN_EMBED_MODEL", "all-minilm")

	svc, err := NewConfigService("")
	if err != nil {
		t.Fatalf("NewConfigService: %v", err)
	}

	servers := svc.Servers()
	if len(servers) < 1 {
		t.Fatal("expected at least 1 server")
	}
	s := servers[0]
	if s.Backend != "ollama" {
		t.Errorf("Backend = %q, want %q", s.Backend, "ollama")
	}
	if s.Host != "http://custom:9999" {
		t.Errorf("Host = %q, want %q", s.Host, "http://custom:9999")
	}
	if s.Model != "all-minilm" {
		t.Errorf("Model = %q, want %q", s.Model, "all-minilm")
	}
}

func TestEnvServerMapping_LMStudio(t *testing.T) {
	t.Setenv("LUMEN_BACKEND", "lmstudio")
	t.Setenv("LM_STUDIO_HOST", "http://lms:2222")
	t.Setenv("LUMEN_EMBED_MODEL", "nomic-ai/nomic-embed-code-GGUF")

	svc, err := NewConfigService("")
	if err != nil {
		t.Fatalf("NewConfigService: %v", err)
	}

	s := svc.Servers()[0]
	if s.Host != "http://lms:2222" {
		t.Errorf("Host = %q, want %q", s.Host, "http://lms:2222")
	}
}

func TestHostConflict_BothSet(t *testing.T) {
	t.Setenv("LUMEN_BACKEND", "lmstudio")
	t.Setenv("OLLAMA_HOST", "http://ollama:1111")
	t.Setenv("LM_STUDIO_HOST", "http://lms:2222")
	t.Setenv("LUMEN_EMBED_MODEL", "nomic-ai/nomic-embed-code-GGUF")

	svc, err := NewConfigService("")
	if err != nil {
		t.Fatalf("NewConfigService: %v", err)
	}

	s := svc.Servers()[0]
	if s.Host != "http://lms:2222" {
		t.Errorf("Host = %q, want %q (lmstudio should win)", s.Host, "http://lms:2222")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/config/ -run "TestEnv|TestHostConflict" -v
```

Expected: FAIL — env vars not loaded into koanf.

- [ ] **Step 3: Add env var loading to NewConfigService**

In `NewConfigService`, after YAML loading and before validation, add the env var
layer. This uses a custom confmap provider (not koanf's env provider) because the
legacy env vars have non-standard key mappings:

```go
	// Layer 3: environment variable overrides
	envOverrides := buildEnvOverrides()
	if len(envOverrides) > 0 {
		if err := k.Load(confmap.Provider(envOverrides, "."), nil); err != nil {
			return nil, fmt.Errorf("loading env overrides: %w", err)
		}
	}
```

Add a helper function:

```go
// buildEnvOverrides reads legacy env vars and maps them to koanf keys.
func buildEnvOverrides() map[string]interface{} {
	m := make(map[string]interface{})

	// Global settings
	if v := os.Getenv("LUMEN_MAX_CHUNK_TOKENS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			m["max_chunk_tokens"] = n
		}
	}
	if v := os.Getenv("LUMEN_FRESHNESS_TTL"); v != "" {
		m["freshness_ttl"] = v
	}
	if v := os.Getenv("LUMEN_REINDEX_TIMEOUT"); v != "" {
		m["reindex_timeout"] = v
	}
	if v := os.Getenv("LUMEN_LOG_LEVEL"); v != "" {
		m["log_level"] = v
	}

	// Server-level env vars → servers.0.*
	// Only set if at least one server env var is present.
	backend := os.Getenv("LUMEN_BACKEND")
	model := os.Getenv("LUMEN_EMBED_MODEL")
	dims := os.Getenv("LUMEN_EMBED_DIMS")
	ctx := os.Getenv("LUMEN_EMBED_CTX")

	// Determine host based on backend
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

	// Build server override map — only include keys that are explicitly set
	srv := make(map[string]interface{})
	hasServerOverride := false

	if os.Getenv("LUMEN_BACKEND") != "" {
		srv["backend"] = backend
		hasServerOverride = true
	}
	if model != "" {
		srv["model"] = model
		hasServerOverride = true
	}
	if host != "" {
		srv["host"] = host
		hasServerOverride = true
	}
	if dims != "" {
		if n, err := strconv.Atoi(dims); err == nil {
			srv["dims"] = n
			hasServerOverride = true
		}
	}
	if ctx != "" {
		if n, err := strconv.Atoi(ctx); err == nil {
			srv["ctx_length"] = n
			hasServerOverride = true
		}
	}

	if hasServerOverride {
		// Merge into servers[0], preserving unset fields from file/defaults
		m["servers"] = []map[string]interface{}{srv}
	}

	return m
}
```

Add `"strconv"` to imports.

**Important**: The servers override approach above replaces the entire `servers`
list when any server env var is set. This is the specified behavior — env vars
define a single-server config. To preserve file-defined servers at index 1+, we
need a merge strategy. However, koanf's confmap provider replaces arrays
entirely. The simplest correct approach: when env vars define server overrides,
read the existing server list from koanf first, merge env overrides into
`servers[0]`, then re-set the full list:

Replace the `if hasServerOverride` block with:

```go
	if hasServerOverride {
		// Read current server list, merge env into servers[0]
		// This is done in NewConfigService after all providers load,
		// not here. Return the raw overrides and let the caller merge.
		m["_server_env_overrides"] = srv
	}
```

Then in `NewConfigService`, after loading all providers:

```go
	// Merge server env overrides into servers[0]
	if overrides, ok := k.Get("_server_env_overrides").(map[string]interface{}); ok {
		var servers []ServerConfig
		_ = k.Unmarshal("servers", &servers)
		if len(servers) == 0 {
			servers = append(servers, ServerConfig{})
		}
		if v, ok := overrides["backend"].(string); ok {
			servers[0].Backend = v
		}
		if v, ok := overrides["model"].(string); ok {
			servers[0].Model = v
		}
		if v, ok := overrides["host"].(string); ok {
			servers[0].Host = v
		}
		if v, ok := overrides["dims"].(int); ok {
			servers[0].Dims = v
		}
		if v, ok := overrides["ctx_length"].(int); ok {
			servers[0].CtxLength = v
		}
		// Re-marshal servers back into koanf
		serverMaps := make([]map[string]interface{}, len(servers))
		for i, s := range servers {
			serverMaps[i] = map[string]interface{}{
				"backend":    s.Backend,
				"host":       s.Host,
				"model":      s.Model,
				"dims":       s.Dims,
				"ctx_length": s.CtxLength,
				"min_score":  s.MinScore,
			}
		}
		_ = k.Load(confmap.Provider(map[string]interface{}{
			"servers": serverMaps,
		}, "."), nil)
	}
```

**On reflection, this is getting complex.** A simpler approach: `buildEnvOverrides`
returns the flat overrides. `NewConfigService` applies them to the first server
entry using direct struct manipulation, then reloads the servers into koanf.
The implementation details can be refined during coding — the test defines the
contract.

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/config/ -run "TestEnv|TestHostConflict" -v
```

Expected: PASS

- [ ] **Step 5: Run all config tests**

```bash
go test ./internal/config/ -v
```

Expected: All PASS (including defaults test from Task 3).

- [ ] **Step 6: Commit**

```bash
git add internal/config/service.go internal/config/service_test.go
git commit -m "feat(config): add env var provider with legacy server mapping"
```

---

### Task 6: KnownModels fallback and dims resolution tests

**Files:**
- Modify: `internal/config/service_test.go`

- [ ] **Step 1: Write failing tests — dims fallback and explicit override**

```go
func TestDimsFallback_KnownModel(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	// Known model, no dims set — should resolve from KnownModels
	yaml := `
servers:
  - backend: ollama
    host: http://localhost:11434
    model: ordis/jina-embeddings-v2-base-code
`
	os.WriteFile(cfgFile, []byte(yaml), 0644)

	svc, err := NewConfigService(cfgFile)
	if err != nil {
		t.Fatalf("NewConfigService: %v", err)
	}

	if got := svc.ServerDims(0); got != 768 {
		t.Errorf("ServerDims(0) = %d, want 768 (from KnownModels)", got)
	}
	if got := svc.ServerCtxLength(0); got != 8192 {
		t.Errorf("ServerCtxLength(0) = %d, want 8192", got)
	}
	if got := svc.ServerMinScore(0); got != 0.35 {
		t.Errorf("ServerMinScore(0) = %f, want 0.35", got)
	}
}

func TestDimsExplicit_OverridesKnown(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	yaml := `
servers:
  - backend: ollama
    host: http://localhost:11434
    model: ordis/jina-embeddings-v2-base-code
    dims: 1024
    ctx_length: 4096
    min_score: 0.5
`
	os.WriteFile(cfgFile, []byte(yaml), 0644)

	svc, err := NewConfigService(cfgFile)
	if err != nil {
		t.Fatalf("NewConfigService: %v", err)
	}

	if got := svc.ServerDims(0); got != 1024 {
		t.Errorf("ServerDims(0) = %d, want 1024 (explicit)", got)
	}
	if got := svc.ServerCtxLength(0); got != 4096 {
		t.Errorf("ServerCtxLength(0) = %d, want 4096 (explicit)", got)
	}
	if got := svc.ServerMinScore(0); got != 0.5 {
		t.Errorf("ServerMinScore(0) = %f, want 0.5 (explicit)", got)
	}
}

func TestDimsUnresolvable_Error(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	yaml := `
servers:
  - backend: ollama
    host: http://localhost:11434
    model: totally-unknown-model
`
	os.WriteFile(cfgFile, []byte(yaml), 0644)

	_, err := NewConfigService(cfgFile)
	if err == nil {
		t.Fatal("expected error for unresolvable dims, got nil")
	}
}
```

- [ ] **Step 2: Run tests to verify**

```bash
go test ./internal/config/ -run "TestDims" -v
```

Expected: ServerDims/ServerCtxLength/ServerMinScore tests should already pass
from Task 3 implementation. Unresolvable test should pass from validation.

- [ ] **Step 3: Fix any failures and commit**

```bash
git add internal/config/service_test.go
git commit -m "test(config): add KnownModels fallback and dims resolution tests"
```

---

### Task 7: Validation tests

**Files:**
- Modify: `internal/config/service_test.go`

- [ ] **Step 1: Write failing validation tests**

```go
func TestValidation_MissingBackend(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	yaml := `
servers:
  - host: http://localhost:11434
    model: ordis/jina-embeddings-v2-base-code
`
	os.WriteFile(cfgFile, []byte(yaml), 0644)

	_, err := NewConfigService(cfgFile)
	if err == nil {
		t.Fatal("expected error for missing backend")
	}
}

func TestValidation_UnknownBackend(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	yaml := `
servers:
  - backend: unknown
    host: http://localhost:11434
    model: ordis/jina-embeddings-v2-base-code
`
	os.WriteFile(cfgFile, []byte(yaml), 0644)

	_, err := NewConfigService(cfgFile)
	if err == nil {
		t.Fatal("expected error for unknown backend")
	}
}

func TestValidation_EmptyServerList(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	yaml := `servers: []`
	os.WriteFile(cfgFile, []byte(yaml), 0644)

	_, err := NewConfigService(cfgFile)
	if err == nil {
		t.Fatal("expected error for empty server list")
	}
}

func TestValidation_InvalidHost(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	yaml := `
servers:
  - backend: ollama
    host: not-a-url
    model: ordis/jina-embeddings-v2-base-code
`
	os.WriteFile(cfgFile, []byte(yaml), 0644)

	_, err := NewConfigService(cfgFile)
	if err == nil {
		t.Fatal("expected error for invalid host URL")
	}
}

func TestValidation_MissingModel(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	yaml := `
servers:
  - backend: ollama
    host: http://localhost:11434
`
	os.WriteFile(cfgFile, []byte(yaml), 0644)

	_, err := NewConfigService(cfgFile)
	if err == nil {
		t.Fatal("expected error for missing model")
	}
}
```

- [ ] **Step 2: Run validation tests**

```bash
go test ./internal/config/ -run "TestValidation_" -v
```

Expected: All PASS (validation was implemented in Task 3).

- [ ] **Step 3: Commit**

```bash
git add internal/config/service_test.go
git commit -m "test(config): add validation tests for server config"
```

---

### Task 8: ServersForModel tests

**Files:**
- Modify: `internal/config/service_test.go`

- [ ] **Step 1: Write failing tests**

```go
func TestServersForModel_Filters(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	yaml := `
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
`
	os.WriteFile(cfgFile, []byte(yaml), 0644)

	svc, err := NewConfigService(cfgFile)
	if err != nil {
		t.Fatalf("NewConfigService: %v", err)
	}

	indices, err := svc.ServersForModel("ordis/jina-embeddings-v2-base-code")
	if err != nil {
		t.Fatalf("ServersForModel: %v", err)
	}
	if len(indices) != 2 || indices[0] != 0 || indices[1] != 2 {
		t.Errorf("got indices %v, want [0, 2]", indices)
	}
}

func TestServersForModel_NoMatch(t *testing.T) {
	svc, err := NewConfigService("")
	if err != nil {
		t.Fatalf("NewConfigService: %v", err)
	}

	_, err = svc.ServersForModel("nonexistent-model")
	if err == nil {
		t.Fatal("expected error for no matching servers")
	}
}
```

- [ ] **Step 2: Run tests**

```bash
go test ./internal/config/ -run "TestServersForModel" -v
```

Expected: PASS (implemented in Task 3).

- [ ] **Step 3: Commit**

```bash
git add internal/config/service_test.go
git commit -m "test(config): add ServersForModel filtering tests"
```

---

## Chunk 2: Hot Reload

### Task 9: Hot reload — file change triggers re-merge

**Files:**
- Modify: `internal/config/service.go` (add Watch/Stop methods)
- Create: `internal/config/reload_test.go`

- [ ] **Step 1: Write failing test**

Create `internal/config/reload_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReload_FileChange(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfgFile, []byte(`max_chunk_tokens: 512
servers:
  - backend: ollama
    host: http://localhost:11434
    model: ordis/jina-embeddings-v2-base-code
`), 0644)

	svc, err := NewConfigService(cfgFile)
	if err != nil {
		t.Fatalf("NewConfigService: %v", err)
	}
	if err := svc.Watch(); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	defer svc.Stop()

	if got := svc.MaxChunkTokens(); got != 512 {
		t.Fatalf("initial MaxChunkTokens() = %d, want 512", got)
	}

	// Modify the file
	os.WriteFile(cfgFile, []byte(`max_chunk_tokens: 2048
servers:
  - backend: ollama
    host: http://localhost:11434
    model: ordis/jina-embeddings-v2-base-code
`), 0644)

	// Poll until change is picked up or deadline expires
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if svc.MaxChunkTokens() == 2048 {
			return // success
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("MaxChunkTokens() = %d after reload, want 2048", svc.MaxChunkTokens())
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/config/ -run TestReload_FileChange -v -timeout 10s
```

Expected: FAIL — `Watch` method undefined.

- [ ] **Step 3: Implement Watch and Stop**

In `internal/config/service.go`, add fields and methods:

```go
import "github.com/fsnotify/fsnotify"
```

Add to `ConfigService` struct:

```go
type ConfigService struct {
	k          *koanf.Koanf
	mu         sync.RWMutex
	configPath string
	watcher    *fsnotify.Watcher
	stopCh     chan struct{}
}
```

Update `NewConfigService` to store `configPath`.

Add methods:

```go
// Watch starts watching the config file for changes. Only needed for
// long-running processes (MCP server). Call Stop to clean up.
func (s *ConfigService) Watch() error {
	if s.configPath == "" {
		return nil // nothing to watch
	}

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("creating watcher: %w", err)
	}

	if err := w.Add(s.configPath); err != nil {
		w.Close()
		return fmt.Errorf("watching %s: %w", s.configPath, err)
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

	// Reload all layers in order
	defaults := s.defaultsMap()
	_ = k.Load(confmap.Provider(defaults, "."), nil)

	if s.configPath != "" {
		if _, err := os.Stat(s.configPath); err == nil {
			if err := k.Load(file.Provider(s.configPath), yaml.Parser()); err != nil {
				return // keep previous config on parse error
			}
		}
	}

	envOverrides := buildEnvOverrides()
	if len(envOverrides) > 0 {
		_ = k.Load(confmap.Provider(envOverrides, "."), nil)
	}

	// Validate before swapping
	tempSvc := &ConfigService{k: k}
	if err := tempSvc.validate(); err != nil {
		return // keep previous config on validation error
	}

	s.mu.Lock()
	s.k = k
	s.mu.Unlock()
}
```

Extract defaults into a method `defaultsMap()` to reuse between `NewConfigService`
and `reload()`.

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/config/ -run TestReload_FileChange -v -timeout 10s
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config/service.go internal/config/reload_test.go
git commit -m "feat(config): add file watcher for hot reload"
```

---

### Task 10: Remaining hot reload tests

**Files:**
- Modify: `internal/config/reload_test.go`

- [ ] **Step 1: Write remaining reload tests**

```go
func TestReload_EnvStillWins(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfgFile, []byte(`max_chunk_tokens: 512
servers:
  - backend: ollama
    host: http://localhost:11434
    model: ordis/jina-embeddings-v2-base-code
`), 0644)

	t.Setenv("LUMEN_MAX_CHUNK_TOKENS", "9999")

	svc, err := NewConfigService(cfgFile)
	if err != nil {
		t.Fatal(err)
	}
	svc.Watch()
	defer svc.Stop()

	// Modify file
	os.WriteFile(cfgFile, []byte(`max_chunk_tokens: 1024
servers:
  - backend: ollama
    host: http://localhost:11434
    model: ordis/jina-embeddings-v2-base-code
`), 0644)

	// Wait for reload
	time.Sleep(500 * time.Millisecond)

	// Env should still win
	if got := svc.MaxChunkTokens(); got != 9999 {
		t.Errorf("MaxChunkTokens() = %d, want 9999 (env should win)", got)
	}
}

func TestReload_InvalidRetainsPrevious(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfgFile, []byte(`max_chunk_tokens: 512
servers:
  - backend: ollama
    host: http://localhost:11434
    model: ordis/jina-embeddings-v2-base-code
`), 0644)

	svc, err := NewConfigService(cfgFile)
	if err != nil {
		t.Fatal(err)
	}
	svc.Watch()
	defer svc.Stop()

	// Overwrite with invalid config (empty server list)
	os.WriteFile(cfgFile, []byte(`servers: []`), 0644)

	time.Sleep(500 * time.Millisecond)

	// Should retain previous valid config
	if got := svc.MaxChunkTokens(); got != 512 {
		t.Errorf("MaxChunkTokens() = %d, want 512 (should retain previous)", got)
	}
	if len(svc.Servers()) != 1 {
		t.Errorf("Servers() len = %d, want 1 (should retain previous)", len(svc.Servers()))
	}
}

func TestReload_ServerListChange(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfgFile, []byte(`
servers:
  - backend: ollama
    host: http://a:11434
    model: ordis/jina-embeddings-v2-base-code
  - backend: ollama
    host: http://b:11434
    model: ordis/jina-embeddings-v2-base-code
`), 0644)

	svc, err := NewConfigService(cfgFile)
	if err != nil {
		t.Fatal(err)
	}
	svc.Watch()
	defer svc.Stop()

	if len(svc.Servers()) != 2 {
		t.Fatalf("initial Servers() len = %d, want 2", len(svc.Servers()))
	}

	// Add a third server
	os.WriteFile(cfgFile, []byte(`
servers:
  - backend: ollama
    host: http://a:11434
    model: ordis/jina-embeddings-v2-base-code
  - backend: ollama
    host: http://b:11434
    model: ordis/jina-embeddings-v2-base-code
  - backend: ollama
    host: http://c:11434
    model: ordis/jina-embeddings-v2-base-code
`), 0644)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(svc.Servers()) == 3 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("Servers() len = %d after reload, want 3", len(svc.Servers()))
}

func TestReload_ConcurrentReads(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	os.WriteFile(cfgFile, []byte(`max_chunk_tokens: 512
servers:
  - backend: ollama
    host: http://localhost:11434
    model: ordis/jina-embeddings-v2-base-code
`), 0644)

	svc, err := NewConfigService(cfgFile)
	if err != nil {
		t.Fatal(err)
	}
	svc.Watch()
	defer svc.Stop()

	// Spawn concurrent readers
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

	// Trigger reloads
	for i := 0; i < 10; i++ {
		os.WriteFile(cfgFile, []byte(fmt.Sprintf(`max_chunk_tokens: %d
servers:
  - backend: ollama
    host: http://localhost:11434
    model: ordis/jina-embeddings-v2-base-code
`, 512+i)), 0644)
		time.Sleep(100 * time.Millisecond)
	}

	close(done)
}
```

(Add `"fmt"` to imports.)

- [ ] **Step 2: Run all reload tests with race detector**

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

### Task 11: FailoverEmbedder — first healthy server selection

**Files:**
- Create: `internal/embedder/failover.go`
- Create: `internal/embedder/failover_test.go`

- [ ] **Step 1: Write failing test — picks first healthy server**

Create `internal/embedder/failover_test.go`:

```go
package embedder

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ory/lumen/internal/config"
)

// newTestOllamaServer creates a test server that responds to health probes and embed requests.
// healthy controls whether GET / returns 200.
// embedStatus controls the response to POST /api/embed.
func newTestOllamaServer(t *testing.T, healthy bool, embedStatus int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/":
			if healthy {
				w.WriteHeader(200)
				fmt.Fprint(w, "Ollama is running")
			} else {
				w.WriteHeader(503)
			}
		case r.Method == "POST" && r.URL.Path == "/api/embed":
			w.WriteHeader(embedStatus)
			if embedStatus == 200 {
				// Return a valid embedding response with 768 dims
				fmt.Fprint(w, `{"embeddings":[[0.1,0.2,0.3]]}`)
			}
		default:
			w.WriteHeader(404)
		}
	}))
}

func testConfigService(t *testing.T, servers ...config.ServerConfig) *config.ConfigService {
	t.Helper()
	// Build YAML from servers
	yaml := "servers:\n"
	for _, s := range servers {
		yaml += fmt.Sprintf("  - backend: %s\n    host: %s\n    model: %s\n    dims: %d\n",
			s.Backend, s.Host, s.Model, s.Dims)
	}
	dir := t.TempDir()
	cfgFile := dir + "/config.yaml"
	if err := os.WriteFile(cfgFile, []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
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
		config.ServerConfig{Backend: "ollama", Host: down.URL, Model: "test-model", Dims: 3},
		config.ServerConfig{Backend: "ollama", Host: up1.URL, Model: "test-model", Dims: 3},
		config.ServerConfig{Backend: "ollama", Host: up2.URL, Model: "test-model", Dims: 3},
	)

	fe := NewFailoverEmbedder(cfg)
	_, err := fe.Embed(context.Background(), []string{"hello"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}

	// Should have selected server index 1 (first healthy)
	if fe.ActiveServerIndex() != 1 {
		t.Errorf("active server = %d, want 1", fe.ActiveServerIndex())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/embedder/ -run TestFailover_FirstHealthy -v
```

Expected: FAIL — `NewFailoverEmbedder` undefined.

- [ ] **Step 3: Implement FailoverEmbedder**

Create `internal/embedder/failover.go`:

```go
package embedder

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/ory/lumen/internal/config"
)

const healthCheckTimeout = 5 * time.Second

// FailoverEmbedder implements the Embedder interface with ordered server failover.
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

// NewFailoverEmbedder creates a FailoverEmbedder backed by the given ConfigService.
func NewFailoverEmbedder(cfg *config.ConfigService) *FailoverEmbedder {
	return &FailoverEmbedder{cfg: cfg, active: -1}
}

// ActiveServerIndex returns the index of the currently active server.
func (f *FailoverEmbedder) ActiveServerIndex() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.active
}

// Dimensions returns dims for the active server (or server 0 before first Embed).
func (f *FailoverEmbedder) Dimensions() int {
	f.mu.Lock()
	idx := f.active
	f.mu.Unlock()
	if idx < 0 {
		idx = 0
	}
	return f.cfg.ServerDims(idx)
}

// ModelName returns the model for the active server (or server 0 before first Embed).
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

// Embed delegates to the active server, with failover on transient errors.
func (f *FailoverEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	f.mu.Lock()
	if !f.checked {
		f.initServers()
		f.checked = true
	}
	f.mu.Unlock()

	return f.embedWithFailover(ctx, texts)
}

func (f *FailoverEmbedder) initServers() {
	servers := f.cfg.Servers()
	f.servers = make([]serverEntry, len(servers))
	for i := range servers {
		f.servers[i] = serverEntry{idx: i}
	}

	// Probe and select first healthy
	for i := range f.servers {
		if f.probeHealth(i) {
			f.servers[i].healthy = true
			f.active = i
			f.ensureEmbedder(i)
			return
		}
	}
}

func (f *FailoverEmbedder) embedWithFailover(ctx context.Context, texts []string) ([][]float32, error) {
	f.mu.Lock()
	active := f.active
	f.mu.Unlock()

	if active < 0 {
		return nil, fmt.Errorf("all servers unhealthy: no active server")
	}

	f.mu.Lock()
	emb := f.servers[active].emb
	f.mu.Unlock()

	result, err := emb.Embed(ctx, texts)
	if err == nil {
		return result, nil
	}

	// Check if error is a failover trigger (transient, not 4xx)
	if !isTransientError(err) {
		return nil, err
	}

	// Try remaining servers
	f.mu.Lock()
	f.servers[active].healthy = false
	next := f.findNextHealthy(active)
	if next >= 0 {
		f.active = next
		f.ensureEmbedder(next)
	}
	f.mu.Unlock()

	if next < 0 {
		return nil, fmt.Errorf("all servers exhausted after failover: last error: %w", err)
	}

	f.mu.Lock()
	emb = f.servers[next].emb
	f.mu.Unlock()

	return emb.Embed(ctx, texts)
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
	return resp.StatusCode >= 200 && resp.StatusCode < 500
}

func (f *FailoverEmbedder) ensureEmbedder(i int) {
	if f.servers[i].emb != nil {
		return
	}
	servers := f.cfg.Servers()
	srv := servers[i]
	dims := f.cfg.ServerDims(i)
	ctxLen := f.cfg.ServerCtxLength(i)

	var emb Embedder
	switch srv.Backend {
	case "ollama":
		emb, _ = NewOllama(srv.Model, dims, ctxLen, srv.Host)
	case "lmstudio":
		emb, _ = NewLMStudio(srv.Model, dims, srv.Host)
	}
	f.servers[i].emb = emb
}

// isTransientError returns true for errors that should trigger failover.
// 4xx errors are NOT transient (config errors).
func isTransientError(err error) bool {
	// Check for EmbedError with status code
	var ee *EmbedError
	if ok := errorAs(err, &ee); ok {
		return ee.StatusCode >= 500
	}
	// Network errors are transient
	return true
}
```

Note: `isTransientError` needs an `EmbedError` type. Add to
`internal/embedder/embedder.go`:

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

Also add `errorAs` helper (or use `errors.As` directly). Update the Ollama and
LMStudio `Embed` methods to return `*EmbedError` for non-200 responses so
failover can distinguish 4xx from 5xx. This requires modifying the existing
error handling in `ollama.go` and `lmstudio.go` to wrap the status code.

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/embedder/ -run TestFailover_FirstHealthy -v
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/embedder/failover.go internal/embedder/failover_test.go internal/embedder/embedder.go
git commit -m "feat(embedder): add FailoverEmbedder with health-check-based server selection"
```

---

### Task 12: FailoverEmbedder — failover on embed error

**Files:**
- Modify: `internal/embedder/failover_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestFailover_OnEmbedError(t *testing.T) {
	// Server 1: healthy, but returns 500 on embed
	srv1 := newTestOllamaServer(t, true, 500)
	defer srv1.Close()
	// Server 2: healthy, returns 200 on embed
	srv2 := newTestOllamaServer(t, true, 200)
	defer srv2.Close()

	cfg := testConfigService(t,
		config.ServerConfig{Backend: "ollama", Host: srv1.URL, Model: "test-model", Dims: 3},
		config.ServerConfig{Backend: "ollama", Host: srv2.URL, Model: "test-model", Dims: 3},
	)

	fe := NewFailoverEmbedder(cfg)
	_, err := fe.Embed(context.Background(), []string{"hello"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}

	// Should have failed over to server 1
	if fe.ActiveServerIndex() != 1 {
		t.Errorf("active server = %d, want 1 (after failover)", fe.ActiveServerIndex())
	}
}
```

- [ ] **Step 2: Run test**

```bash
go test ./internal/embedder/ -run TestFailover_OnEmbedError -v
```

Expected: PASS (failover logic already implemented in Task 11).

- [ ] **Step 3: Commit**

```bash
git add internal/embedder/failover_test.go
git commit -m "test(embedder): add failover-on-embed-error test"
```

---

### Task 13: FailoverEmbedder — 4xx no failover

**Files:**
- Modify: `internal/embedder/failover_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestFailover_4xxNoFailover(t *testing.T) {
	// Server 1: healthy, returns 400 on embed (config error)
	srv1 := newTestOllamaServer(t, true, 400)
	defer srv1.Close()
	// Server 2: healthy
	srv2 := newTestOllamaServer(t, true, 200)
	defer srv2.Close()

	cfg := testConfigService(t,
		config.ServerConfig{Backend: "ollama", Host: srv1.URL, Model: "test-model", Dims: 3},
		config.ServerConfig{Backend: "ollama", Host: srv2.URL, Model: "test-model", Dims: 3},
	)

	fe := NewFailoverEmbedder(cfg)
	_, err := fe.Embed(context.Background(), []string{"hello"})
	if err == nil {
		t.Fatal("expected error for 400 response")
	}

	// Should NOT have failed over — still on server 0
	if fe.ActiveServerIndex() != 0 {
		t.Errorf("active server = %d, want 0 (4xx should not trigger failover)", fe.ActiveServerIndex())
	}
}
```

- [ ] **Step 2: Run test**

```bash
go test ./internal/embedder/ -run TestFailover_4xxNoFailover -v
```

Expected: PASS if `isTransientError` correctly excludes 4xx. May need fixes to
ensure Ollama/LMStudio return `*EmbedError` with status codes.

- [ ] **Step 3: Fix and commit**

```bash
git add internal/embedder/failover_test.go
git commit -m "test(embedder): add 4xx-no-failover test"
```

---

### Task 14: FailoverEmbedder — all exhausted, dimensions, single server, lazy init

**Files:**
- Modify: `internal/embedder/failover_test.go`

- [ ] **Step 1: Write remaining failover tests**

```go
func TestFailover_AllExhausted(t *testing.T) {
	down1 := newTestOllamaServer(t, false, 200)
	defer down1.Close()
	down2 := newTestOllamaServer(t, false, 200)
	defer down2.Close()

	cfg := testConfigService(t,
		config.ServerConfig{Backend: "ollama", Host: down1.URL, Model: "test-model", Dims: 3},
		config.ServerConfig{Backend: "ollama", Host: down2.URL, Model: "test-model", Dims: 3},
	)

	fe := NewFailoverEmbedder(cfg)
	_, err := fe.Embed(context.Background(), []string{"hello"})
	if err == nil {
		t.Fatal("expected error when all servers are down")
	}
}

func TestFailover_DimensionsReflectActive(t *testing.T) {
	down := newTestOllamaServer(t, false, 200)
	defer down.Close()
	up := newTestOllamaServer(t, true, 200)
	defer up.Close()

	cfg := testConfigService(t,
		config.ServerConfig{Backend: "ollama", Host: down.URL, Model: "model-a", Dims: 768},
		config.ServerConfig{Backend: "ollama", Host: up.URL, Model: "model-b", Dims: 1024},
	)

	fe := NewFailoverEmbedder(cfg)

	// Before embed: defaults to server 0
	if got := fe.Dimensions(); got != 768 {
		t.Errorf("Dimensions() before embed = %d, want 768", got)
	}

	// After embed: server 0 is down, falls to server 1
	fe.Embed(context.Background(), []string{"hello"})

	if got := fe.Dimensions(); got != 1024 {
		t.Errorf("Dimensions() after failover = %d, want 1024", got)
	}
	if got := fe.ModelName(); got != "model-b" {
		t.Errorf("ModelName() = %q, want %q", got, "model-b")
	}
}

func TestFailover_SingleServer(t *testing.T) {
	srv := newTestOllamaServer(t, true, 200)
	defer srv.Close()

	cfg := testConfigService(t,
		config.ServerConfig{Backend: "ollama", Host: srv.URL, Model: "test-model", Dims: 3},
	)

	fe := NewFailoverEmbedder(cfg)
	_, err := fe.Embed(context.Background(), []string{"hello"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
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
		config.ServerConfig{Backend: "ollama", Host: srv1.URL, Model: "test-model", Dims: 3},
		config.ServerConfig{Backend: "ollama", Host: srv2.URL, Model: "test-model", Dims: 3},
	)

	fe := NewFailoverEmbedder(cfg)
	fe.Embed(context.Background(), []string{"hello"})

	// Only server 0 should have an embedder
	fe.mu.Lock()
	if fe.servers[0].emb == nil {
		t.Error("server[0].emb should be initialized")
	}
	if fe.servers[1].emb != nil {
		t.Error("server[1].emb should be nil (lazy)")
	}
	fe.mu.Unlock()
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
git commit -m "test(embedder): add exhaustion, dimensions, single-server, and lazy-init failover tests"
```

---

### Task 15: EmbedError type in existing embedders

**Files:**
- Modify: `internal/embedder/embedder.go` (add EmbedError type)
- Modify: `internal/embedder/ollama.go` (return EmbedError on non-200)
- Modify: `internal/embedder/lmstudio.go` (return EmbedError on non-200)

- [ ] **Step 1: Check existing error handling in ollama.go and lmstudio.go**

Read the error-returning paths in both files to see how they currently report
HTTP errors.

- [ ] **Step 2: Add EmbedError and update embedders**

In `internal/embedder/embedder.go`, add:

```go
import "errors"

// EmbedError wraps an HTTP error from an embedding API.
type EmbedError struct {
	StatusCode int
	Message    string
}

func (e *EmbedError) Error() string {
	return fmt.Sprintf("embed error (HTTP %d): %s", e.StatusCode, e.Message)
}
```

In `ollama.go`, find the non-200 response handling and change from
`fmt.Errorf(...)` to `&EmbedError{StatusCode: resp.StatusCode, Message: ...}`.

In `lmstudio.go`, do the same.

- [ ] **Step 3: Run existing embedder tests**

```bash
go test ./internal/embedder/ -v
```

Expected: All existing tests still PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/embedder/embedder.go internal/embedder/ollama.go internal/embedder/lmstudio.go
git commit -m "refactor(embedder): add EmbedError type for status-code-aware error handling"
```

---

## Chunk 4: Command Integration

### Task 16: Update cmd/embedder.go to use ConfigService

**Files:**
- Modify: `cmd/embedder.go:25-34`

- [ ] **Step 1: Update newEmbedder to create FailoverEmbedder**

Replace the current `newEmbedder(cfg config.Config)` with:

```go
func newEmbedder(cfg *config.ConfigService) embedder.Embedder {
	return embedder.NewFailoverEmbedder(cfg)
}
```

This breaks the existing callers — that's expected. Fix in Tasks 17-19.

- [ ] **Step 2: Commit (will not compile yet)**

```bash
git add cmd/embedder.go
git commit -m "refactor(cmd): update newEmbedder to use ConfigService and FailoverEmbedder"
```

---

### Task 17: Update cmd/index.go

**Files:**
- Modify: `cmd/index.go:42-242`

- [ ] **Step 1: Replace config.Load() with ConfigService**

Key changes:
- Replace `cfg, err := config.Load()` with
  `cfg, err := config.NewConfigService(config.DefaultConfigPath())`
- Replace `applyModelFlag(cmd, &cfg)` — the `--model` flag now calls
  `cfg.ServersForModel(model)` and the FailoverEmbedder is filtered accordingly
- Replace `newEmbedder(cfg)` with `newEmbedder(cfg)` (new signature)
- Replace `cfg.MaxChunkTokens` with `cfg.MaxChunkTokens()`
- Replace `config.DBPathForProject(root, cfg.Model)` with
  `config.DBPathForProject(root, emb.ModelName())`
- `setupIndexer` takes `*config.ConfigService` instead of `*config.Config`

- [ ] **Step 2: Build and fix compilation errors**

```bash
go build ./cmd/...
```

Fix all compilation errors iteratively.

- [ ] **Step 3: Run existing tests**

```bash
go test ./... -short
```

Expected: All PASS.

- [ ] **Step 4: Commit**

```bash
git add cmd/index.go
git commit -m "refactor(cmd): migrate index command to ConfigService"
```

---

### Task 18: Update cmd/search.go

**Files:**
- Modify: `cmd/search.go:94-211`

- [ ] **Step 1: Replace config.Load() with ConfigService**

Same pattern as Task 17:
- `config.Load()` → `config.NewConfigService(config.DefaultConfigPath())`
- `cfg.Model` → `emb.ModelName()`
- `cfg.MaxChunkTokens` → `cfg.MaxChunkTokens()`
- `computeMaxDistance(minScore, emb.ModelName(), emb.Dimensions())` stays the
  same since FailoverEmbedder implements the same interface

- [ ] **Step 2: Build and fix**

```bash
go build ./cmd/...
```

- [ ] **Step 3: Run tests**

```bash
go test ./... -short
```

- [ ] **Step 4: Commit**

```bash
git add cmd/search.go
git commit -m "refactor(cmd): migrate search command to ConfigService"
```

---

### Task 19: Update cmd/stdio.go (MCP server)

**Files:**
- Modify: `cmd/stdio.go:115-148` and related functions

- [ ] **Step 1: Replace config.Config with ConfigService in indexerCache**

Key changes:
- Replace `cfg config.Config` field with `cfg *config.ConfigService`
- Replace `ic.cfg.MaxChunkTokens` → `ic.cfg.MaxChunkTokens()`
- Replace `ic.cfg.Backend` → read from active server via ConfigService
- Replace `ic.embedder` (single instance) → `FailoverEmbedder` from
  `newEmbedder(cfg)`
- Replace `ic.model` → `ic.embedder.ModelName()` (dynamic from failover)
- Add `cfg.Watch()` at startup, `cfg.Stop()` at shutdown for hot reload
- Remove `ic.freshnessTTL` and `ic.reindexTimeout` fields — read from
  `cfg.FreshnessTTL()` and `cfg.ReindexTimeout()` directly

- [ ] **Step 2: Build and fix**

```bash
go build ./cmd/...
```

- [ ] **Step 3: Run tests**

```bash
go test ./... -short
```

- [ ] **Step 4: Commit**

```bash
git add cmd/stdio.go
git commit -m "refactor(cmd): migrate MCP server to ConfigService with hot reload"
```

---

### Task 20: Add DefaultConfigPath helper

**Files:**
- Modify: `internal/config/service.go`

- [ ] **Step 1: Add DefaultConfigPath**

```go
// DefaultConfigPath returns the default config file path.
func DefaultConfigPath() string {
	return filepath.Join(XDGConfigDir(), "lumen", "config.yaml")
}
```

- [ ] **Step 2: Commit**

```bash
git add internal/config/service.go
git commit -m "feat(config): add DefaultConfigPath helper"
```

---

## Chunk 5: Backward Compatibility and Cleanup

### Task 21: Backward compatibility tests

**Files:**
- Create: `internal/config/compat_test.go`

- [ ] **Step 1: Write compat tests**

Create `internal/config/compat_test.go`:

```go
package config

import (
	"testing"
	"time"

	"github.com/ory/lumen/internal/embedder"
)

func TestCompat_ZeroConfig(t *testing.T) {
	// Clear all LUMEN env vars
	for _, key := range []string{
		"LUMEN_BACKEND", "LUMEN_EMBED_MODEL", "LUMEN_EMBED_DIMS",
		"LUMEN_EMBED_CTX", "LUMEN_MAX_CHUNK_TOKENS", "OLLAMA_HOST",
		"LM_STUDIO_HOST", "LUMEN_FRESHNESS_TTL", "LUMEN_REINDEX_TIMEOUT",
		"LUMEN_LOG_LEVEL",
	} {
		t.Setenv(key, "")
	}

	svc, err := NewConfigService("")
	if err != nil {
		t.Fatalf("NewConfigService: %v", err)
	}

	// Must match current config.Load() defaults
	if got := svc.MaxChunkTokens(); got != 512 {
		t.Errorf("MaxChunkTokens = %d, want 512", got)
	}
	if got := svc.FreshnessTTL(); got != 60*time.Second {
		t.Errorf("FreshnessTTL = %v, want 60s", got)
	}

	servers := svc.Servers()
	if len(servers) != 1 {
		t.Fatalf("Servers len = %d, want 1", len(servers))
	}
	if servers[0].Backend != BackendOllama {
		t.Errorf("Backend = %q, want %q", servers[0].Backend, BackendOllama)
	}
	if servers[0].Host != "http://localhost:11434" {
		t.Errorf("Host = %q, want %q", servers[0].Host, "http://localhost:11434")
	}
	if servers[0].Model != embedder.DefaultOllamaModel {
		t.Errorf("Model = %q, want %q", servers[0].Model, embedder.DefaultOllamaModel)
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

	svc, err := NewConfigService("")
	if err != nil {
		t.Fatalf("NewConfigService: %v", err)
	}

	if got := svc.MaxChunkTokens(); got != 1024 {
		t.Errorf("MaxChunkTokens = %d, want 1024", got)
	}
	if got := svc.FreshnessTTL(); got != 30*time.Second {
		t.Errorf("FreshnessTTL = %v, want 30s", got)
	}
	if got := svc.ReindexTimeout(); got != 5*time.Minute {
		t.Errorf("ReindexTimeout = %v, want 5m", got)
	}

	s := svc.Servers()[0]
	if s.Backend != "lmstudio" {
		t.Errorf("Backend = %q, want lmstudio", s.Backend)
	}
	if s.Host != "http://myhost:5555" {
		t.Errorf("Host = %q, want http://myhost:5555", s.Host)
	}
	if s.Model != "nomic-ai/nomic-embed-code-GGUF" {
		t.Errorf("Model = %q", s.Model)
	}
	if got := svc.ServerDims(0); got != 3584 {
		t.Errorf("ServerDims(0) = %d, want 3584", got)
	}
}
```

- [ ] **Step 2: Run compat tests**

```bash
go test ./internal/config/ -run "TestCompat_" -v
```

Expected: All PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/config/compat_test.go
git commit -m "test(config): add backward compatibility tests"
```

---

### Task 22: Remove old config.Load() and clean up

**Files:**
- Modify: `internal/config/config.go` (remove Load, EnvOrDefault helpers)
- Modify: `internal/config/config_test.go` (remove old tests or adapt)

- [ ] **Step 1: Remove Load() and helper functions**

Remove `Load()`, `EnvOrDefault()`, `EnvOrDefaultInt()`,
`EnvOrDefaultDuration()` from `config.go`. Keep `XDGDataDir()`,
`XDGConfigDir()`, `DBPathForProject()`, `DBPathForProjectBase()`, and
`IndexVersion`.

Keep the `BackendOllama` and `BackendLMStudio` constants.

Remove or keep the `Config` struct depending on whether any code still
references it. If `cmd/hook.go` or other commands still use it, keep it
temporarily and mark as deprecated, or migrate those too.

- [ ] **Step 2: Update or remove old tests**

Adapt `config_test.go` — remove tests for `Load()` that are now covered by
`service_test.go` and `compat_test.go`.

- [ ] **Step 3: Build and run all tests**

```bash
go build ./...
go test ./... -short
```

Expected: All PASS with no dead code.

- [ ] **Step 4: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "refactor(config): remove old Load() in favor of ConfigService"
```

---

### Task 23: Run full test suite and lint

**Files:** none (verification only)

- [ ] **Step 1: Run full test suite**

```bash
go test ./... -race -v
```

Expected: All PASS.

- [ ] **Step 2: Run linter**

```bash
golangci-lint run
```

Expected: Zero issues.

- [ ] **Step 3: Run format check**

```bash
goimports -l .
```

Expected: No files need formatting.

- [ ] **Step 4: Final commit if any formatting fixes needed**

```bash
git add -A
git commit -m "style: fix formatting"
```
