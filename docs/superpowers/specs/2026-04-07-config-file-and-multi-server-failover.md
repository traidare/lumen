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

- **ConfigService unit tests**: verify layering (defaults < file < env), verify
  KnownModels fallback for dims/ctx_length/min_score, verify env var mapping to
  server entries, verify host env var conflict resolution based on backend
- **Validation tests**: missing required fields, unknown backend, unresolvable
  dims, empty server list, invalid host format
- **Hot reload tests**: verify file change triggers re-merge, verify env vars
  still win after reload, verify invalid reload retains previous config
- **FailoverEmbedder unit tests**: mock health endpoints, verify ordered
  failover, verify 4xx does not trigger failover, verify all-exhausted error,
  verify Dimensions()/ModelName() reflect active server
- **Integration tests**: real koanf loading from temp YAML files with env var
  overrides
- **Backward compat tests**: verify zero-config defaults match current behavior,
  verify legacy env vars produce identical config to today
