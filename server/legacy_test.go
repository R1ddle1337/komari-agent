package server

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	v2 "github.com/komari-monitor/komari-agent/protocol/v2"
)

func preservePanelConfig(t *testing.T) {
	t.Helper()
	previous := *flags
	protocol := connectionProtocol.Load()
	t.Cleanup(func() { *flags = previous; connectionProtocol.Store(protocol) })
	flags.Token = "local-test-token"
	flags.PreferIPVersion = ""
	flags.IgnoreUnsafeCert = false
	flags.DisableCompression = false
	setConnectionProtocolVersion(2)
}

func decodeTestRequest(t *testing.T, r *http.Request) map[string]interface{} {
	t.Helper()
	var body io.Reader = r.Body
	if r.Header.Get("Content-Encoding") == "gzip" {
		reader, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Error(err)
			return nil
		}
		defer reader.Close()
		body = reader
	}
	var result map[string]interface{}
	if err := json.NewDecoder(body).Decode(&result); err != nil {
		t.Error(err)
	}
	return result
}

func TestBasicInfoFallsBackToV1AndRemembersProtocol(t *testing.T) {
	for _, response := range []string{"404", "html"} {
		t.Run(response, func(t *testing.T) {
			preservePanelConfig(t)
			var v1Calls, v2Calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("token") != "local-test-token" {
					t.Error("token not preserved")
				}
				switch r.URL.Path {
				case "/api/clients/v2/rpc":
					v2Calls.Add(1)
					if response == "404" {
						http.NotFound(w, r)
					} else {
						_, _ = io.WriteString(w, "<html>old panel</html>")
					}
				case "/api/clients/uploadBasicInfo":
					v1Calls.Add(1)
					if r.Header.Get("Content-Encoding") != "" {
						t.Error("legacy requests must not be compressed")
					}
					body := decodeTestRequest(t, r)
					if body["cpu_name"] != "test CPU" || body["jsonrpc"] != nil {
						t.Errorf("wrong v1 payload: %+v", body)
					}
					_, _ = io.WriteString(w, `{"status":"success"}`)
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(server.Close)
			flags.Endpoint = server.URL
			for attempt := 0; attempt < 2; attempt++ {
				if err := tryUploadData(map[string]interface{}{"cpu_name": "test CPU"}); err != nil {
					t.Fatal(err)
				}
			}
			if v2Calls.Load() != 1 || v1Calls.Load() != 2 || uploadProtocolVersion() != 1 {
				t.Fatalf("unexpected protocol negotiation: v2=%d v1=%d active=%d", v2Calls.Load(), v1Calls.Load(), uploadProtocolVersion())
			}
		})
	}
}

func TestBasicInfoDoesNotDowngradeForAuthorizationOrServerFailure(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError} {
		preservePanelConfig(t)
		var legacyCalls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/clients/v2/rpc" {
				legacyCalls.Add(1)
			}
			w.WriteHeader(status)
		}))
		flags.Endpoint = server.URL
		err := tryUploadData(map[string]interface{}{"cpu_name": "test CPU"})
		server.Close()
		if err == nil || legacyCalls.Load() != 0 || uploadProtocolVersion() != 2 {
			t.Fatalf("status %d unexpectedly fell back: %v", status, err)
		}
	}
}

func TestLegacyBasicInfoRetriesUnsupportedOptionalFields(t *testing.T) {
	preservePanelConfig(t)
	setConnectionProtocolVersion(1)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body := decodeTestRequest(t, r)
		if body["kernel_version"] != nil || body["cpu_physical_cores"] != nil {
			http.Error(w, "unknown fields", http.StatusBadRequest)
			return
		}
		if body["cpu_name"] != "test CPU" {
			t.Error("required field was lost")
		}
		_, _ = io.WriteString(w, `{"status":"success"}`)
	}))
	t.Cleanup(server.Close)
	flags.Endpoint = server.URL
	data := map[string]interface{}{"cpu_name": "test CPU", "kernel_version": "test", "cpu_physical_cores": 2}
	if err := tryUploadData(data); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || len(data) != 3 {
		t.Fatalf("unexpected retry or original data mutation: calls=%d data=%+v", calls.Load(), data)
	}
}

