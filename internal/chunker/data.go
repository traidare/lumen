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
	"bufio"
	"bytes"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
)

// DataChunker chunks YAML and JSON files by their top-level keys.
// Each top-level key and its associated value block becomes one chunk.
// Oversized chunks pass through to the split.go pipeline.
type DataChunker struct{}

// NewDataChunker returns a new DataChunker.
func NewDataChunker() *DataChunker { return &DataChunker{} }

var yamlTopKey = regexp.MustCompile(`^([a-zA-Z_"'][a-zA-Z0-9_"'\-]*)\s*:`)

// Chunk implements Chunker. Dispatches to YAML or JSON based on file extension.
func (c *DataChunker) Chunk(filePath string, content []byte) ([]Chunk, error) {
	switch strings.ToLower(filepath.Ext(filePath)) {
	case ".json":
		return c.chunkJSON(filePath, content)
	default:
		return c.chunkYAML(filePath, content)
	}
}

func (c *DataChunker) chunkYAML(filePath string, content []byte) ([]Chunk, error) {
	type section struct {
		key       string
		startLine int
		lines     []string
	}

	var chunks []Chunk
	var cur *section

	flush := func(endLine int) {
		if cur == nil {
			return
		}
		body := strings.Join(cur.lines, "\n")
		if strings.TrimSpace(body) != "" {
			chunks = append(chunks, makeChunk(filePath, cur.key, "key", cur.startLine, endLine, body))
		}
		cur = nil
	}

	scanner := bufio.NewScanner(bytes.NewReader(content))
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		// Top-level key: no leading whitespace, not a comment, not a list item
		if line != "" && line[0] != ' ' && line[0] != '\t' && line[0] != '#' && line[0] != '-' {
			if m := yamlTopKey.FindStringSubmatch(line); m != nil {
				flush(lineNum - 1)
				key := strings.Trim(m[1], `"'`)
				cur = &section{key: key, startLine: lineNum, lines: []string{line}}
				continue
			}
		}
		if cur != nil {
			cur.lines = append(cur.lines, line)
		}
	}
	flush(lineNum)
	return chunks, nil
}

func (c *DataChunker) chunkJSON(filePath string, content []byte) ([]Chunk, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(content, &obj); err != nil || len(obj) == 0 {
		// Not a JSON object or empty — emit as single chunk.
		trimmed := strings.TrimSpace(string(content))
		if trimmed == "" {
			return nil, nil
		}
		lines := strings.Count(trimmed, "\n") + 1
		return []Chunk{makeChunk(filePath, "root", "key", 1, lines, trimmed)}, nil
	}

	srcLines := strings.Split(string(content), "\n")
	var chunks []Chunk
	for key, raw := range obj {
		searchKey := `"` + key + `"`
		startLine := 1
		for i, l := range srcLines {
			if strings.Contains(l, searchKey) {
				startLine = i + 1
				break
			}
		}
		rawStr := strings.TrimSpace(string(raw))
		endLine := startLine + strings.Count(rawStr, "\n")
		if endLine > len(srcLines) {
			endLine = len(srcLines)
		}
		body := searchKey + ": " + rawStr
		chunks = append(chunks, makeChunk(filePath, key, "key", startLine, endLine, body))
	}
	return chunks, nil
}
