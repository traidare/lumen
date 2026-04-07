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
