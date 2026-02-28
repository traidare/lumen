# agent-index

[![CI](https://github.com/aeneasr/agent-index-go/actions/workflows/ci.yml/badge.svg)](https://github.com/aeneasr/agent-index-go/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)

A fully local semantic code search engine, exposed as an [MCP](https://modelcontextprotocol.io/) server. Think of it as a self-hosted alternative to cloud-based code vector databases — but everything runs on your machine, embeddings included.

It parses your codebase into semantic chunks (functions, methods, types, interfaces, constants), embeds them via a local Ollama model, stores vectors in SQLite with [sqlite-vec](https://github.com/asg017/sqlite-vec), and exposes semantic search over MCP. Your code never leaves your machine.

Supports **12 language families** with semantic chunking:

| Language | Extensions | Chunking strategy |
|---|---|---|
| Go | `.go` | Native Go AST — functions, methods, types, interfaces, consts, vars |
| TypeScript / TSX | `.ts`, `.tsx` | tree-sitter — functions, classes, interfaces, type aliases, methods |
| JavaScript / JSX | `.js`, `.jsx`, `.mjs` | tree-sitter — functions, classes, methods, generators |
| Python | `.py` | tree-sitter — function definitions, class definitions |
| Rust | `.rs` | tree-sitter — functions, structs, enums, traits, impls, consts |
| Ruby | `.rb` | tree-sitter — methods, singleton methods, classes, modules |
| Java | `.java` | tree-sitter — methods, classes, interfaces, constructors, enums |
| PHP | `.php` | tree-sitter — functions, classes, interfaces, traits, methods |
| C / C++ | `.c`, `.h`, `.cpp`, `.cc`, `.cxx`, `.hpp` | tree-sitter — function definitions, structs, enums, classes |
| Markdown / MDX | `.md`, `.mdx` | Heading-based — each `#` / `##` / `###` section is one chunk |
| YAML | `.yaml`, `.yml` | Key-based — each top-level key and its value block is one chunk |
| JSON | `.json` | Key-based — each top-level key and its value block is one chunk |

## Why

AI coding agents are good at writing code but bad at navigating large codebases. They waste context window tokens reading entire files when they only need one function. Semantic search fixes this — the agent describes what it's looking for in natural language and gets back precise file paths and line ranges.

Cloud-hosted vector databases solve this, but they require sending your code to a third party. agent-index gives you the same capability with everything running locally:

- **Local embeddings** via Ollama (no API keys, no network calls to external services)
- **Local storage** via SQLite + sqlite-vec (no external database)
- **Incremental indexing** via Merkle tree change detection (only re-embeds changed files)
- **Auto-indexing** on search (no manual reindex step)

## Install

**Prerequisites:**

1. [Ollama](https://ollama.com/) installed and running
2. [Go](https://go.dev/) 1.26+

```bash
# Pull the default embedding model
ollama pull ordis/jina-embeddings-v2-base-code

# Install the binary
CGO_ENABLED=1 go install github.com/aeneasr/agent-index@latest
```

> `CGO_ENABLED=1` is required — sqlite-vec compiles from C source.

## Setup with Claude Code

```bash
# Pull the default embedding model
ollama pull ordis/jina-embeddings-v2-base-code

# Add as an MCP server (defaults work out of the box)
claude mcp add --scope user \
  agent-index "$(go env GOPATH)/bin/agent-index" -- stdio
```

To use a different model, set `AGENT_INDEX_EMBED_MODEL` — dims and context are looked up automatically:

```bash
claude mcp remove --scope user agent-index
claude mcp add --scope user \
  -eAGENT_INDEX_EMBED_MODEL=nomic-embed-text \
  agent-index "$(go env GOPATH)/bin/agent-index" -- stdio
```

That's it. Claude Code will now have access to `semantic_search` and `index_status` tools. On the first search against a project, it auto-indexes the codebase.

## MCP Tools

### `semantic_search`

Search indexed code using natural language. Auto-indexes if the index is stale.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `query` | string | yes | Natural language search query |
| `path` | string | yes | Absolute path to the project root |
| `limit` | integer | no | Max results (default: 50) |
| `min_score` | float | no | Minimum score threshold (-1 to 1). Default 0.5. Use -1 to return all results. |
| `force_reindex` | boolean | no | Force full re-index before searching |

Returns file paths, symbol names, line ranges, and similarity scores (0–1).

### `index_status`

Check indexing status without triggering a reindex.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `path` | string | yes | Absolute path to the project root |

## Configuration

All configuration is via environment variables:

| Variable | Default | Description |
|---|---|---|
| `AGENT_INDEX_EMBED_MODEL` | `ordis/jina-embeddings-v2-base-code` | Ollama embedding model (must be in registry) |
| `OLLAMA_HOST` | `http://localhost:11434` | Ollama server URL |

### Supported embedding models

Dimensions and context length are configured automatically per model:

| Model | Dims | Context | Size | Notes |
|---|---|---|---|---|
| `ordis/jina-embeddings-v2-base-code` | 768 | 8192 | ~323MB | Default. Code-optimized |
| `nomic-embed-text` | 768 | 8192 | ~274MB | Fast, good general quality |
| `qwen3-embedding:8b` | 4096 | 32768 | ~4.7GB | High quality, large |
| `all-minilm` | 384 | 512 | ~33MB | Tiny, used in CI |

Switching models creates a separate index automatically — the model name is part of the database path hash, so different models never collide.

## Supported Languages

| Language | Parser | Status |
|---|---|---|
| Go | Native `go/ast` | Primary — thoroughly tested |
| TypeScript / TSX | tree-sitter | Supported |
| JavaScript / JSX | tree-sitter | Supported |
| Python | tree-sitter | Supported |
| Rust | tree-sitter | Supported |
| Ruby | tree-sitter | Supported |
| Java | tree-sitter | Supported |
| C | tree-sitter | Supported |
| C++ | tree-sitter | Supported |

Go uses the native Go AST parser, which produces the most precise chunks and has comprehensive test coverage. All other languages use tree-sitter grammars — they work but have less test coverage and may miss some language-specific constructs.

## How It Works

```
  source files
      │
      ▼
┌─────────────┐     ┌──────────────┐     ┌─────────────────┐
│  Merkle Tree │────▶│  AST         │────▶│  Ollama         │
│  (diff only) │     │  Chunker     │     │  Embeddings     │
└─────────────┘     └──────────────┘     └────────┬────────┘
                                                   │
                                                   ▼
                                          ┌─────────────────┐
                                    ◀─────│  SQLite +        │
                              search      │  sqlite-vec      │
                                          └─────────────────┘
```

1. **Change detection**: SHA-256 Merkle tree identifies added/modified/removed files. If nothing changed, search hits the existing index directly.
2. **AST chunking**: Changed files are parsed into semantic chunks. Go files use the native `go/ast` parser; other languages use tree-sitter grammars. Each function, method, type, interface, and const/var declaration becomes a chunk, including its doc comment.
3. **Embedding**: Chunks are batched (32 at a time) and sent to Ollama for embedding.
4. **Storage**: Vectors and metadata go into SQLite via sqlite-vec with cosine distance. Database lives in `$XDG_DATA_HOME/agent-index/` — your project directory stays clean.
5. **Search**: Query is embedded with the same model, KNN search returns the closest matches.

## Storage

Index databases are stored outside your project:

```
~/.local/share/agent-index/<hash>/index.db
```

Where `<hash>` is derived from the absolute project path and embedding model name. No files are added to your repo, no `.gitignore` modifications needed.

## Benchmarks

We benchmarked Claude Code on a real coding task with and without agent-index. The task was identical across all runs — only the availability of semantic search changed. Results show that agent-index consistently reduces wall-clock time by 60–70%, even though it uses slightly more tokens (the extra tokens come from search results replacing much slower full-file reads).

### With agent-index

| Model | Run 1 | Run 2 | Run 3 | Run 4 | Avg Time | Avg Tokens |
|---|---|---|---|---|---|---|
| Sonnet 4.6 | 57s / 56,154t | 42s / 56,691t | 35s / 59,639t | 37s / 57,226t | **43s** | 57,428 |
| Opus 4.6 | 1m8s / 74,415t | 52s / 71,372t | — | — | **60s** | 72,894 |

### Without agent-index

| Model | Run 1 | Run 2 | Run 3 | Run 4 | Avg Time | Avg Tokens |
|---|---|---|---|---|---|---|
| Sonnet 4.6 | 2m31s / 50,282t | 1m51s / 52,497t | 2m18s / 53,211t | 2m13s / 52,432t | **2m13s** | 52,106 |
| Opus 4.6 | 2m5s / 52,834t | 2m10s / 52,061t | 1m48s / 50,964t | 1m55s / 51,265t | **2m0s** | 51,781 |

### Summary

| Model | Without | With | Speedup | Token Delta |
|---|---|---|---|---|
| Sonnet 4.6 | 2m13s | 43s | **3.1×** | +10% |
| Opus 4.6 | 2m0s | 60s | **2.0×** | +41% |

The token increase is a good trade — agent-index returns precise code snippets instead of forcing the agent to read entire files, so it spends more tokens on useful context and less time on file navigation.

## Building from source

```bash
CGO_ENABLED=1 go build -o agent-index .
```
