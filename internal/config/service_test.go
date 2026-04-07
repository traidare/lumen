package config

import (
	"testing"
	"time"
)

func TestDefaults_NoFileNoEnv(t *testing.T) {
	// Clear env vars that would override defaults to test clean-slate behaviour.
	for _, key := range []string{
		"LUMEN_BACKEND", "LUMEN_EMBED_MODEL", "LUMEN_EMBED_DIMS", "LUMEN_EMBED_CTX",
		"OLLAMA_HOST", "LM_STUDIO_HOST",
		"LUMEN_MAX_CHUNK_TOKENS", "LUMEN_FRESHNESS_TTL", "LUMEN_REINDEX_TIMEOUT", "LUMEN_LOG_LEVEL",
	} {
		t.Setenv(key, "")
	}
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
