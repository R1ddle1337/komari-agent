package monitoring

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPublicIPParsingRejectsMalformedAndWrongFamily(t *testing.T) {
	for _, test := range []struct {
		body string
		v4   bool
		want string
	}{
		{`{"ip":"203.0.113.7"}`, true, "203.0.113.7"},
		{"ip=2606:4700::1111\nloc=US", false, "2606:4700::1111"},
		{"999.999.999.999", true, ""},
		{"10.0.0.1", true, ""},
		{"fe80::1", false, ""},
		{"1.1.1.1", false, ""},
		{"2001:db8::1", true, ""},
	} {
		if got := parsePublicIP(test.body, test.v4); got != test.want {
			t.Fatalf("parse %q: got %q want %q", test.body, got, test.want)
		}
	}
}

func TestPublicIPLookupSharesTotalDeadlineAndBoundsBodies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/stall":
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		case "/oversize":
			_, _ = w.Write([]byte(strings.Repeat("x", maxPublicIPResponseBytes+1) + "1.1.1.1"))
		default:
			_, _ = w.Write([]byte(`{"ip":"203.0.113.7"}`))
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	began := time.Now()
	if _, err := lookupPublicIP(ctx, server.Client(), []string{server.URL + "/stall", server.URL + "/ok"}, true); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline: %v", err)
	}
	if time.Since(began) > time.Second {
		t.Fatal("stalled body escaped total timeout")
	}
	ip, err := lookupPublicIP(context.Background(), server.Client(), []string{server.URL + "/oversize", server.URL + "/ok"}, true)
	if err != nil || ip != "203.0.113.7" {
		t.Fatalf("bounded fallback: %q %v", ip, err)
	}
}
