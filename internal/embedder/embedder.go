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

// Package embedder defines the interface for generating embedding vectors from text.
package embedder

import (
	"context"
	"fmt"
)

// EmbedError wraps an HTTP error from an embedding API.
type EmbedError struct {
	StatusCode int
	Message    string
}

func (e *EmbedError) Error() string {
	return fmt.Sprintf("embed error (HTTP %d): %s", e.StatusCode, e.Message)
}

// Embedder converts text chunks into vector embeddings.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	Dimensions() int
	ModelName() string
}

// Shared embedding request parameters.
const (
	embedBatchSize  = 32
	embedMaxRetries = 3
)
