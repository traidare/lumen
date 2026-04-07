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

package tui

import (
	"bytes"
	"strings"
	"testing"
)

func TestNewProgress_ReturnsNonNil(t *testing.T) {
	var buf bytes.Buffer
	p := NewProgress(&buf)
	if p == nil {
		t.Fatal("NewProgress returned nil")
	}
}

func TestProgress_StartStop(t *testing.T) {
	var buf bytes.Buffer
	p := NewProgress(&buf)

	p.Start("Indexing", 10)
	p.Update(5, "Processing file 5/10: foo.go")
	p.Stop()

	output := buf.String()
	if output == "" {
		t.Fatal("expected output on writer, got empty string")
	}
}

func TestProgress_Info(t *testing.T) {
	var buf bytes.Buffer
	p := NewProgress(&buf)

	p.Info("Indexing /tmp/project (model: jina-v2, dims: 768)")

	output := buf.String()
	if !strings.Contains(output, "Indexing") {
		t.Errorf("expected output to contain 'Indexing', got %q", output)
	}
}

func TestProgress_AsProgressFunc(t *testing.T) {
	var buf bytes.Buffer
	p := NewProgress(&buf)

	p.Start("Indexing", 5)
	fn := p.AsProgressFunc()

	fn(1, 5, "Processing file 1/5: a.go")
	fn(3, 5, "Processing file 3/5: c.go")
	fn(5, 5, "Done")

	p.Stop()

	output := buf.String()
	if output == "" {
		t.Fatal("expected output from progress func callback, got empty string")
	}
}

func TestProgress_ZeroTotal(t *testing.T) {
	var buf bytes.Buffer
	p := NewProgress(&buf)

	// Should not panic when total is 0
	p.Start("Scanning", 0)
	p.Update(0, "scanning...")
	p.Stop()
}

