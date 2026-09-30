package forkpolicy

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMoccForkHasNoRepositoryNativeGitHubWorkflows(t *testing.T) {
	t.Helper()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve fork-policy test source path")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	workflowDir := filepath.Join(repoRoot, ".github", "workflows")

	entries, err := os.ReadDir(workflowDir)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		t.Fatalf("read GitHub workflow directory: %v", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext == ".yml" || ext == ".yaml" {
			t.Fatalf(
				"MOCC fork must not reintroduce repository-native GitHub workflow %q; "+
					"use the governed central Mocc build/release rail or obtain an explicit "+
					"repo-scoped self-hosted workflow disposition and update this guard",
				entry.Name(),
			)
		}
	}
}
