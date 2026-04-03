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

package cmd

import (
	"os"
	"path/filepath"

	"github.com/ory/lumen/internal/config"
)

// findAncestorIndex walks up from filepath.Dir(path) checking whether any
// ancestor directory has an existing lumen index DB on disk. Returns the
// ancestor path if found, or "" if none exists.
//
// This is the CLI equivalent of indexerCache.findEffectiveRoot for one-shot
// commands (index, search, hook) that don't maintain an in-memory cache.
// Only use for non-git directories; callers should try git.RepoRoot first.
func findAncestorIndex(path, model string) string {
	candidate := filepath.Dir(path)
	for {
		if !pathCrossesSkipDir(candidate, path) {
			if _, err := os.Stat(config.DBPathForProject(candidate, model)); err == nil {
				return candidate
			}
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			break
		}
		candidate = parent
	}
	return ""
}
