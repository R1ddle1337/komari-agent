package server

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestICMPPing(t *testing.T) {
	// 原始 ICMP 需要系统权限；先确认环境具备能力，测试只访问回环地址。
	probe, err := net.ListenPacket("ip4:icmp", "127.0.0.1")
	if err != nil {
		t.Skipf("raw ICMP is unavailable in this test environment: %v", err)
	}
	probe.Close()
	for _, target := range []string{"127.0.0.1", "127.0.0.1:80"} {
		t.Run(target, func(t *testing.T) {
			latency, err := icmpPing(target, time.Second)
			if err != nil || latency < 0 {
				t.Fatalf("icmpPing(%q) = %d, %v", target, latency, err)
			}
		})
	}
}

func TestTCPPing(t *testing.T) {
	for _, address := range []string{"127.0.0.1:0", "[::1]:0"} {
		t.Run(address, func(t *testing.T) {
			listener, err := net.Listen("tcp", address)
			if err != nil {
				if strings.HasPrefix(address, "[") {
					t.Skipf("IPv6 loopback is unavailable: %v", err)
				}
				t.Fatal(err)
			}
			t.Cleanup(func() { listener.Close() })
			go func() {
				for {
					conn, err := listener.Accept()
					if err != nil {
						return
					}
					conn.Close()
				}
			}()
			if strings.HasPrefix(address, "[") {
				probe, err := net.DialTimeout("tcp6", listener.Addr().String(), time.Second)
				if err != nil {
					t.Skipf("IPv6 loopback connections are unavailable: %v", err)
				}
				probe.Close()
			}
			latency, err := tcpPing(listener.Addr().String(), time.Second)
			if err != nil || latency < 0 {
				t.Fatalf("tcpPing() = %d, %v", latency, err)
			}
		})
	}
}

func TestHTTPPing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/error" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	for _, target := range []string{server.URL, strings.TrimPrefix(server.URL, "http://")} {
		t.Run(target, func(t *testing.T) {
			latency, err := httpPing(target, time.Second)
			if err != nil || latency < 0 {
				t.Fatalf("httpPing(%q) = %d, %v", target, latency, err)
			}
		})
	}
	if _, err := httpPing(server.URL+"/error", time.Second); err == nil {
		t.Fatal("httpPing() must reject a failed HTTP response")
	}
}
