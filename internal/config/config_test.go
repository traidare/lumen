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

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvOrDefaultInt(t *testing.T) {
	t.Setenv("TEST_DIMS", "384")
	if got := EnvOrDefaultInt("TEST_DIMS", 1024); got != 384 {
		t.Fatalf("got %d, want 384", got)
	}
	if got := EnvOrDefaultInt("TEST_DIMS_UNSET", 1024); got != 1024 {
		t.Fatalf("got %d, want 1024", got)
	}
}

func TestLoad(t *testing.T) {
	tests := []struct {
		name        string
		model       string
		dims        string
		ctx         string
		wantDims    int
		wantCtx     int
		wantErr     bool
	}{
		{
			name:     "known model no overrides",
			model:    "ordis/jina-embeddings-v2-base-code",
			wantDims: 768,
			wantCtx:  8192,
		},
		{
			name:     "unknown model with DIMS only",
			model:    "custom-model",
			dims:     "4096",
			wantDims: 4096,
			wantCtx:  8192,
		},
		{
			name:     "unknown model with DIMS and CTX",
			model:    "custom-model",
			dims:     "4096",
			ctx:      "40960",
			wantDims: 4096,
			wantCtx:  40960,
		},
		{
			name:    "unknown model no DIMS",
			model:   "custom-model",
			wantErr: true,
		},
		{
			name:    "unknown model CTX only no DIMS",
			model:   "custom-model",
			ctx:     "8192",
			wantErr: true,
		},
		{
			name:     "known model DIMS override",
			model:    "ordis/jina-embeddings-v2-base-code",
			dims:     "512",
			wantDims: 512,
			wantCtx:  8192,
		},
		{
			name:     "known model CTX override",
			model:    "ordis/jina-embeddings-v2-base-code",
			ctx:      "16384",
			wantDims: 768,
			wantCtx:  16384,
		},
		{
			name:     "known model both overrides",
			model:    "ordis/jina-embeddings-v2-base-code",
			dims:     "512",
			ctx:      "4096",
			wantDims: 512,
			wantCtx:  4096,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LUMEN_EMBED_MODEL", tc.model)
			if tc.dims != "" {
				t.Setenv("LUMEN_EMBED_DIMS", tc.dims)
			}
			if tc.ctx != "" {
				t.Setenv("LUMEN_EMBED_CTX", tc.ctx)
			}

			cfg, err := Load()
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.Dims != tc.wantDims {
				t.Errorf("Dims: got %d, want %d", cfg.Dims, tc.wantDims)
			}
			if cfg.CtxLength != tc.wantCtx {
				t.Errorf("CtxLength: got %d, want %d", cfg.CtxLength, tc.wantCtx)
			}
		})
	}
}

func TestLoadAliasResolution(t *testing.T) {
	t.Setenv("LUMEN_BACKEND", "lmstudio")
	t.Setenv("LUMEN_EMBED_MODEL", "text-embedding-nomic-embed-code")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Dims != 3584 {
		t.Errorf("Dims: got %d, want 3584", cfg.Dims)
	}
	if cfg.CtxLength != 8192 {
		t.Errorf("CtxLength: got %d, want 8192", cfg.CtxLength)
	}
	// Config.Model must stay as user-configured name — LM Studio API expects
	// "text-embedding-nomic-embed-code", not "nomic-ai/nomic-embed-code-GGUF".
	if cfg.Model != "text-embedding-nomic-embed-code" {
		t.Errorf("Model: got %q, want %q", cfg.Model, "text-embedding-nomic-embed-code")
	}
}

func TestDBPathForProject(t *testing.T) {
	t.Run("deterministic", func(t *testing.T) {
		p1 := DBPathForProject("/home/user/project", "model-a")
		p2 := DBPathForProject("/home/user/project", "model-a")
		if p1 != p2 {
			t.Fatalf("expected same path, got %q and %q", p1, p2)
		}
	})

	t.Run("different project paths produce different hashes", func(t *testing.T) {
		p1 := DBPathForProject("/home/user/project-a", "model-a")
		p2 := DBPathForProject("/home/user/project-b", "model-a")
		if p1 == p2 {
			t.Fatalf("expected different paths, got same: %q", p1)
		}
	})

	t.Run("different models produce different hashes", func(t *testing.T) {
		p1 := DBPathForProject("/home/user/project", "model-a")
		p2 := DBPathForProject("/home/user/project", "model-b")
		if p1 == p2 {
			t.Fatalf("expected different paths, got same: %q", p1)
		}
	})

	t.Run("uses IndexVersion not runtime state", func(t *testing.T) {
		// The path must be stable regardless of build-time variables.
		// We verify this by computing the path twice and confirming stability,
		// and by checking that IndexVersion is a non-empty hardcoded constant.
		if IndexVersion == "" {
			t.Fatal("IndexVersion must not be empty")
		}
		p1 := DBPathForProject("/some/path", "some-model")
		p2 := DBPathForProject("/some/path", "some-model")
		if p1 != p2 {
			t.Fatalf("path not stable: %q vs %q", p1, p2)
		}
	})

	t.Run("ends with index.db", func(t *testing.T) {
		p := DBPathForProject("/some/path", "model")
		if !strings.HasSuffix(p, "index.db") {
			t.Fatalf("expected path to end with index.db, got %q", p)
		}
	})
}

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
