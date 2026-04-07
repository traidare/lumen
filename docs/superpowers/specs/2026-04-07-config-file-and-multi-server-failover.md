# Config File and Multi-Server Failover

## Problem

Lumen configuration is entirely environment-variable driven. This makes it
painful to manage when running multiple code agents with different embedding
backends. There is no way to define multiple upstream embedding servers or fail
over between them when one is unhealthy.

## Goals

1. Replace env-var-only config with a YAML config file at
   `$XDG_CONFIG_HOME/lumen/config.yaml` (default `~/.config/lumen/config.yaml`)
2. Support an ordered list of embedding servers with automatic health-check-based
   failover
3. Retain full backward compatibility with existing env vars as overrides
4. Support hot reload of the config file for long-running processes (MCP server)

## Non-Goals

- Per-project config files (out of scope, global config only)
- Automatic model pulling/installation
- Load balancing across healthy servers (use first healthy, not round-robin)
- `lumen config init` scaffolding command (follow-up work)

## Config File Format

```yaml
max_chunk_tokens: 512
freshness_ttl: 60s
reindex_timeout: 5m
log_level: info

servers:
  - backend: ollama
    host: http://localhost:11434
    model: ordis/jina-embeddings-v2-base-code
  - backend: lmstudio
    host: http://localhost:1234
    model: nomic-ai/nomic-embed-code-GGUF
  - backend: ollama
    host: http://gpu-box:11434
    model: ordis/jina-embeddings-v2-base-code
    dims: 768
    ctx_length: 8192
    min_score: 0.35
```

Per-server fields `dims`, `ctx_length`, and `min_score` are optional. When
omitted, values are resolved from the `KnownModels` registry. When set, they
override the registry. If neither the config nor `KnownModels` provides `dims`
for a model, that is a config error.

`min_score` is a search-time threshold, not an embedding-time parameter. It is
defined per-server because different models produce different cosine similarity
distributions — the threshold must match the active server's model at search
time.

Global settings (`max_chunk_tokens`, `freshness_ttl`, `reindex_timeout`,
`log_level`) apply to the entire process regardless of which server is active.

## Config Loading with koanf

Use the `koanf/v2` library for layered config management. koanf merges multiple
providers in order, with later providers overriding earlier ones:

1. **Hardcoded defaults** — `max_chunk_tokens: 512`, `freshness_ttl: 60s`,
   `reindex_timeout: 0` (disabled), `log_level: info`, single default server
   (ollama, localhost:11434, jina model)
2. **YAML file** — `$XDG_CONFIG_HOME/lumen/config.yaml` (optional, not an error
   if missing)
3. **Environment variables** — `LUMEN_` prefix, override everything

### Prerequisites

A new `XDGConfigDir()` helper is needed in `internal/config/` (analogous to the
existing `XDGDataDir()`). It returns `$XDG_CONFIG_HOME` if set, otherwise
`~/.config`.

### ConfigService

koanf is wrapped in a `ConfigService` that is passed as a dependency to all
components. There is no intermediate `Config` struct populated at startup.
Components read config values directly from `ConfigService` when they need them:

```go
type ConfigService struct {
    k  *koanf.Koanf
    mu sync.RWMutex // protects reloads
}

// Global settings
svc.MaxChunkTokens()    // reads "max_chunk_tokens"
svc.FreshnessTTL()      // reads "freshness_ttl"
svc.LogLevel()          // reads "log_level"
svc.ReindexTimeout()    // reads "reindex_timeout"

// Server list
svc.Servers()           // reads "servers" slice

// Per-server model properties (koanf first, KnownModels fallback)
svc.ServerDims(i)       // reads "servers.{i}.dims", falls back to KnownModels
svc.ServerCtxLength(i)  // reads "servers.{i}.ctx_length", falls back to KnownModels
svc.ServerMinScore(i)   // reads "servers.{i}.min_score", falls back to KnownModels
```

**Type coercion**: koanf's env provider reads all values as strings. The
`ConfigService` accessor methods handle parsing: `MaxChunkTokens()` parses
string to int, `FreshnessTTL()` parses string to `time.Duration`, etc. The YAML
provider returns native types, so coercion only applies to the env layer.

### Environment Variable Mapping

Legacy env vars map into koanf's key space as the first server entry:

