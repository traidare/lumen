# E2E Tests: Config, Hot Reload, and Failover

**Date:** 2026-04-07
**Status:** Approved

## Problem

PR #109 (config-home) introduces ConfigService with YAML config, hot reload via
fsnotify, and FailoverEmbedder with multi-server health-check failover. Unit and
integration tests exist for both features, but no e2e test exercises them through
the MCP server subprocess — the actual production code path.

## Approach

New file `e2e_config_test.go` with `//go:build e2e`, following the existing e2e
test patterns. Tests launch the MCP server as a subprocess with controlled
`XDG_CONFIG_HOME` and `XDG_DATA_HOME`, write config.yaml files, and verify
behavior via MCP tool calls.

A new `startServerWithEnv` helper extends the existing `startServer` pattern to
accept a custom environment slice, enabling config.yaml-driven tests alongside
the existing env-var-driven ones.

For failover tests, `net/http/httptest` servers simulate unhealthy backends
(returning 503 or closing connections). The real Ollama instance serves as the
healthy fallback.

## Test Scenarios

### Happy Paths

1. **Config YAML drives server selection** — Write config.yaml with single
   Ollama server pointing to real Ollama. Set `XDG_CONFIG_HOME` to temp dir.
   Start MCP server, call `semantic_search`. Verify results are returned (index
   created, search works).

2. **Multi-server failover** — config.yaml lists two servers: first is an
   httptest server returning 503 on health check, second is real Ollama. Call
   `semantic_search`. Verify search succeeds transparently (failover happened).

3. **Hot reload changes chunk config** — Start server with
   `max_chunk_tokens: 100`. Index a project via `semantic_search`. Rewrite
   config.yaml to `max_chunk_tokens: 50`. Wait for fsnotify. Call
   `semantic_search` again on a modified file (trigger re-index). Verify chunk
   count changed (more chunks with smaller token limit).

### Edge Cases

4. **Env vars override config.yaml** — config.yaml sets model to a
   non-existent model name. Env var `LUMEN_EMBED_MODEL` overrides to real model.
   Verify search works (env wins).

5. **Invalid config reload preserves previous** — Start with valid config. Hot
   reload to `servers: []` (invalid). Verify search still works with original
   config (no crash, no degradation).

6. **No config file, env vars only** — Don't create config.yaml. Set server
   config via env vars only. Verify search works (backward compatibility with
   pre-ConfigService behavior).

### Unhappy Paths

7. **All servers unhealthy** — config.yaml lists only httptest servers that
   return 503. Call `semantic_search`. Verify the tool returns an error message
   (not a crash or hang).

8. **Unknown backend in config** — config.yaml with `backend: foobar`. Verify
   the MCP server process exits with a non-zero status (validation rejects
   invalid backend at startup).

## Implementation Notes

- `startServerWithEnv(t, env []string, opts *mcp.ClientOptions)` creates the
  subprocess with the given env slice instead of the hardcoded one in
  `startServerWithOpts`.
- Config YAML temp files use `t.TempDir()` for `XDG_CONFIG_HOME`, with the
  config at `<tmpdir>/lumen/config.yaml`.
- Hot reload tests write to the config file and poll with a deadline (similar to
  `pollUntil` in reload_test.go) rather than using fixed sleeps.
- Httptest servers are started per-test and their URLs injected into config.yaml.
- The `serverBinary` global from `e2e_test.go` is reused (same build tag, same
  package).
