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

package chunker

import (
	"testing"
)

func TestDataChunker_YAML(t *testing.T) {
	src := `name: my-app
version: 1.0.0
dependencies:
  - foo
  - bar
config:
  host: localhost
  port: 8080
`
	c := NewDataChunker()
	chunks, err := c.Chunk("config.yaml", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 4 {
		t.Fatalf("expected 4 chunks, got %d", len(chunks))
	}
	for _, ch := range chunks {
		if ch.Kind != "key" {
			t.Errorf("chunk %q: Kind = %q, want key", ch.Symbol, ch.Kind)
		}
	}
}

func TestDataChunker_JSON_Object(t *testing.T) {
	src := `{
  "name": "my-app",
  "version": "1.0.0",
  "scripts": {
    "build": "tsc",
    "test": "jest"
  }
}`
	c := NewDataChunker()
	chunks, err := c.Chunk("package.json", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) == 0 {
		t.Fatal("expected chunks, got none")
	}
	for _, ch := range chunks {
		if ch.Kind != "key" {
			t.Errorf("chunk %q: Kind = %q, want key", ch.Symbol, ch.Kind)
		}
	}
}

func TestDataChunker_JSON_Array(t *testing.T) {
	c := NewDataChunker()
	chunks, err := c.Chunk("list.json", []byte(`["a","b","c"]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 1 || chunks[0].Symbol != "root" {
		t.Errorf("expected 1 root chunk, got %d", len(chunks))
	}
}

func TestDataChunker_EmptyYAML(t *testing.T) {
	c := NewDataChunker()
	chunks, err := c.Chunk("empty.yaml", []byte(""))
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 0 {
		t.Errorf("expected 0 chunks, got %d", len(chunks))
	}
}
