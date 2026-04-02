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

// Package git provides utilities for detecting git worktrees and finding
// sibling worktree paths.
package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// IsWorktree reports whether projectPath is a git worktree (as opposed to
// the main working tree). A worktree has a .git file pointing at the shared
// .git directory, whereas the main repo has a .git directory.
func IsWorktree(projectPath string) bool {
	info, err := os.Lstat(filepath.Join(projectPath, ".git"))
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// CommonDir returns the shared .git directory for the repository containing
// projectPath, by running "git rev-parse --git-common-dir". Returns an error
// if git is not available or projectPath is not inside a git repository.
func CommonDir(projectPath string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--git-common-dir")
	cmd.Dir = projectPath
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}

	dir := strings.TrimSpace(string(out))
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(projectPath, dir)
	}
	dir = filepath.Clean(dir)
	// Resolve symlinks so paths are comparable across macOS
	// /var → /private/var symlink boundaries.
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	return dir, nil
}

// InternalWorktreePaths returns the relative paths (relative to projectPath)
// of any sibling worktrees that are checked out inside projectPath. These
// should be excluded from indexing to avoid double-counting their files.
// Returns nil if git is unavailable, projectPath is not a repo, or no
// worktrees are nested inside it.
func InternalWorktreePaths(projectPath string) []string {
	worktrees, err := ListWorktrees(projectPath)
	if err != nil {
		return nil
	}

	resolvedRoot := projectPath
	if r, err := filepath.EvalSymlinks(projectPath); err == nil {
		resolvedRoot = r
	}

	var result []string
	for _, wt := range worktrees {
		if wt == resolvedRoot {
			continue
		}
		rel, err := filepath.Rel(resolvedRoot, wt)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		result = append(result, rel)
	}
	return result
}

// RepoRoot returns the absolute path of the root directory of the git
// repository containing projectPath, by running "git rev-parse --show-toplevel".
// Returns an error if git is not available or projectPath is not inside a git
// repository.
func RepoRoot(projectPath string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
	cmd.Dir = projectPath
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}

	root := strings.TrimSpace(string(out))
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	return root, nil
}

// IsGitRoot reports whether path is a git repository or worktree root
// (i.e. contains a .git directory or .git file).
func IsGitRoot(path string) bool {
	_, err := os.Lstat(filepath.Join(path, ".git"))
	return err == nil
}

// DiscoverNestedGitRepos walks rootPath and returns absolute paths of all
// nested directories that are git repo roots. It stops descending into
// discovered repos. Returns nil if rootPath is itself a git root or contains
// no nested repos.
func DiscoverNestedGitRepos(rootPath string) []string {
	if IsGitRoot(rootPath) {
		return nil
	}

	var repos []string
	_ = filepath.WalkDir(rootPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			return nil
		}
		if path == rootPath {
			return nil
		}
		if IsGitRoot(path) {
			repos = append(repos, path)
			return filepath.SkipDir
		}
		return nil
	})
	return repos
}

// ListWorktrees returns the absolute paths of all worktrees (including the
// main working tree) for the repository containing projectPath. Returns nil
// if git is not available or projectPath is not inside a git repository.
func ListWorktrees(projectPath string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "worktree", "list", "--porcelain")
	cmd.Dir = projectPath
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var paths []string
	for _, line := range strings.Split(string(out), "\n") {
		if path, ok := strings.CutPrefix(line, "worktree "); ok {
			// Resolve symlinks so paths are comparable across macOS
			// /var → /private/var symlink boundaries.
			if resolved, err := filepath.EvalSymlinks(path); err == nil {
				path = resolved
			}
			paths = append(paths, path)
		}
	}
	return paths, nil
}
