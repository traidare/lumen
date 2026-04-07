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
	svc, err := NewConfigService("")
	if err != nil {
		t.Fatalf("NewConfigService: %v", err)
	}
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
	svc, err := NewConfigService("")
	if err != nil {
		t.Fatalf("NewConfigService: %v", err)
	}
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
	for _, key := range []string{
		"LUMEN_BACKEND", "LUMEN_EMBED_MODEL", "LUMEN_EMBED_DIMS",
		"LUMEN_EMBED_CTX", "OLLAMA_HOST", "LM_STUDIO_HOST",
	} {
		t.Setenv(key, "")
	}
	svc, err := NewConfigService("")
	if err != nil {
		t.Fatalf("NewConfigService: %v", err)
	}
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

func TestCompat_BackendOnlyOverrideUsesBackendDefaults(t *testing.T) {
	t.Setenv("LUMEN_BACKEND", "lmstudio")
	t.Setenv("LUMEN_EMBED_MODEL", "")
	t.Setenv("LM_STUDIO_HOST", "")
	t.Setenv("OLLAMA_HOST", "")

	svc, err := NewConfigService("")
	if err != nil {
		t.Fatalf("NewConfigService: %v", err)
	}

	s := svc.Servers()[0]
	if s.Backend != BackendLMStudio {
		t.Errorf("Backend = %q, want %q", s.Backend, BackendLMStudio)
	}
	if s.Host != "http://localhost:1234" {
		t.Errorf("Host = %q, want %q", s.Host, "http://localhost:1234")
	}
	if s.Model != models.DefaultLMStudioModel {
		t.Errorf("Model = %q, want %q", s.Model, models.DefaultLMStudioModel)
	}
}
