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
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/ory/lumen/internal/config"
)

const healthCheckTimeout = 5 * time.Second

// FailoverEmbedder wraps multiple backend embedders with ordered health-check
// failover. It probes servers on first use and fails over on transient errors.
type FailoverEmbedder struct {
	cfg     *config.ConfigService
	mu      sync.Mutex
	servers []serverEntry
	active  int
	checked bool
}

type serverEntry struct {
	idx     int
	emb     Embedder
	healthy bool
}

// NewFailoverEmbedder creates a FailoverEmbedder. Servers are probed lazily on
// first Embed call.
func NewFailoverEmbedder(cfg *config.ConfigService) *FailoverEmbedder {
	return &FailoverEmbedder{cfg: cfg, active: -1}
}

// ActiveServerIndex returns the index of the currently active server, or -1
// if no healthy server has been found yet.
func (f *FailoverEmbedder) ActiveServerIndex() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.active
}

// Dimensions returns dims for the active server (or server 0 before first
// Embed call).
func (f *FailoverEmbedder) Dimensions() int {
	f.mu.Lock()
	idx := f.active
	f.mu.Unlock()
	if idx < 0 {
		idx = 0
	}
	return f.cfg.ServerDims(idx)
}

// ModelName returns the model name for the active server.
func (f *FailoverEmbedder) ModelName() string {
	f.mu.Lock()
	idx := f.active
	f.mu.Unlock()
	if idx < 0 {
		idx = 0
	}
	servers := f.cfg.Servers()
	if idx < len(servers) {
		return servers[idx].Model
	}
	return ""
}

// Embed generates embeddings for the given texts. On first call it probes
// all servers for health and selects the first healthy one. On transient
// errors (5xx, network) it fails over to the next healthy server.
func (f *FailoverEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	f.mu.Lock()
	if !f.checked {
		f.initServers()
		f.checked = true
	}
	f.mu.Unlock()

	if f.active < 0 {
		return nil, fmt.Errorf("all embedding servers are unhealthy")
	}

	for {
		f.mu.Lock()
		active := f.active
		if active < 0 || active >= len(f.servers) {
			f.mu.Unlock()
			return nil, fmt.Errorf("all embedding servers exhausted")
		}
		emb := f.servers[active].emb
		f.mu.Unlock()

		result, err := emb.Embed(ctx, texts)
		if err == nil {
			return result, nil
		}

		if !isTransientError(err) {
			return nil, err // 4xx = config error, don't failover
		}

		// Mark current as unhealthy, find next
		f.mu.Lock()
		f.servers[active].healthy = false
		next := f.findNextHealthy(active)
		if next < 0 {
			f.mu.Unlock()
			return nil, fmt.Errorf("all embedding servers exhausted after failover: last error: %w", err)
		}
		f.active = next
		if err := f.ensureEmbedder(next); err != nil {
			f.mu.Unlock()
			return nil, fmt.Errorf("failed to initialize fallback server %d: %w", next, err)
		}
		f.mu.Unlock()
		// Loop continues with new active server
	}
}

// initServers initializes the server list and probes for the first healthy
// server. Must be called with f.mu held.
func (f *FailoverEmbedder) initServers() {
	servers := f.cfg.Servers()
	f.servers = make([]serverEntry, len(servers))
	for i := range servers {
		f.servers[i] = serverEntry{idx: i}
	}
	for i := range f.servers {
		if f.probeHealth(i) {
			f.servers[i].healthy = true
			if err := f.ensureEmbedder(i); err == nil {
				f.active = i
				return
			}
			// ensureEmbedder failed (e.g. unknown backend); try next server
		}
	}
}

// findNextHealthy probes servers after index `after` and returns the first
// healthy one, or -1 if none found. Must be called with f.mu held.
func (f *FailoverEmbedder) findNextHealthy(after int) int {
	for i := after + 1; i < len(f.servers); i++ {
		if f.probeHealth(i) {
			f.servers[i].healthy = true
			return i
		}
	}
	return -1
}

// probeHealth checks if server i is healthy by sending a GET request.
// For Ollama: GET / (returns "Ollama is running").
// For LM Studio: GET /v1/models.
// Returns true only if StatusCode == 200.
func (f *FailoverEmbedder) probeHealth(i int) bool {
	servers := f.cfg.Servers()
	if i >= len(servers) {
		return false
	}
	srv := servers[i]
	endpoint := srv.Host + "/"
	if srv.Backend == "lmstudio" {
		endpoint = srv.Host + "/v1/models"
	}
	client := &http.Client{Timeout: healthCheckTimeout}
	resp, err := client.Get(endpoint)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode == http.StatusOK
}

// ensureEmbedder lazily initializes the backend embedder for server i.
// Must be called with f.mu held.
func (f *FailoverEmbedder) ensureEmbedder(i int) error {
	if f.servers[i].emb != nil {
		return nil
	}
	servers := f.cfg.Servers()
	srv := servers[i]
	dims := f.cfg.ServerDims(i)
	ctxLen := f.cfg.ServerCtxLength(i)

	var emb Embedder
	var err error
	switch srv.Backend {
	case "ollama":
		emb, err = NewOllama(srv.Model, dims, ctxLen, srv.Host)
	case "lmstudio":
		emb, err = NewLMStudio(srv.Model, dims, srv.Host)
	default:
		return fmt.Errorf("unknown backend %q", srv.Backend)
	}
	if err != nil {
		return err
	}
	f.servers[i].emb = emb
	return nil
}

// isTransientError returns true if the error represents a transient failure
// that warrants failover (5xx HTTP errors or network errors). Returns false
// for 4xx errors (configuration errors, no failover).
func isTransientError(err error) bool {
	var ee *EmbedError
	if errors.As(err, &ee) {
		return ee.StatusCode >= 500
	}
	return true // network errors are transient
}