func TestNegotiatedWebSocketReportsAndPingResults(t *testing.T) {
	for _, protocol := range []int{1, 2} {
		t.Run(string(rune('0'+protocol)), func(t *testing.T) {
			preservePanelConfig(t)
			messages := make(chan map[string]interface{}, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if protocol == 1 && r.URL.Path == "/api/clients/v2/rpc" {
					_, _ = io.WriteString(w, "<html>old panel</html>")
					return
				}
				wantPath := "/api/clients/report"
				if protocol == 2 {
					wantPath = "/api/clients/v2/rpc"
				}
				if r.URL.Path != wantPath {
					t.Errorf("wrong report endpoint %s", r.URL.Path)
					http.NotFound(w, r)
					return
				}
				upgrader := websocket.Upgrader{}
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
				for i := 0; i < 2; i++ {
					var message map[string]interface{}
					if err := conn.ReadJSON(&message); err != nil {
						t.Error(err)
						return
					}
					messages <- message
				}
			}))
			t.Cleanup(server.Close)
			flags.Endpoint = server.URL
			conn, negotiated, err := connectNegotiatedWebSocket(2)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if negotiated != protocol {
				t.Fatalf("negotiated %d, want %d", negotiated, protocol)
			}
			if err := conn.WriteMessage(websocket.TextMessage, buildReportPayload(negotiated, []byte(`{"cpu":42}`))); err != nil {
				t.Fatal(err)
			}
			if err := sendPingResult(conn, negotiated, 7, "tcp", 12, time.Now()); err != nil {
				t.Fatal(err)
			}
			for index := 0; index < 2; index++ {
				select {
				case message := <-messages:
					if protocol == 1 {
						if message["jsonrpc"] != nil {
							t.Fatalf("v1 received JSON-RPC: %+v", message)
						}
						if index == 0 && message["cpu"] != float64(42) {
							t.Fatalf("bad report: %+v", message)
						}
						if index == 1 && (message["type"] != "ping_result" || message["task_id"] != float64(7) || message["value"] != float64(12)) {
							t.Fatalf("bad ping result: %+v", message)
						}
					} else {
						wantMethod := v2.MethodAgentReport
						if index == 1 {
							wantMethod = v2.MethodAgentPingResult
						}
						if message["jsonrpc"] != "2.0" || message["method"] != wantMethod {
							t.Fatalf("bad v2 message: %+v", message)
						}
					}
				case <-time.After(5 * time.Second):
					t.Fatal("timed out waiting for websocket payload")
				}
			}
		})
	}
}

func TestTaskResultSupportsLegacyAndMixedPanels(t *testing.T) {
	for _, test := range []struct {
		name      string
		protocol  int
		response  string
		wantV1    bool
		wantError bool
	}{
		{"legacy", 1, "", true, false},
		{"modern", 2, `{"jsonrpc":"2.0","result":{}}`, false, false},
		{"mixed-1.4.3", 2, `{"jsonrpc":"2.0","error":{"code":-32601,"message":"method not found"}}`, true, false},
		{"denied", 2, `{"jsonrpc":"2.0","error":{"code":-32001,"message":"denied"}}`, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			preservePanelConfig(t)
			var legacyCalls, modernCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body := decodeTestRequest(t, r)
				switch r.URL.Path {
				case "/api/clients/v2/rpc":
					modernCalls.Add(1)
					if body["method"] != v2.MethodAgentTaskResult {
						t.Errorf("bad method: %+v", body)
					}
					_, _ = io.WriteString(w, test.response)
				case "/api/clients/task/result":
					legacyCalls.Add(1)
					if body["task_id"] != "test-task" || body["result"] != "safe fixture" || body["jsonrpc"] != nil {
						t.Errorf("bad v1 task result: %+v", body)
					}
					_, _ = io.WriteString(w, `{"status":"success"}`)
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(server.Close)
			flags.Endpoint = server.URL
			err := sendTaskResult(test.protocol, "test-task", "safe fixture", 0, time.Now())
			if (err != nil) != test.wantError {
				t.Fatalf("sendTaskResult error: %v", err)
			}
			if (legacyCalls.Load() == 1) != test.wantV1 {
				t.Fatalf("unexpected legacy calls: %d", legacyCalls.Load())
			}
			if (modernCalls.Load() == 1) != (test.protocol == 2) {
				t.Fatalf("unexpected modern calls: %d", modernCalls.Load())
			}
			if uploadProtocolVersion() != 2 {
				t.Fatal("method fallback must not downgrade the active v2 connection")
			}
		})
	}
}

