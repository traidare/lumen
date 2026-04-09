// Copyright 2026 Aeneas Rekkas
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package embedder

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
				_, _ = fmt.Fprint(w, "Ollama is running")
			} else {
				w.WriteHeader(503)
			}
		case r.Method == "POST" && r.URL.Path == "/api/embed":
			w.WriteHeader(embedStatus)
			if embedStatus == 200 {
				_, _ = fmt.Fprint(w, `{"embeddings":[[0.1,0.2,0.3]]}`)
			} else {
				_, _ = fmt.Fprintf(w, `{"error":"status %d"}`, embedStatus)
			}
		default:
			w.WriteHeader(404)
		}
	}))
}

func testConfigService(t *testing.T, servers ...config.ServerConfig) *config.ConfigService {
	t.Helper()
	// Clear env vars that would override the test config servers.
	t.Setenv("LUMEN_BACKEND", "")
	t.Setenv("LUMEN_EMBED_MODEL", "")
	t.Setenv("LUMEN_EMBED_DIMS", "")
	t.Setenv("LUMEN_EMBED_CTX", "")
	t.Setenv("OLLAMA_HOST", "")
	t.Setenv("LM_STUDIO_HOST", "")

	y := "servers:\n"
	for _, s := range servers {
		y += fmt.Sprintf("  - backend: %s\n    host: %s\n    model: %s\n    dims: %d\n",
			s.Backend, s.Host, s.Model, s.Dims)
	}
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgFile, []byte(y), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
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
	_, _ = fe.Embed(context.Background(), []string{"hello"})
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
	_, _ = fe.Embed(context.Background(), []string{"hello"})
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
	// Clear env vars that would override the test config servers.
	t.Setenv("LUMEN_BACKEND", "")
	t.Setenv("LUMEN_EMBED_MODEL", "")
	t.Setenv("LUMEN_EMBED_DIMS", "")
	t.Setenv("LUMEN_EMBED_CTX", "")
	t.Setenv("OLLAMA_HOST", "")
	t.Setenv("LM_STUDIO_HOST", "")

	down := newTestOllamaServer(t, false, 200)
	defer down.Close()
	up := newTestOllamaServer(t, true, 200)
	defer up.Close()

	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgFile, []byte(fmt.Sprintf("servers:\n  - backend: ollama\n    host: %s\n    model: test\n    dims: 3\n", down.URL)), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := config.NewConfigService(cfgFile)
	if err != nil {
		t.Fatalf("NewConfigService: %v", err)
	}
	if err := cfg.Watch(); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	defer cfg.Stop()

	fe := NewFailoverEmbedder(cfg)
	_, err = fe.Embed(context.Background(), []string{"hello"})
	if err == nil {
		t.Fatal("expected error with only down server")
	}

	// Hot reload adds the healthy server
	if err := os.WriteFile(cfgFile, []byte(fmt.Sprintf("servers:\n  - backend: ollama\n    host: %s\n    model: test\n    dims: 3\n  - backend: ollama\n    host: %s\n    model: test\n    dims: 3\n", down.URL, up.URL)), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	time.Sleep(500 * time.Millisecond)

	// serversChanged() now detects the config reload automatically
	_, err = fe.Embed(context.Background(), []string{"hello"})
	if err != nil {
		t.Fatalf("Embed after reload: %v", err)
	}
}

func newTestLMStudioServer(t *testing.T, healthy bool, embedStatus int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/models":
			if healthy {
				w.WriteHeader(200)
				_, _ = fmt.Fprint(w, `{"data":[]}`)
			} else {
				w.WriteHeader(503)
			}
		case r.Method == "POST" && r.URL.Path == "/v1/embeddings":
			w.WriteHeader(embedStatus)
			if embedStatus == 200 {
				_, _ = fmt.Fprint(w, `{"data":[{"embedding":[0.1,0.2,0.3]}]}`)
			} else {
				_, _ = fmt.Fprintf(w, `{"error":"status %d"}`, embedStatus)
			}
		default:
			w.WriteHeader(404)
		}
	}))
}

func TestFailover_PrimaryDown_FallsBackWithLogging(t *testing.T) {
	// Simulate real scenario: remote LM Studio is down, local Ollama is up.
	lmDown := newTestLMStudioServer(t, false, 200)
	defer lmDown.Close()
	ollamaUp := newTestOllamaServer(t, true, 200)
	defer ollamaUp.Close()

	cfg := testConfigService(t,
		config.ServerConfig{Backend: "lmstudio", Host: lmDown.URL, Model: "test-lm", Dims: 3},
		config.ServerConfig{Backend: "ollama", Host: ollamaUp.URL, Model: "test-ollama", Dims: 3},
	)

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	fe := NewFailoverEmbedder(cfg)
	fe.SetLogger(logger)

	result, err := fe.Embed(context.Background(), []string{"hello"})
	if err != nil {
		t.Fatalf("Embed should succeed via fallback, got: %v", err)
	}
	if len(result) == 0 {
		t.Fatal("expected non-empty embeddings")
	}
	if fe.ActiveServerIndex() != 1 {
		t.Errorf("active = %d, want 1 (should have fallen back to ollama)", fe.ActiveServerIndex())
	}

	logs := buf.String()
	if !strings.Contains(logs, "health probe non-200") {
		t.Error("expected log entry for failed health probe on server 0")
	}
	if !strings.Contains(logs, "selected embedding server") {
		t.Error("expected log entry for server selection")
	}
	if !strings.Contains(logs, "test-ollama") {
		t.Error("expected selected server log to mention the ollama model name")
	}
}

