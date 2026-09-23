package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	v2 "github.com/komari-monitor/komari-agent/protocol/v2"
	"github.com/komari-monitor/komari-agent/ws"
	ping "github.com/prometheus-community/pro-bing"
)

func NewTask(task_id, command string) {
	if task_id == "" {
		return
	}
	if strings.TrimSpace(command) == "" {
		uploadTaskResult(task_id, "No command provided", 0, time.Now())
		return
	}
	if flags.DisableWebSsh {
		uploadTaskResult(task_id, "Remote control is disabled.", -1, time.Now())
		return
	}
	log.Printf("Executing task %s (%d command bytes)", task_id, len(command))
	result, exitCode := runTaskCommand(command)
	uploadTaskResult(task_id, result, exitCode, time.Now())
}

func runTaskCommand(command string) (string, int) {
	cmd, cleanup, err := buildTaskCommand(command)
	if err != nil {
		return err.Error(), -1
	}
	defer cleanup()

	var stdout, stderr taskOutput
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()

	result := stdout.String()
	if stderr.Len() > 0 {
		result = appendErrorResult(result, stderr.String())
	}
	result = strings.ReplaceAll(result, "\r\n", "\n")
	exitCode := 0
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
		} else {
			result = appendErrorResult(result, err.Error())
			exitCode = -1
		}
	}

	return result, exitCode
}

func buildTaskCommand(command string) (*exec.Cmd, func(), error) {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		scriptFile, err := os.CreateTemp("", "komari-task-*.ps1")
		if err != nil {
			return nil, func() {}, err
		}
		cleanup := func() {
			_ = os.Remove(scriptFile.Name())
		}
		if _, err := scriptFile.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
			_ = scriptFile.Close()
			cleanup()
			return nil, func() {}, err
		}
		script := "[Console]::OutputEncoding = [System.Text.Encoding]::UTF8\n" + command
		if _, err := scriptFile.WriteString(script); err != nil {
			_ = scriptFile.Close()
			cleanup()
			return nil, func() {}, err
		}
		if err := scriptFile.Close(); err != nil {
			cleanup()
			return nil, func() {}, err
		}
		cmd = exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", scriptFile.Name())
		return cmd, cleanup, nil
	} else {
		cmd = exec.Command("sh", "-s")
		cmd.Stdin = strings.NewReader(command)
	}
	return cmd, func() {}, nil
}

func appendErrorResult(result, err string) string {
	if result == "" {
		return err
	}
	return result + "\n" + err
}

func uploadTaskResult(taskID, result string, exitCode int, finishedAt time.Time) {
	if err := sendTaskResult(taskID, result, exitCode, finishedAt); err != nil {
		log.Printf("Failed to upload task result: %v", err)
	}
}

func sendTaskResult(taskID, result string, exitCode int, finishedAt time.Time) error {
	params := v2.TaskResultParams{TaskID: taskID, Result: result, ExitCode: exitCode, FinishedAt: finishedAt}
	payload := v2.Request{
		JSONRPC: v2.Version,
		Method:  v2.MethodAgentTaskResult,
		Params:  params,
	}
	return postV2RPC(payload)
}

// resolveIP 解析域名到 IP 地址，排除 DNS 查询时间
func resolveIP(target string) (string, error) {
	// 如果已经是 IP 地址，直接返回
	if ip := net.ParseIP(target); ip != nil {
		return target, nil
	}
	// 解析域名到 IP
	addrs, err := net.LookupHost(target)
	if err != nil || len(addrs) == 0 {
		return "", errors.New("failed to resolve target")
	}
	return addrs[0], nil // 返回第一个解析的 IP
}

func icmpPing(target string, timeout time.Duration) (int64, error) {
	host, _, err := net.SplitHostPort(target)
	if err != nil {
		host = target
	}
	// For ICMP, we only need the host/IP, port is irrelevant.
	// If the host is an IPv6 literal, it might be wrapped in brackets.
	host = strings.Trim(host, "[]")

	// 先解析 IP 地址
	ip, err := resolveIP(host)
	if err != nil {
		return -1, err
	}

	pinger, err := ping.NewPinger(ip)
	if err != nil {
		return -1, err
	}
	pinger.Count = 1
	pinger.Timeout = timeout
	pinger.SetPrivileged(true)
	err = pinger.Run()
	if err != nil {
		return -1, err
	}
	stats := pinger.Statistics()
	if stats.PacketsRecv == 0 {
		return -1, errors.New("no packets received")
	}
	return stats.AvgRtt.Milliseconds(), nil
}

