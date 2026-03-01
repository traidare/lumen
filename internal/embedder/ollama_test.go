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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type mockOllamaResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
	Model      string      `json:"model"`
}

func TestOllamaEmbedder_Embed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		resp := mockOllamaResponse{
			Model: "nomic-embed-text",
			Embeddings: [][]float32{
				{0.1, 0.2, 0.3, 0.4},
				{0.5, 0.6, 0.7, 0.8},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	e, err := NewOllama("nomic-embed-text", 4, 0, server.URL)
	if err != nil {
		t.Fatal(err)
	}

	vecs, err := e.Embed(context.Background(), []string{"hello", "world"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 2 {
		t.Fatalf("expected 2 vectors, got %d", len(vecs))
	}
	if len(vecs[0]) != 4 {
		t.Fatalf("expected 4 dimensions, got %d", len(vecs[0]))
	}
}

func TestOllamaEmbedder_NumCtx(t *testing.T) {
	var receivedOptions map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if opts, ok := req["options"]; ok {
			receivedOptions = opts.(map[string]any)
		}
		resp := mockOllamaResponse{
			Model:      "test",
			Embeddings: [][]float32{{0.1, 0.2, 0.3, 0.4}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// With contextLength=0, no options should be sent.
	e, _ := NewOllama("test", 4, 0, server.URL)
	_, _ = e.Embed(context.Background(), []string{"hello"})
	if receivedOptions != nil {
		t.Fatalf("expected no options with contextLength=0, got %v", receivedOptions)
	}

	// With contextLength=8192, num_ctx should be set.
	receivedOptions = nil
	e, _ = NewOllama("test", 4, 8192, server.URL)
	_, _ = e.Embed(context.Background(), []string{"hello"})
	if receivedOptions == nil {
		t.Fatal("expected options with contextLength=8192")
	}
	if numCtx, ok := receivedOptions["num_ctx"]; !ok || numCtx != float64(8192) {
		t.Fatalf("expected num_ctx=8192, got %v", receivedOptions["num_ctx"])
	}
}

func TestOllamaEmbedder_Dimensions(t *testing.T) {
	e, _ := NewOllama("nomic-embed-text", 1024, 0, "http://localhost:11434")
	if e.Dimensions() != 1024 {
		t.Fatalf("expected 1024, got %d", e.Dimensions())
	}
}

func TestOllamaEmbedder_ModelName(t *testing.T) {
	e, _ := NewOllama("nomic-embed-text", 1024, 0, "http://localhost:11434")
	if e.ModelName() != "nomic-embed-text" {
		t.Fatalf("expected nomic-embed-text, got %s", e.ModelName())
	}
}

func TestOllamaEmbedder_Batching(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		input := req["input"].([]any)

		embeddings := make([][]float32, len(input))
		for i := range input {
			embeddings[i] = []float32{0.1, 0.2, 0.3, 0.4}
		}
		resp := mockOllamaResponse{Model: "test", Embeddings: embeddings}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	e, _ := NewOllama("test", 4, 0, server.URL)
	texts := make([]string, 50)
	for i := range texts {
		texts[i] = "text"
	}

	vecs, err := e.Embed(context.Background(), texts)
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 50 {
		t.Fatalf("expected 50 vectors, got %d", len(vecs))
	}
	if callCount != 2 {
		t.Fatalf("expected 2 batch calls (32+18), got %d", callCount)
	}
}

func TestOllamaEmbedder_ErrorHandling(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	e, _ := NewOllama("test", 4, 0, server.URL)
	_, err := e.Embed(context.Background(), []string{"hello"})
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestOllama_Embed_ContextCancelledStopsRetry(t *testing.T) {
	// Server always returns 500 to force retry attempts.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	emb, _ := NewOllama("test", 4, 0, srv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before any request

	start := time.Now()
	_, err := emb.Embed(ctx, []string{"hello"})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	// With context-aware backoff, should return almost immediately.
	// The old time.Sleep ignores context — this test catches the regression.
	if elapsed > 500*time.Millisecond {
		t.Fatalf("expected fast failure on pre-cancelled context, took %v", elapsed)
	}
}