func TestFailedLegacyProbePreservesV2POSTFallback(t *testing.T) {
	preservePanelConfig(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/clients/v2/rpc" {
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","result":{}}`)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)
	flags.Endpoint = server.URL
	conn, protocol, err := connectNegotiatedWebSocket(2)
	if conn != nil || err == nil || protocol != 2 {
		t.Fatalf("POST-only panel lost v2 fallback: conn=%v protocol=%d err=%v", conn, protocol, err)
	}
	if _, err := postV2Request(v2.BuildReportRequest("local-test", []byte(`{"cpu":42}`), nil)); err != nil {
		t.Fatalf("v2 POST reporting no longer works: %v", err)
	}
}

func TestV1WebSocketDispatchKeepsRemoteControlDisabled(t *testing.T) {
	preservePanelConfig(t)
	flags.DisableWebSsh = true
	result := make(chan map[string]interface{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/clients/report":
			upgrader := websocket.Upgrader{}
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.Close()
			if err := conn.WriteJSON(map[string]interface{}{"message": "exec", "task_id": "disabled-task", "command": "MUST_NOT_EXECUTE"}); err != nil {
				t.Error(err)
			}
			_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			_, _, _ = conn.ReadMessage()
		case "/api/clients/task/result":
			result <- decodeTestRequest(t, r)
			_, _ = io.WriteString(w, `{"status":"success"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	flags.Endpoint = server.URL
	conn, protocol, err := connectNegotiatedWebSocket(1)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go handleWebSocketMessages(conn, protocol, done)
	select {
	case payload := <-result:
		if payload["task_id"] != "disabled-task" || payload["result"] != "Remote control is disabled." || payload["exit_code"] != float64(-1) {
			t.Fatalf("bad disabled command result: %+v", payload)
		}
	case <-time.After(5 * time.Second):
		t.Error("timed out waiting for disabled command result")
	}
	conn.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("message reader did not stop")
	}
}

func TestPingResultHTTPFallbackKeepsProtocolShape(t *testing.T) {
	for _, protocol := range []int{1, 2} {
		preservePanelConfig(t)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body := decodeTestRequest(t, r)
			if protocol == 1 {
				if r.URL.Path != "/api/clients/ping/result" || body["type"] != "ping_result" || body["jsonrpc"] != nil {
					t.Errorf("bad v1 ping: %s %+v", r.URL.Path, body)
				}
				_, _ = io.WriteString(w, `{"status":"success"}`)
			} else {
				if r.URL.Path != "/api/clients/v2/rpc" || body["method"] != v2.MethodAgentPingResult {
					t.Errorf("bad v2 ping: %s %+v", r.URL.Path, body)
				}
				_, _ = io.WriteString(w, `{"jsonrpc":"2.0","result":{}}`)
			}
		}))
		flags.Endpoint = server.URL
		err := sendPingResult(nil, protocol, 7, "tcp", 12, time.Now())
		server.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestProtocolFallbackRejectsTransportErrors(t *testing.T) {
	if shouldFallbackToV1(io.EOF) {
		t.Fatal("network errors must not cause protocol downgrade")
	}
	if shouldFallbackToV1(&httpStatusError{StatusCode: http.StatusTooManyRequests}) {
		t.Fatal("rate limits must not cause protocol downgrade")
	}
	_, err := parseV2Response([]byte(strings.Repeat("x", 3)))
	if !shouldFallbackToV1(err) {
		t.Fatal("unrecognized endpoint response should permit v1 negotiation")
	}
}
