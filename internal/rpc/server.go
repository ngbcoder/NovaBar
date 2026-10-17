package rpc

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Method and topic names exposed on the loopback interface.
const (
	MethodWorkspaceSwitch = "workspace.switch"
	MethodWorkspaceGet    = "workspace.get"
	MethodLauncherSearch  = "launcher.search"
	MethodLauncherLaunch  = "launcher.launch"
	MethodMediaGet        = "media.get"
	MethodMediaCommand    = "media.command"
	MethodWindowFocus     = "window.focus"
	MethodWindowMove      = "window.move"
	MethodWindowClose     = "window.close"
	MethodWindowsGet      = "windows.get"
	MethodConfigGet       = "config.get"

	TopicWindowsUpdated  = "windows.updated"
	TopicWorkspaceChange = "workspace.changed"
	TopicMediaState      = "media.state"
	TopicSystemStats     = "system.stats"
)

// ipcFileName is the descriptor written to %APPDATA%\NovaBar\ so that shellctl and
// widgets can discover the ephemeral port and auth token.
const ipcFileName = "ipc.json"

// IPCDescriptor is the on-disk discovery record.
type IPCDescriptor struct {
	Port      int    `json:"port"`
	Token     string `json:"token"`
	PID       uint32 `json:"pid"`
	StartedAt string `json:"started_at"`
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

// Standard JSON-RPC error codes plus two application codes.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
	CodeUnauthorized   = -32001
)

// HandlerFunc is the signature for RPC method handlers.
type HandlerFunc func(params json.RawMessage) (interface{}, error)

// Client is one connected widget or CLI session.
type Client struct {
	conn *websocket.Conn

	// writeMu serializes writes. gorilla/websocket permits only one concurrent
	// writer per connection, and two topics firing together would otherwise race.
	writeMu sync.Mutex

	mu   sync.RWMutex
	subs map[string]bool
}

// Send writes one text frame safely.
func (c *Client) Send(v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.WriteMessage(websocket.TextMessage, data)
}

// Subscribe adds a topic to this client's subscription set.
func (c *Client) Subscribe(topic string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.subs == nil {
		c.subs = make(map[string]bool)
	}
	c.subs[topic] = true
}

// Unsubscribe removes a topic.
func (c *Client) Unsubscribe(topic string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.subs, topic)
}

// Subscribed reports whether the client wants a topic.
func (c *Client) Subscribed(topic string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.subs[topic]
}

// Server coordinates WebSocket connections and RPC routing for widgets.
type Server struct {
	mu       sync.RWMutex
	handlers map[string]HandlerFunc
	clients  map[*Client]bool

	// allTopics is the set a client receives when it has not explicitly subscribed.
	// Subscriptions are opt-in: a client sees nothing until it asks.
	allTopics map[string]bool

	port       int
	token      string
	descriptor string
	listener   net.Listener
	httpSrv    *http.Server

	// controlHandler executes CLI control commands. nil disables the endpoint.
	controlHandler ControlHandler

	// assets are static trees mounted for the WebView2 surfaces.
	assets []assetMount
}

// NewServer creates a Server with an ephemeral loopback port and a fresh auth token.
//
// The port is 0 by default so the OS assigns it, and a random token is generated. Both
// are published in %APPDATA%\NovaBar\ipc.json with a user-only ACL. Binding to a
// fixed port with no token would let any web page the user visits drive the shell.
func NewServer() *Server {
	return &Server{
		handlers: make(map[string]HandlerFunc),
		clients:  make(map[*Client]bool),
		allTopics: map[string]bool{
			TopicWindowsUpdated:  true,
			TopicWorkspaceChange: true,
			TopicMediaState:      true,
			TopicSystemStats:     true,
		},
		descriptor: ipcPath(),
	}
}

// ipcPath returns the descriptor location.
func ipcPath() string {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		home, _ := os.UserHomeDir()
		appData = filepath.Join(home, "AppData", "Roaming")
	}
	return filepath.Join(appData, "NovaBar", ipcFileName)
}

// generateToken returns a 256-bit random hex token.
func generateToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("could not generate auth token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// Port returns the bound port, valid after Start.
func (s *Server) Port() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.port
}

// Token returns the session auth token.
func (s *Server) Token() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.token
}

// DescriptorPath returns where the discovery file is written.
func (s *Server) DescriptorPath() string {
	return s.descriptor
}

// RegisterMethod binds an RPC method name to a handler.
func (s *Server) RegisterMethod(method string, handler HandlerFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[method] = handler
}

// SetControlHandler installs the CLI control command handler and exposes the control
// endpoint.
func (s *Server) SetControlHandler(handler ControlHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.controlHandler = handler
}

