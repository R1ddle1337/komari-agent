package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/komari-monitor/komari-agent/dnsresolver"
	"github.com/komari-monitor/komari-agent/ws"
)

// 旧面板的消息格式保留终端、命令执行与 Ping；新文件流仍由 v2 分派处理。
func handleV1Message(conn *ws.SafeConn, raw []byte) {
	var message struct {
		Message    string `json:"message"`
		TerminalID string `json:"request_id"`
		Command    string `json:"command"`
		TaskID     string `json:"task_id"`
		PingTaskID uint   `json:"ping_task_id"`
		PingType   string `json:"ping_type"`
		PingTarget string `json:"ping_target"`
	}
	if err := json.Unmarshal(raw, &message); err != nil {
		log.Println("Bad v1 ws message:", err)
		return
	}
	switch {
	case message.Message == "terminal" || message.TerminalID != "":
		go establishTerminalConnection(flags.Token, message.TerminalID, flags.Endpoint)
	case message.Message == "exec":
		go NewTask(1, message.TaskID, message.Command)
	case message.Message == "ping" || message.PingTaskID != 0 || message.PingType != "" || message.PingTarget != "":
		go NewPingTask(conn, 1, message.PingTaskID, message.PingType, message.PingTarget)
	}
}

func postV1JSON(path string, payload interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	endpoint := strings.TrimSuffix(flags.Endpoint, "/") + path + "?token=" + flags.Token
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := dnsresolver.GetHTTPClientWithPreference(30*time.Second, flags.PreferIPVersion)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &httpStatusError{StatusCode: resp.StatusCode, Status: resp.Status, Body: string(body)}
	}
	_, err = io.Copy(io.Discard, resp.Body)
	return err
}