func tcpPing(target string, timeout time.Duration) (int64, error) {
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		// No port, assume port 80
		host = target
		port = "80"
	}

	// If the host is an IPv6 literal, it might be wrapped in brackets.
	host = strings.Trim(host, "[]")

	ip, err := resolveIP(host)
	if err != nil {
		return -1, err
	}

	targetAddr := net.JoinHostPort(ip, port)
	start := time.Now()
	conn, err := net.DialTimeout("tcp", targetAddr, timeout)
	if err != nil {
		return -1, err
	}
	defer conn.Close()
	return time.Since(start).Milliseconds(), nil
}

func httpPing(target string, timeout time.Duration) (int64, error) {
	// Handle raw IPv6 address for URL
	if strings.Contains(target, ":") && !strings.Contains(target, "[") {
		// check if it's a valid IP to avoid wrapping hostnames
		if ip := net.ParseIP(target); ip != nil && ip.To4() == nil {
			target = "[" + target + "]"
		}
	}

	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		target = "http://" + target
	}

	transport := &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			// 在 Dial 之前解析 IP，排除 DNS 时间
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ip, err := resolveIP(host)
			if err != nil {
				return nil, err
			}
			return net.DialTimeout(network, net.JoinHostPort(ip, port), timeout)
		},
	}
	defer transport.CloseIdleConnections()

	client := &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
	start := time.Now()
	resp, err := client.Get(target)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return -1, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		return latency, nil
	}
	return latency, errors.New("http status not ok")
}

func NewPingTask(conn *ws.SafeConn, taskID uint, pingType, pingTarget string) {
	if taskID == 0 {
		log.Printf("Invalid task ID: %d", taskID)
		return
	}
	var err error = nil
	var latency int64
	pingResult := -1
	timeout := 3 * time.Second           // 默认超时时间
	const highLatencyThreshold = 1000    // ms 阈值
	const retryDropThresholdTcping = 800 // ms 重试中延迟降低超过此值则基本认为发生重传
	// 800ms = SYN/SYN-ACK 首次超时重传 1000ms - 防误判容许 200ms 延迟抖动

	measure := func() (int64, error) {
		switch pingType {
		case "icmp":
			return icmpPing(pingTarget, timeout)
		case "tcp":
			return tcpPing(pingTarget, timeout)
		case "http":
			return httpPing(pingTarget, timeout)
		default:
			return -1, errors.New("unsupported ping type")
		}
	}
	PingHighLatencyRetries := 3
	// 首次测量
	if latency, err = measure(); err == nil {
		firstLatency := latency
		if latency > int64(highLatencyThreshold) && PingHighLatencyRetries > 0 {
			attempts := PingHighLatencyRetries
			for i := 0; i < attempts; i++ {
				if second, err2 := measure(); err2 == nil {
					if second <= int64(highLatencyThreshold) {
						if pingType == "tcp" && firstLatency-second > int64(retryDropThresholdTcping) {
							err = errors.New("suspicious retransmission detected in tcp handshake")
							break
						}
						latency = second
						break
					}
					if i == attempts-1 { // 最后一次仍高
						err = errors.New("latency remains high after retries")
					}
				} else {
					err = err2
					break
				}
			}
		}
	}

	if err != nil {
		log.Printf("Ping task %d failed: %v", taskID, err)
		pingResult = -1 // 如果有错误，设置结果为 -1
	} else {
		pingResult = int(latency)
	}
	finishedAt := time.Now()
	if err := sendPingResult(conn, taskID, pingType, pingResult, finishedAt); err != nil {
		log.Printf("Failed to upload ping result: %v", err)
	}
}

func sendPingResult(conn *ws.SafeConn, taskID uint, pingType string, result int, finishedAt time.Time) error {
	payload := v2.BuildPingResultPayload(taskID, pingType, result, finishedAt)
	if conn == nil {
		return postV2RPC(payload)
	}
	return conn.WriteJSON(payload)
}

func postV2RPC(payload interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = postV2Request(body)
	return err
}

func gzipBytes(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(data); err != nil {
		_ = zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
