package server

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/komari-monitor/komari-agent/dnsresolver"
	v2 "github.com/komari-monitor/komari-agent/protocol/v2"
)

type failingPanelTransport struct{}

func (failingPanelTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, context.DeadlineExceeded
}

func TestPanelRequestErrorsDoNotExposeCredentials(t *testing.T) {
	previous := *flags
	t.Cleanup(func() { *flags = previous })
	flags.Endpoint = "https://panel.example"
	flags.Token = "test-agent-secret"
	flags.PreferIPVersion = "4"
	flags.DisableCompression = true

	controlClient := dnsresolver.GetHTTPClientWithPreference(35*time.Second, flags.PreferIPVersion)
	streamClient := fileStreamHTTPClient()
	for _, client := range []*http.Client{controlClient, streamClient} {
		transport := client.Transport
		t.Cleanup(func() { client.Transport = transport })
		client.Transport = failingPanelTransport{}
	}
	_, err := postV2RequestContext(context.Background(), []byte(`{"jsonrpc":"2.0","method":"test"}`))
	assertSafePanelRequestError(t, err)

	file := filepath.Join(t.TempDir(), "test-file")
	if err := os.WriteFile(file, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := map[string]interface{}{
		"path": file, "offset": 0, "length": 4,
		"transfer_id": "test-transfer", "transfer_token": "test-transfer-secret",
		"upload_id": "test-upload", "total_size": 4, "chunk_size": 4,
		"chunk_index": 0, "chunk_count": 1, "first": true,
	}
	_, err = sendDownloadStream(args)
	assertSafePanelRequestError(t, err)
	_, err = receiveUploadStream(args)
	assertSafePanelRequestError(t, err)
}

func assertSafePanelRequestError(t *testing.T, err error) {
	t.Helper()
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("request error lost its original cause")
	}
	if strings.Contains(err.Error(), "test-agent-secret") || strings.Contains(err.Error(), "test-transfer-secret") {
		t.Fatal("request error includes credentials")
	}
	if !strings.Contains(err.Error(), "panel.example/api/clients/") {
		t.Fatal("request error lost its endpoint context")
	}
}

func TestFileResultRequestConstructionLogOmitsCredentials(t *testing.T) {
	previous := *flags
	writer := log.Writer()
	t.Cleanup(func() { *flags = previous; log.SetOutput(writer) })
	flags.Endpoint = "http://[test.invalid"
	flags.Token = "test-agent-secret"
	var output bytes.Buffer
	log.SetOutput(&output)
	postFileResult(v2.FileResult{})
	if strings.Contains(output.String(), "test-agent-secret") {
		t.Fatal("file result log includes credentials")
	}
	if !strings.Contains(output.String(), "failed to create file result request") {
		t.Fatal("file result log lost its operation context")
	}
}