// SetAssetHandler mounts a static file server, used to serve widget HTML, CSS and JS to
// the WebView2 surfaces.
//
// Deliberately not behind the session token: the assets are static presentation with no
// secrets in them, and the token is injected into the page at runtime rather than baked
// into a file. Keeping assets unauthenticated also means a widget loads even if its
// token has been rotated between process start and navigation.
func (s *Server) SetAssetHandler(prefix string, handler http.Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.assets = append(s.assets, assetMount{prefix: prefix, handler: handler})
}

// assetMount is one mounted static file tree.
type assetMount struct {
	prefix  string
	handler http.Handler
}

// Methods returns the registered method names, sorted.
func (s *Server) Methods() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.handlers))
	for m := range s.handlers {
		out = append(out, m)
	}
	sortStrings(out)
	return out
}

// Broadcast pushes a notification to every client subscribed to topic.
func (s *Server) Broadcast(topic string, data interface{}) {
	s.mu.RLock()
	clients := make([]*Client, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	s.mu.RUnlock()

	notif := Notification{JSONRPC: "2.0", Method: topic, Params: data}
	for _, c := range clients {
		if !c.Subscribed(topic) {
			continue
		}
		if err := c.Send(notif); err != nil {
			log.Printf("[NovaBar IPC] broadcast to client on %s failed: %v\n", topic, err)
		}
	}
}

// Start binds the loopback listener and publishes the discovery descriptor.
func (s *Server) Start() error {
	token, err := generateToken()
	if err != nil {
		return err
	}

	// Loopback only. Binding to all interfaces would expose shell control to the LAN.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("could not bind loopback listener: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWebSocket)
	mux.HandleFunc(ControlPath, s.handleControl)

	// Static mounts for widget assets and the SDK. Registered individually because the
	// widget tree and the SDK live in different directories.
	s.mu.RLock()
	mounts := append([]assetMount(nil), s.assets...)
	s.mu.RUnlock()
	for _, m := range mounts {
		prefix := strings.TrimSuffix(m.prefix, "/")
		mux.Handle(prefix+"/", http.StripPrefix(prefix, m.handler))
	}

	s.mu.Lock()
	s.token = token
	s.port = ln.Addr().(*net.TCPAddr).Port
	s.listener = ln
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	s.httpSrv = srv
	port := s.port
	s.mu.Unlock()

	if err := s.writeDescriptor(port, token); err != nil {
		log.Printf("[NovaBar IPC] Warning: could not write discovery descriptor: %v\n", err)
	}

	// Capture srv in the closure. Reading s.httpSrv here instead would race with
	// Stop, which nils the field: a fast Stop between Start and this goroutine
	// running would otherwise dereference nil inside net/http.
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("[NovaBar IPC] HTTP server error: %v\n", err)
		}
	}()

	log.Printf("[NovaBar IPC] JSON-RPC listening on ws://127.0.0.1:%d/ws (token required)\n", port)
	return nil
}

// writeDescriptor publishes the port and token with a user-only ACL.
func (s *Server) writeDescriptor(port int, token string) error {
	if err := os.MkdirAll(filepath.Dir(s.descriptor), 0700); err != nil {
		return err
	}

	pid := uint32(os.Getpid())
	desc := IPCDescriptor{
		Port:      port,
		Token:     token,
		PID:       pid,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.MarshalIndent(desc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.descriptor, data, 0600); err != nil {
		return err
	}

	// WriteFile honors the mode only when creating; enforce it for an existing file
	// so a stale world-readable descriptor cannot persist.
	return os.Chmod(s.descriptor, 0600)
}

// Stop shuts down the listener and removes the descriptor.
func (s *Server) Stop() {
	s.mu.Lock()
	srv := s.httpSrv
	s.httpSrv = nil
	s.clients = make(map[*Client]bool)
	s.mu.Unlock()

	if srv != nil {
		_ = srv.Close()
	}
	_ = os.Remove(s.descriptor)
}

// upgrader enforces a same-origin check.
func (s *Server) upgrader() *websocket.Upgrader {
	return &websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			if origin == "" {
				// Non-browser clients (shellctl, Go widgets) send no Origin.
				return true
			}
			// Only ever accept an origin naming this exact loopback port.
			return origin == "http://127.0.0.1:"+strconv.Itoa(s.Port()) ||
				origin == "ws://127.0.0.1:"+strconv.Itoa(s.Port())
		},
	}
}

