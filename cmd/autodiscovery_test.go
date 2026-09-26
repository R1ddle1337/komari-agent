package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestAutoDiscoveryConfigPrivateAndAtomicallyReplaced(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auto-discovery.json")
	first := &AutoDiscoveryConfig{UUID: "test-node", Token: "test-token-one"}
	second := &AutoDiscoveryConfig{UUID: "test-node", Token: "test-token-two"}
	if err := writeAutoDiscoveryConfig(path, first); err != nil {
		t.Fatal(err)
	}
	assertPrivateConfigMode(t, path)
	if err := writeAutoDiscoveryConfig(path, second); err != nil {
		t.Fatal(err)
	}
	got, err := readAutoDiscoveryConfig(path)
	if err != nil || got == nil || *got != *second {
		t.Fatalf("updated config was not preserved: %v", err)
	}
	assertPrivateConfigMode(t, path)
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 || entries[0].Name() != "auto-discovery.json" {
		t.Fatalf("temporary file was not cleaned up: %v", err)
	}
}

func TestAutoDiscoveryConfigTightensExistingPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auto-discovery.json")
	want := &AutoDiscoveryConfig{UUID: "test-node", Token: "test-token"}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readAutoDiscoveryConfig(path)
	if err != nil || got == nil || *got != *want {
		t.Fatalf("existing config was not preserved: %v", err)
	}
	assertPrivateConfigMode(t, path)
	missing, err := readAutoDiscoveryConfig(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil || missing != nil {
		t.Fatalf("missing config must remain a normal first-run condition: %v", err)
	}
}

func assertPrivateConfigMode(t *testing.T, path string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return // Windows uses directory ACLs rather than Unix permission bits.
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %04o, want 0600", info.Mode().Perm())
	}
}