| Env Var              | koanf Key            |
| -------------------- | -------------------- |
| `LUMEN_BACKEND`      | `servers.0.backend`  |
| `LUMEN_EMBED_MODEL`  | `servers.0.model`    |
| `OLLAMA_HOST`        | `servers.0.host` (only when `LUMEN_BACKEND=ollama` or unset) |
| `LM_STUDIO_HOST`     | `servers.0.host` (only when `LUMEN_BACKEND=lmstudio`) |
| `LUMEN_EMBED_DIMS`   | `servers.0.dims`     |
| `LUMEN_EMBED_CTX`    | `servers.0.ctx_length` |

**Host env var conflict resolution**: When both `OLLAMA_HOST` and
`LM_STUDIO_HOST` are set, `LUMEN_BACKEND` (or the default `ollama`) determines
which host var maps to `servers.0.host`. The other is ignored. This matches
current behavior where each host var only matters for its respective backend.

Global env vars map directly:

| Env Var                  | koanf Key          |
| ------------------------ | ------------------ |
| `LUMEN_MAX_CHUNK_TOKENS` | `max_chunk_tokens` |
| `LUMEN_FRESHNESS_TTL`    | `freshness_ttl`    |
| `LUMEN_REINDEX_TIMEOUT`  | `reindex_timeout`  |
| `LUMEN_LOG_LEVEL`        | `log_level`        |

When legacy env vars are set, they replace the first server entry in the merged
config. The remaining file-defined servers (index 1+) are preserved.

### No Config File, No Env Vars

Falls back to hardcoded defaults: single server with ollama, localhost:11434,
`ordis/jina-embeddings-v2-base-code`. Zero-config still works identically to
today.

### Validation

Config is validated eagerly at load time (and after each hot reload):

- **Required server fields**: each server entry must have `backend`, `host`, and
  `model`. Missing any of these is a config error.
- **Backend values**: must be `"ollama"` or `"lmstudio"`. Unknown backend is a
  config error.
- **Dims resolution**: for each server, dims must be resolvable (from config or
  `KnownModels`). Unresolvable dims is a config error.
- **Empty server list**: if `servers` is present but empty, that is a config
  error. Omitting `servers` entirely falls back to defaults.
- **Host format**: must be a valid URL with scheme (http/https).

Validation errors at startup are fatal. Validation errors during hot reload are
logged as warnings and the previous valid config is retained.

## Hot Reload

`ConfigService` watches the YAML file for changes using koanf's file watcher.

- On file change: reload YAML provider, re-merge layers (defaults -> file ->
  env). Env vars still win after reload.
- Thread safety: `sync.RWMutex` around reload; reads acquire read lock.
- Consumers always read live values from `ConfigService` — no
  notification/callback mechanism needed.

**Watcher lifecycle**:

- **CLI commands** (short-lived): no watcher, load once and exit.
- **MCP server** (`lumen stdio`): watcher starts at server init, stops at
  shutdown.

**What reload affects**: server list, global settings, per-server overrides.

**What reload does not affect mid-operation**: an in-progress indexing run keeps
its current embedder. The active server does not switch on reload — only on next
failover event or next command invocation.

**FailoverEmbedder and reload**: `FailoverEmbedder` re-reads the server list
from `ConfigService` on the next failover event after a reload. It does not
re-read on every `Embed()` call (no overhead on the hot path). If the server
list changed, it rebuilds its internal `[]serverEntry` and resets health state.

## Server Health Check and Failover

### Health Probes

| Backend   | Endpoint        | Expected Response              |
| --------- | --------------- | ------------------------------ |
| Ollama    | `GET /`         | 200, body: "Ollama is running" |
| LM Studio | `GET /v1/models`| 200, JSON model list           |

Health check timeout: 5 seconds.

These probes verify server liveness only, not model availability. Model-not-found
errors (4xx from the embedding API) surface on first `Embed()` call as config
errors, not failover triggers.

### FailoverEmbedder

A `FailoverEmbedder` wraps multiple backend embedders and implements the
existing `Embedder` interface. It receives `*ConfigService` and reads the server
list from koanf.