func TestFailover_PrimaryUp_SelectsPrimary(t *testing.T) {
	// Both servers are healthy — should select primary (server 0).
	lmUp := newTestLMStudioServer(t, true, 200)
	defer lmUp.Close()
	ollamaUp := newTestOllamaServer(t, true, 200)
	defer ollamaUp.Close()

	cfg := testConfigService(t,
		config.ServerConfig{Backend: "lmstudio", Host: lmUp.URL, Model: "test-lm", Dims: 3},
		config.ServerConfig{Backend: "ollama", Host: ollamaUp.URL, Model: "test-ollama", Dims: 3},
	)

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	fe := NewFailoverEmbedder(cfg)
	fe.SetLogger(logger)

	_, err := fe.Embed(context.Background(), []string{"hello"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if fe.ActiveServerIndex() != 0 {
		t.Errorf("active = %d, want 0 (primary should be selected)", fe.ActiveServerIndex())
	}

	logs := buf.String()
	if !strings.Contains(logs, "selected embedding server") {
		t.Error("expected log entry for server selection")
	}
	if !strings.Contains(logs, "test-lm") {
		t.Error("expected selected server log to mention the lmstudio model name")
	}
	if strings.Contains(logs, "health probe failed") || strings.Contains(logs, "health probe non-200") {
		t.Error("no health probe warnings expected when primary is up")
	}
}

func TestFailover_AllDown_LogsWarning(t *testing.T) {
	lmDown := newTestLMStudioServer(t, false, 200)
	defer lmDown.Close()
	ollamaDown := newTestOllamaServer(t, false, 200)
	defer ollamaDown.Close()

	cfg := testConfigService(t,
		config.ServerConfig{Backend: "lmstudio", Host: lmDown.URL, Model: "test-lm", Dims: 3},
		config.ServerConfig{Backend: "ollama", Host: ollamaDown.URL, Model: "test-ollama", Dims: 3},
	)

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	fe := NewFailoverEmbedder(cfg)
	fe.SetLogger(logger)

	_, err := fe.Embed(context.Background(), []string{"hello"})
	if err == nil {
		t.Fatal("expected error when all servers are down")
	}

	logs := buf.String()
	if !strings.Contains(logs, "no healthy embedding server found") {
		t.Error("expected 'no healthy embedding server found' warning")
	}
	// Should have two health probe warnings (one per server)
	if count := strings.Count(logs, "health probe non-200"); count != 2 {
		t.Errorf("expected 2 health probe warnings, got %d", count)
	}
}

func TestFailover_PrimaryFailsDuringEmbed_LogsFailover(t *testing.T) {
	// Primary is healthy but returns 500 on embed; should failover with logging.
	srv1 := newTestOllamaServer(t, true, 500)
	defer srv1.Close()
	srv2 := newTestOllamaServer(t, true, 200)
	defer srv2.Close()

	cfg := testConfigService(t,
		config.ServerConfig{Backend: "ollama", Host: srv1.URL, Model: "test-a", Dims: 3},
		config.ServerConfig{Backend: "ollama", Host: srv2.URL, Model: "test-b", Dims: 3},
	)

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	fe := NewFailoverEmbedder(cfg)
	fe.SetLogger(logger)

	_, err := fe.Embed(context.Background(), []string{"hello"})
	if err != nil {
		t.Fatalf("Embed should succeed via failover, got: %v", err)
	}
	if fe.ActiveServerIndex() != 1 {
		t.Errorf("active = %d, want 1", fe.ActiveServerIndex())
	}

	logs := buf.String()
	if !strings.Contains(logs, "embedding server failed, trying next") {
		t.Error("expected failover log entry")
	}
}

func TestFailover_Unreachable_LogsConnectionError(t *testing.T) {
	// Server 0 is unreachable (closed immediately), server 1 is healthy.
	unreachable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	unreachable.Close() // close immediately to make it unreachable

	up := newTestOllamaServer(t, true, 200)
	defer up.Close()

	cfg := testConfigService(t,
		config.ServerConfig{Backend: "ollama", Host: unreachable.URL, Model: "dead", Dims: 3},
		config.ServerConfig{Backend: "ollama", Host: up.URL, Model: "alive", Dims: 3},
	)

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	fe := NewFailoverEmbedder(cfg)
	fe.SetLogger(logger)

	_, err := fe.Embed(context.Background(), []string{"hello"})
	if err != nil {
		t.Fatalf("Embed should succeed via fallback, got: %v", err)
	}
	if fe.ActiveServerIndex() != 1 {
		t.Errorf("active = %d, want 1", fe.ActiveServerIndex())
	}

	logs := buf.String()
	if !strings.Contains(logs, "health probe failed") {
		t.Error("expected 'health probe failed' with connection error for unreachable server")
	}
	if !strings.Contains(logs, "selected embedding server") {
		t.Error("expected server selection log")
	}
}