// authorized checks the bearer token. Accepts the Authorization header or a token
// query parameter, because browser WebSocket clients cannot set custom headers.
func (s *Server) authorized(r *http.Request) bool {
	expected := s.Token()
	if expected == "" {
		return false
	}

	if h := r.Header.Get("Authorization"); h != "" {
		if strings.TrimPrefix(h, "Bearer ") == expected {
			return true
		}
	}
	if q := r.URL.Query().Get("token"); q != "" && q == expected {
		return true
	}
	return false
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("unauthorized: missing or invalid NovaBar token"))
		return
	}

	conn, err := s.upgrader().Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[NovaBar IPC] WebSocket upgrade failed: %v\n", err)
		return
	}

	client := &Client{conn: conn, subs: make(map[string]bool)}

	s.mu.Lock()
	s.clients[client] = true
	count := len(s.clients)
	s.mu.Unlock()

	// Log connections: without this there is no way to tell a widget that failed to
	// load from one that loaded and never connected, which are visually identical.
	log.Printf("[NovaBar IPC] widget connected (%d total)\n", count)

	defer func() {
		s.mu.Lock()
		delete(s.clients, client)
		remaining := len(s.clients)
		s.mu.Unlock()
		_ = conn.Close()
		log.Printf("[NovaBar IPC] widget disconnected (%d remaining)\n", remaining)
	}()

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			break
		}
		s.dispatch(client, msg)
	}
}

// dispatch handles one inbound frame.
func (s *Server) dispatch(client *Client, msg []byte) {
	var req Request
	if err := json.Unmarshal(msg, &req); err != nil {
		_ = client.Send(Response{
			JSONRPC: "2.0",
			Error:   &RPCError{Code: CodeParseError, Message: "Parse error"},
		})
		return
	}

	switch req.Method {
	case "subscribe", "unsubscribe":
		s.handleSubscribe(client, msg)
		return
	}

	s.mu.RLock()
	handler, exists := s.handlers[req.Method]
	s.mu.RUnlock()

	if !exists {
		_ = client.Send(Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &RPCError{Code: CodeMethodNotFound, Message: fmt.Sprintf("Method not found: %s", req.Method)},
		})
		return
	}

	result, err := handler(req.Params)
	if err != nil {
		_ = client.Send(Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &RPCError{Code: CodeInternalError, Message: err.Error()},
		})
		return
	}

	_ = client.Send(Response{JSONRPC: "2.0", ID: req.ID, Result: result})
}

func (s *Server) handleSubscribe(client *Client, msg []byte) {
	var raw struct {
		ID     any    `json:"id"`
		Method string `json:"method"`
		Params struct {
			Topic  string   `json:"topic"`
			Topics []string `json:"topics"`
		} `json:"params"`
	}
	if err := json.Unmarshal(msg, &raw); err != nil {
		_ = client.Send(Response{
			JSONRPC: "2.0",
			Error:   &RPCError{Code: CodeParseError, Message: "Parse error"},
		})
		return
	}

	topics := raw.Params.Topics
	if raw.Params.Topic != "" {
		topics = append(topics, raw.Params.Topic)
	}

	s.mu.RLock()
	known := s.allTopics
	s.mu.RUnlock()

	accepted := make([]string, 0, len(topics))
	for _, t := range topics {
		if !known[t] {
			continue
		}
		if raw.Method == "subscribe" {
			client.Subscribe(t)
		} else {
			client.Unsubscribe(t)
		}
		accepted = append(accepted, t)
	}

	_ = client.Send(Response{
		JSONRPC: "2.0",
		ID:      raw.ID,
		Result:  map[string]any{"topics": accepted},
	})

	if len(accepted) > 0 {
		log.Printf("[NovaBar IPC] widget subscribed to %v\n", accepted)
	}
}

// IPCDescriptorPath returns where the discovery descriptor lives.
func IPCDescriptorPath() string { return ipcPath() }

// ReadDescriptor loads the discovery descriptor written by a running daemon.
//
// Returns an error when no daemon is running or the file is unreadable, which callers
// use to fall back to direct recovery.
func ReadDescriptor() (IPCDescriptor, error) {
	var desc IPCDescriptor
	data, err := os.ReadFile(ipcPath())
	if err != nil {
		return desc, err
	}
	if err := json.Unmarshal(data, &desc); err != nil {
		return desc, fmt.Errorf("malformed ipc descriptor at %s: %w", ipcPath(), err)
	}
	if desc.Port == 0 || desc.Token == "" {
		return desc, fmt.Errorf("incomplete ipc descriptor at %s", ipcPath())
	}
	return desc, nil
}

// IsDaemonAlive reports whether the daemon recorded in the descriptor is still
// running. Guards against a stale descriptor left by a crashed daemon.
func IsDaemonAlive(desc IPCDescriptor) bool {
	if desc.PID == 0 {
		return false
	}
	return processAlive(desc.PID)
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