```go
type FailoverEmbedder struct {
    cfg     *config.ConfigService
    servers []serverEntry  // lazily initialized from config
    active  int
    checked bool
}

type serverEntry struct {
    idx     int              // index in config server list
    emb     embedder.Embedder // nil until first use
    healthy bool
}
```

**Failover behavior**:

1. On first `Embed()` call, probe servers top-to-bottom. Pick the first healthy
   one. Lazily instantiate only that server's embedder.
2. Subsequent calls go to the active server directly — no probing.
3. If an `Embed()` call fails (network error, 5xx), mark the server unhealthy
   and try the next one. Lazily instantiate its embedder if needed.
4. If all servers are exhausted, return an error listing what was tried.
5. HTTP 4xx errors (model not found, bad request) are **not** failover triggers.
   These indicate config errors and surface immediately.

**`Dimensions()` and `ModelName()`**: These methods always reflect the currently
active server. `Dimensions()` reads from `ConfigService.ServerDims(active)`,
which resolves via koanf then KnownModels fallback. `ModelName()` reads
`ConfigService` for the active server's model. Before the first `Embed()` call
(before health check), these return values for server index 0 (the first/default
server). On failover, they reflect the new active server.

**Index implications**: Failing over between servers with different models means
the DB path changes (it is hashed from model name). Lumen already handles
multiple indexes per project. If the fallback server uses a different model whose
index does not exist yet, indexing is triggered. The user accepts this trade-off
by putting different models in their server list. Callers that cache by DB path
(like `indexerCache` in the MCP server) already key on project path + model,
so they naturally get separate cache entries per model.

### --model CLI Flag

Filters the server list to only servers with the matching model. If no server
has that model, return an error. Failover only happens among the filtered
servers. Filtering is implemented inside `ConfigService` as a
`ServersForModel(model)` method that returns the filtered indices. If the model
is not in `KnownModels` but a server entry provides explicit `dims`, that is
valid.

## Testing Strategy

All work follows red/green TDD: write a failing test first, then implement to
make it pass. Tests are grouped into three phases, each building on the previous.

### Phase 1: ConfigService Unit Tests

Tests in `internal/config/config_service_test.go`. Each test creates a
`ConfigService` with controlled koanf state (in-memory providers, temp YAML
files, env vars via `t.Setenv`). No network, no real servers.

| Test | Red (what fails) | Green (what to implement) |
|------|-------------------|---------------------------|
| `TestDefaults_NoFileNoEnv` | `svc.MaxChunkTokens()` returns 0, `svc.Servers()` is empty | Hardcoded default provider with global defaults and single default server |
| `TestYAMLOverridesDefaults` | Write temp YAML with `max_chunk_tokens: 1024` and two servers. `svc.MaxChunkTokens()` returns 512 (default) | YAML file provider loading |
| `TestEnvOverridesYAML` | Set `LUMEN_MAX_CHUNK_TOKENS=2048` with YAML setting 1024. Returns 1024 | Env var provider layer on top of YAML |
| `TestEnvServerMapping_Ollama` | Set `LUMEN_BACKEND=ollama`, `OLLAMA_HOST=http://x:1111`, `LUMEN_EMBED_MODEL=foo`. `svc.Servers()[0]` wrong | Env-to-server mapping logic |
| `TestEnvServerMapping_LMStudio` | Set `LUMEN_BACKEND=lmstudio`, `LM_STUDIO_HOST=http://y:2222`. `svc.Servers()[0].Host` wrong | Backend-aware host var resolution |
| `TestHostConflict_BothSet` | Set both `OLLAMA_HOST` and `LM_STUDIO_HOST` with `LUMEN_BACKEND=lmstudio`. Wrong host selected | Conflict resolution: backend determines which host var wins |
| `TestDimsFallback_KnownModel` | Server has known model but no `dims` in config. `svc.ServerDims(0)` returns 0 | KnownModels fallback in accessor |
| `TestDimsExplicit_OverridesKnown` | Server has known model and `dims: 1024` in config. Returns model default instead of 1024 | Config-first, KnownModels-second resolution |
| `TestDimsUnresolvable_Error` | Server has unknown model, no `dims` in config. No error returned | Validation: unresolvable dims is config error |
| `TestServersForModel_Filters` | Call `svc.ServersForModel("foo")` with mixed server list. Returns wrong indices | Filtering method |
| `TestServersForModel_NoMatch` | Call with model not in any server. No error | Error on no matching servers |

