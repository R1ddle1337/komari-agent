package ws

import (
	"encoding/json"
	"io"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type SafeConn struct {
	conn         *websocket.Conn
	mu           sync.Mutex
	writeTimeout time.Duration
}

const defaultWriteTimeout = 10 * time.Second
const maxControlFrameBytes = 16 << 20

func NewSafeConn(conn *websocket.Conn) *SafeConn {
	conn.SetReadLimit(maxControlFrameBytes)
	return &SafeConn{
		conn:         conn,
		mu:           sync.Mutex{},
		writeTimeout: defaultWriteTimeout,
	}
}

func (sc *SafeConn) WriteMessage(messageType int, data []byte) error {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if err := sc.conn.SetWriteDeadline(time.Now().Add(sc.writeTimeout)); err != nil {
		return err
	}
	return sc.conn.WriteMessage(messageType, data)
}

func (sc *SafeConn) WriteJSON(v interface{}) error {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if err := sc.conn.SetWriteDeadline(time.Now().Add(sc.writeTimeout)); err != nil {
		return err
	}
	return sc.conn.WriteJSON(v)
}

func (sc *SafeConn) Close() error {
	// Close is concurrency-safe and must interrupt a blocked write.
	return sc.conn.Close()
}
func (sc *SafeConn) ReadMessage() (int, []byte, error) {
	typ, reader, err := sc.conn.NextReader()
	if err != nil {
		return 0, nil, err
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxControlFrameBytes+1))
	if err != nil {
		return 0, nil, err
	}
	if len(data) > maxControlFrameBytes {
		_ = sc.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseMessageTooBig, "message too large"), time.Now().Add(time.Second))
		return 0, nil, websocket.ErrReadLimit
	}
	return typ, data, nil
}
func (sc *SafeConn) ReadJSON(v interface{}) error {
	_, data, err := sc.ReadMessage()
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
func (sc *SafeConn) SetReadDeadline(t time.Time) error {
	// sc.mu.Lock()
	// defer sc.mu.Unlock()
	return sc.conn.SetReadDeadline(t)
}
func (sc *SafeConn) GetConn() *websocket.Conn {
	return sc.conn
}
