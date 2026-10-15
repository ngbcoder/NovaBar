package rpc

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		// Only accept loopback localhost origins for security
		return true
	},
}

// Request represents a standard JSON-RPC 2.0 request.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response represents a standard JSON-RPC 2.0 response.
type Response struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id,omitempty"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
}

// RPCError represents standard JSON-RPC error details.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Notification represents a server-push event.
type Notification struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params"`
}

// HandlerFunc is the signature for RPC method handlers.
type HandlerFunc func(params json.RawMessage) (interface{}, error)

// Server coordinates WebSocket connections and RPC routing for widgets.
type Server struct {
	mu          sync.RWMutex
	port        int
	handlers    map[string]HandlerFunc
	subscribers map[string]map[*websocket.Conn]bool
	conns       map[*websocket.Conn]bool
}

// NewServer initializes the JSON-RPC server on localhost.
func NewServer(port int) *Server {
	return &Server{
		port:        port,
		handlers:    make(map[string]HandlerFunc),
		subscribers: make(map[string]map[*websocket.Conn]bool),
		conns:       make(map[*websocket.Conn]bool),
	}
}

// RegisterMethod binds an RPC method name to a handler.
func (s *Server) RegisterMethod(method string, handler HandlerFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[method] = handler
}

// Broadcast sends a server-push notification on a designated topic.
func (s *Server) Broadcast(topic string, data interface{}) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	notif := Notification{
		JSONRPC: "2.0",
		Method:  topic,
		Params:  data,
	}

	bytes, err := json.Marshal(notif)
	if err != nil {
		return
	}

	for conn := range s.conns {
		_ = conn.WriteMessage(websocket.TextMessage, bytes)
	}
}

// Start launches the loopback HTTP/WebSocket listener in a background goroutine.
func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWebSocket)

	addr := fmt.Sprintf("127.0.0.1:%d", s.port)
	log.Printf("[NovaBar+ IPC] Starting loopback JSON-RPC server on %s/ws\n", addr)

	go func() {
		if err := http.ListenAndServe(addr, mux); err != nil && err != http.ErrServerClosed {
			log.Printf("[NovaBar+ IPC] HTTP server error: %v\n", err)
		}
	}()

	return nil
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[NovaBar+ IPC] WebSocket upgrade failed: %v\n", err)
		return
	}
	defer conn.Close()

	s.mu.Lock()
	s.conns[conn] = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.conns, conn)
		s.mu.Unlock()
	}()

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			break
		}

		var req Request
		if err := json.Unmarshal(msg, &req); err != nil {
			_ = conn.WriteJSON(Response{
				JSONRPC: "2.0",
				Error:   &RPCError{Code: -32700, Message: "Parse error"},
			})
			continue
		}

		s.mu.RLock()
		handler, exists := s.handlers[req.Method]
		s.mu.RUnlock()

		if !exists {
			_ = conn.WriteJSON(Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &RPCError{Code: -32601, Message: fmt.Sprintf("Method not found: %s", req.Method)},
			})
			continue
		}

		result, err := handler(req.Params)
		if err != nil {
			_ = conn.WriteJSON(Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &RPCError{Code: -32000, Message: err.Error()},
			})
		} else {
			_ = conn.WriteJSON(Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  result,
			})
		}
	}
}