**Validation tests** (same file):

| Test | Red | Green |
|------|-----|-------|
| `TestValidation_MissingBackend` | Config with server missing `backend` loads without error | Eager validation at load time |
| `TestValidation_UnknownBackend` | Config with `backend: foo` loads without error | Backend value check |
| `TestValidation_EmptyServerList` | Config with `servers: []` loads without error | Empty list rejection |
| `TestValidation_InvalidHost` | Config with `host: not-a-url` loads without error | URL format validation |
| `TestValidation_MissingModel` | Config with server missing `model` loads without error | Required field check |

### Phase 2: Hot Reload Integration Tests

Tests in `internal/config/config_reload_test.go`. These use real temp files on
disk and real file watchers. No mocks for the file system.

| Test | Red | Green |
|------|-----|-------|
| `TestReload_FileChange` | Write temp YAML, create `ConfigService` with watcher. Modify YAML (change `max_chunk_tokens`). Read value — still old | File watcher + re-merge implementation |
| `TestReload_EnvStillWins` | Set env var, start watcher. Modify YAML to different value. Env var value lost | Re-merge preserves env layer on top |
| `TestReload_InvalidRetainsPrevious` | Start with valid YAML. Overwrite with invalid YAML (missing required field). Config reverts to invalid | Validation on reload, retain previous on failure |
| `TestReload_ServerListChange` | Start with 2 servers. Reload with 3 servers. `svc.Servers()` still returns 2 | Server list re-read after reload |
| `TestReload_ConcurrentReads` | Spawn 100 goroutines reading config while triggering reloads. Race detector catches data race | RWMutex correctness (run with `-race`) |

**Timing**: hot reload tests use a polling loop with short deadline (2s) rather
than `time.Sleep`, to avoid flaky timing-dependent tests. The test reads the
config value in a loop until it changes or the deadline expires.

### Phase 3: FailoverEmbedder Integration Tests

Tests in `internal/embedder/failover_test.go`. These spin up real
`httptest.Server` instances to simulate Ollama and LM Studio backends. No mocks
for HTTP — real TCP connections, real health probes.

| Test | Red | Green |
|------|-----|-------|
| `TestFailover_FirstHealthy` | Create 3 test servers (down, up, up). `Embed()` hits server 2 instead of trying in order | Health probe + ordered selection |
| `TestFailover_OnEmbedError` | Server 1 healthy, returns 500 on `Embed()`. Server 2 healthy. Second call still goes to server 1 | Failover on embed failure |
| `TestFailover_4xxNoFailover` | Server 1 returns 400 on `Embed()`. `Embed()` tries server 2 | 4xx surfaced as config error, no failover |
| `TestFailover_AllExhausted` | All servers down. `Embed()` returns generic error | Exhaustion error with details of what was tried |
| `TestFailover_DimensionsReflectActive` | Server 1 (model A, 768d) down, server 2 (model B, 1024d) up. `Dimensions()` returns 768 | Dimensions()/ModelName() track active server |
| `TestFailover_SingleServer` | One server, healthy. Works identically to non-failover path | No special-case code path for single server |
| `TestFailover_LazyInit` | 3 servers, first is healthy. Only first server's embedder is instantiated | Lazy instantiation — verify other servers' embedders are nil |
| `TestFailover_ReloadPicksUpNewServers` | Start with 1 server (down). Hot reload adds server 2 (up). Next `Embed()` still fails | FailoverEmbedder re-reads server list from ConfigService on failover |

### Backward Compatibility Tests

Tests in `internal/config/config_compat_test.go`. These verify that the new
system produces identical behavior to the current env-var-only implementation
for all existing usage patterns.

| Test | Red | Green |
|------|-----|-------|
| `TestCompat_ZeroConfig` | New `ConfigService` with no file, no env vars. Compare output to current `config.Load()` defaults | Default provider matches current hardcoded defaults |
| `TestCompat_AllEnvVars` | Set all current env vars. Compare new `ConfigService` output field-by-field to current `config.Load()` | Env mapping produces identical values |
| `TestCompat_ModelFlagOverride` | Set `--model` flag equivalent. Compare behavior to current `applyModelFlag()` | ServersForModel produces same filtering |
