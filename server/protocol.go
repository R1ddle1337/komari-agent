package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"

	v2 "github.com/komari-monitor/komari-agent/protocol/v2"
)

var connectionProtocol atomic.Int32

// 每个 Agent 进程只连接一个面板；后台基础信息上报与连接协商共享当前协议。
func uploadProtocolVersion() int {
	if connectionProtocol.Load() == 1 {
		return 1
	}
	return 2
}

func setConnectionProtocolVersion(version int) { connectionProtocol.Store(int32(version)) }

type v2ResponseError struct {
	Code    int
	Message string
}

func (e *v2ResponseError) Error() string {
	return fmt.Sprintf("v2 rpc error %d: %s", e.Code, e.Message)
}

type v2FormatError struct{ err error }

func (e *v2FormatError) Error() string { return e.err.Error() }
func (e *v2FormatError) Unwrap() error { return e.err }

// 仅确定的协议不兼容触发回退；鉴权失败、TLS/网络故障或服务端临时故障不降级。
func shouldFallbackToV1(err error) bool {
	var status *httpStatusError
	if errors.As(err, &status) {
		switch status.StatusCode {
		case http.StatusOK, http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotImplemented:
			return true
		}
	}
	var format *v2FormatError
	if errors.As(err, &format) {
		return true
	}
	var rpc *v2ResponseError
	return errors.As(err, &rpc) && rpc.Code == -32601
}

type httpStatusError struct {
	StatusCode int
	Status     string
	Body       string
}

func (e *httpStatusError) Error() string {
	if e == nil {
		return ""
	}
	if e.Body != "" {
		return fmt.Sprintf("status code: %d,%s", e.StatusCode, e.Body)
	}
	if e.Status != "" {
		return e.Status
	}
	return fmt.Sprintf("status code: %d", e.StatusCode)
}

func parseV2Response(body []byte) (*v2.Response, error) {
	var rpcResp v2.Response
	if err := json.Unmarshal(body, &rpcResp); err != nil {
		return nil, &v2FormatError{fmt.Errorf("invalid v2 JSON-RPC response: %w, body: %s", err, bodySnippet(body))}
	}
	if rpcResp.JSONRPC != v2.Version {
		return nil, &v2FormatError{fmt.Errorf("invalid v2 JSON-RPC version %q, body: %s", rpcResp.JSONRPC, bodySnippet(body))}
	}
	if rpcResp.Error != nil {
		return &rpcResp, &v2ResponseError{Code: rpcResp.Error.Code, Message: rpcResp.Error.Message}
	}
	return &rpcResp, nil
}

func bodySnippet(body []byte) string {
	const max = 120
	if len(body) > max {
		body = body[:max]
	}
	return fmt.Sprintf("%q", string(body))
}
