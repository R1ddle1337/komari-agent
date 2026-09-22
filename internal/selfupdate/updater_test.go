package selfupdate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestVerifiedRawUpdatePreservesMode(t *testing.T) {
	binary := []byte("new verified binary")
	sum := sha256.Sum256(binary)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/owner/agent/releases/assets/1":
			_, _ = w.Write(binary)
		case "/repos/owner/agent/releases/assets/2":
			_, _ = fmt.Fprintf(w, "%s  agent\n", hex.EncodeToString(sum[:]))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	updater, err := NewUpdater(Config{AssetName: "agent", Validator: &SHA2Validator{}, APIBaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "agent")
	if err = os.WriteFile(target, []byte("old"), 0700); err != nil {
		t.Fatal(err)
	}
	err = updater.UpdateTo(&Release{RepoOwner: "owner", RepoName: "agent", AssetID: 1, ValidationAssetID: 2}, target)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != string(binary) {
		t.Fatalf("replacement failed: %q %v", got, err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0700 {
		t.Fatalf("permissions changed: %v", info.Mode())
	}
}

func TestReleaseDetectionChoosesHighestStableVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `[
   {"tag_name":"v1.4.0","assets":[{"id":1,"name":"agent"},{"id":2,"name":"agent.sha256"}]},
   {"tag_name":"v9.0.0","draft":true,"assets":[{"id":3,"name":"agent"}]},
   {"tag_name":"v1.5.0","assets":[{"id":4,"name":"agent"},{"id":5,"name":"agent.sha256"}]},
   {"tag_name":"v2.0.0-beta.1","prerelease":true,"assets":[{"id":6,"name":"agent"}]}
  ]`)
	}))
	defer server.Close()
	updater, _ := NewUpdater(Config{AssetName: "agent", Validator: &SHA2Validator{}, APIBaseURL: server.URL})
	release, found, err := updater.DetectLatest("owner/agent")
	if err != nil || !found || release.Version.String() != "1.5.0" || release.ValidationAssetID != 5 {
		t.Fatalf("stable selection: %#v %v %v", release, found, err)
	}
}

func TestOversizedMetadataRejected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, strings.Repeat(" ", maxMetadataBytes+1))
	}))
	defer server.Close()
	updater, _ := NewUpdater(Config{AssetName: "agent", Validator: &SHA2Validator{}, APIBaseURL: server.URL})
	if _, _, err := updater.DetectLatest("owner/agent"); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized response: %v", err)
	}
}
