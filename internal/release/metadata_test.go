package release_test

import (
	"encoding/json"
	"os"
	"testing"
)

func TestDistributionManifestVersionsStayAligned(t *testing.T) {
	t.Parallel()

	manifestVersion := readVersionMap(t, "../../.release-please-manifest.json")["."]
	if manifestVersion == "" {
		t.Fatal("missing root version in .release-please-manifest.json")
	}

	for _, path := range []string{
		"../../.claude-plugin/plugin.json",
		"../../.cursor-plugin/plugin.json",
		"../../package.json",
	} {
		if got := readVersionField(t, path); got != manifestVersion {
			t.Fatalf("%s version = %q, want %q", path, got, manifestVersion)
		}
	}
}

func readVersionMap(t *testing.T, path string) map[string]string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}

	out := map[string]string{}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("Unmarshal(%q): %v", path, err)
	}
	return out
}

func readVersionField(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}

	var out struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("Unmarshal(%q): %v", path, err)
	}
	return out.Version
}
