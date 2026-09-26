package utils

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestSanitizeHTTPErrorPreservesContextWithoutCredentials(t *testing.T) {
	for _, rawURL := range []string{
		"https://panel.example/api/clients/v2/rpc?token=test-agent-secret",
		"https://test-user:test-password@panel.example/api/clients/transfer/test?token=test-agent-secret&transfer_token=test-transfer-secret#test-fragment",
	} {
		t.Run(rawURL, func(t *testing.T) {
			original := &url.Error{Op: "Post", URL: rawURL, Err: context.DeadlineExceeded}
			err := SanitizeHTTPError(fmt.Errorf("send report: %w", original))
			for _, secret := range []string{"test-agent-secret", "test-transfer-secret", "test-user", "test-password", "test-fragment"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatal("error includes credential data")
				}
			}
			for _, context := range []string{"send report", "Post", "panel.example/api/clients/", "deadline exceeded"} {
				if !strings.Contains(err.Error(), context) {
					t.Fatalf("error lost useful context %q", context)
				}
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("error lost its original cause")
			}
			var urlErr *url.Error
			if !errors.As(err, &urlErr) || urlErr != original {
				t.Fatal("error type information was lost")
			}
		})
	}
}

func TestSanitizeHTTPErrorHandlesMissingAndInvalidURLs(t *testing.T) {
	if SanitizeHTTPError(nil) != nil {
		t.Fatal("nil error changed")
	}
	plain := errors.New("connection unavailable")
	if SanitizeHTTPError(plain) != plain {
		t.Fatal("unrelated error changed")
	}
	err := SanitizeHTTPError(&url.Error{Op: "parse", URL: "https://panel.example/%ZZ?token=test-secret", Err: plain})
	if strings.Contains(err.Error(), "test-secret") || !errors.Is(err, plain) {
		t.Fatal("invalid URL was not safely formatted")
	}
}
