package server

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUploadSessionRejectsReplacedPartAndKeepsReplacement(t *testing.T) {
	resetUploadStreamState(t)
	dir := t.TempDir()
	spec := uploadStreamSpec{
		Path: filepath.Join(dir, "target.txt"), UploadID: "test-file-identity",
		ChunkCount: 1, ChunkSize: 4, TotalSize: 4, Expected: 4, First: true,
	}
	if _, err := writeUploadStreamChunk(spec, bytes.NewReader([]byte("data"))); err != nil {
		t.Fatal(err)
	}
	partPath := uploadPartPath(t, spec.UploadID)
	uploadChunksMu.Lock()
	state := uploadChunks[spec.UploadID]
	uploadChunksMu.Unlock()
	// Model an external file replacement using ordinary files only. Close the
	// test's handle so this setup also works on Windows.
	if err := state.File.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(partPath, filepath.Join(dir, "original-part")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(partPath, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := writeUploadStreamChunk(spec, bytes.NewReader([]byte("next"))); err == nil || !strings.Contains(err.Error(), "replaced") {
		t.Fatalf("changed part path must be rejected before writing: %v", err)
	}
	args := map[string]interface{}{
		"path": spec.Path, "upload_id": spec.UploadID,
		"total_size": 4, "chunk_size": 4, "chunk_count": 1,
	}
	if _, err := commitFileUpload(args); err == nil || !strings.Contains(err.Error(), "replaced") {
		t.Fatalf("changed part path must not be committed: %v", err)
	}
	if _, err := cancelFileUpload(args); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(partPath)
	if err != nil || string(got) != "replacement" {
		t.Fatalf("cleanup changed an unrelated replacement file: %v", err)
	}
	if _, err := os.Stat(spec.Path); !os.IsNotExist(err) {
		t.Fatal("rejected upload created its destination")
	}
}

func TestUploadSessionCleanupKeepsActiveWrites(t *testing.T) {
	resetUploadStreamState(t)
	spec := uploadStreamSpec{
		Path: filepath.Join(t.TempDir(), "target.txt"), UploadID: "test-idle-session",
		ChunkCount: 1, ChunkSize: 4, TotalSize: 4, Expected: 4, First: true,
	}
	if _, err := writeUploadStreamChunk(spec, bytes.NewReader([]byte("data"))); err != nil {
		t.Fatal(err)
	}
	partPath := uploadPartPath(t, spec.UploadID)
	now := time.Now()
	uploadChunksMu.Lock()
	state := uploadChunks[spec.UploadID]
	state.CreatedAt = now.Add(-uploadSessionIdleTimeout - time.Second)
	state.ActiveWrites = 1
	uploadChunks[spec.UploadID] = state
	pruneUploadSessionsLocked(now)
	_, activeExists := uploadChunks[spec.UploadID]
	state.ActiveWrites = 0
	uploadChunks[spec.UploadID] = state
	pruneUploadSessionsLocked(now)
	_, idleExists := uploadChunks[spec.UploadID]
	uploadChunksMu.Unlock()
	if !activeExists || idleExists {
		t.Fatal("cleanup must keep active writes and release expired idle sessions")
	}
	if _, err := state.File.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("idle upload handle was not closed: %v", err)
	}
	if _, err := os.Stat(partPath); !os.IsNotExist(err) {
		t.Fatalf("idle upload temporary file was not removed: %v", err)
	}
}

func TestCancelUnknownUploadLeavesUntrackedFile(t *testing.T) {
	resetUploadStreamState(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	legacyPath := filepath.Join(dir, ".target.txt.komari-upload-test-session.part")
	if err := os.WriteFile(legacyPath, []byte("untracked"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := cancelFileUpload(map[string]interface{}{"path": target, "upload_id": "test-session"}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(legacyPath)
	if err != nil || string(got) != "untracked" {
		t.Fatalf("unknown session removed an untracked file: %v", err)
	}
}
