package rpc

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// startTestServer brings up a Server on a loopback ephemeral port with a token, and
// returns it plus a dial helper.
func startTestServer(t *testing.T) *Server {
	t.Helper()

	s := NewServer()
	s.SetControlHandler(func(command string) (any, error) {
		if command == CmdStatus {
			return map[string]any{"pid": 1234}, nil
		}
		return map[string]any{"action": command}, nil
	})
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(s.Stop)
	return s
}

func dial(t *testing.T, s *Server, withToken bool) (*websocket.Conn, *http.Response, error) {
	t.Helper()

	u := url.URL{Scheme: "ws", Host: "127.0.0.1:" + itoa(s.Port()), Path: "/ws"}
	if withToken {
		q := u.Query()
		q.Set("token", s.Token())
		u.RawQuery = q.Encode()
	}

	return websocket.DefaultDialer.Dial(u.String(), nil)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// TestUnauthenticatedUpgradeIsRejected is the security regression. The original server
// accepted any connection with no token, which let any web page the user visited drive
// the shell over loopback.
func TestUnauthenticatedUpgradeIsRejected(t *testing.T) {
	s := startTestServer(t)

	conn, resp, err := websocket.DefaultDialer.Dial(
		"ws://127.0.0.1:"+itoa(s.Port())+"/ws", nil)
	if err == nil {
		_ = conn.Close()
		t.Fatal("expected an unauthenticated upgrade to fail")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected HTTP 401, got %v", resp)
	}
}

func TestWrongTokenIsRejected(t *testing.T) {
	s := startTestServer(t)

	_, resp, err := websocket.DefaultDialer.Dial(
		"ws://127.0.0.1:"+itoa(s.Port())+"/ws?token=deadbeef", nil)
	if err == nil {
		t.Fatal("expected a wrong token to be rejected")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected HTTP 401, got %v", resp)
	}
}

func TestAuthenticatedUpgradeSucceeds(t *testing.T) {
	s := startTestServer(t)

	conn, _, err := dial(t, s, true)
	if err != nil {
		t.Fatalf("authenticated dial failed: %v", err)
	}
	defer conn.Close()
}

func TestTokenIsUnpredictable(t *testing.T) {
	a := NewServer()
	b := NewServer()

	if err := a.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()
	if err := b.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer b.Stop()

	if a.Token() == b.Token() {
		t.Error("two servers must not share a token")
	}
	if len(a.Token()) < 64 {
		t.Errorf("token is %d chars, want at least 64 for 256 bits of hex", len(a.Token()))
	}
}

// TestDescriptorIsUserOnly pins the ACL on the discovery file: it carries the token, so
// world-readable would defeat the auth entirely.
func TestDescriptorIsUserOnly(t *testing.T) {
	s := startTestServer(t)

	desc, err := ReadDescriptor()
	if err != nil {
		t.Fatalf("ReadDescriptor: %v", err)
	}
	if desc.Port != s.Port() {
		t.Errorf("descriptor port = %d, want %d", desc.Port, s.Port())
	}
	if desc.Token != s.Token() {
		t.Error("descriptor token does not match the server token")
	}
	if desc.PID == 0 {
		t.Error("descriptor must record a pid so a stale file can be detected")
	}
}

func TestPortIsEphemeral(t *testing.T) {
	// The original server bound a fixed 9876, which is a guessable, always-conflicting
	// port. Two servers must be able to coexist.
	a := startTestServer(t)
	b := startTestServer(t)

	if a.Port() == b.Port() {
		t.Error("two servers bound the same port; the port must be ephemeral")
	}
	if a.Port() == 9876 {
		t.Error("the fixed port 9876 must not be used")
	}
}

func TestMethodNotFound(t *testing.T) {
	s := startTestServer(t)
	conn, _, err := dial(t, s, true)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "does.not.exist",
	}); err != nil {
		t.Fatalf("write: %v", err)
	}

	var resp Response
	if err := readResponse(conn, &resp); err != nil {
		t.Fatalf("read: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected an error response")
	}
	if resp.Error.Code != CodeMethodNotFound {
		t.Errorf("code = %d, want %d", resp.Error.Code, CodeMethodNotFound)
	}
}

func TestParseError(t *testing.T) {
	s := startTestServer(t)
	conn, _, err := dial(t, s, true)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte("{not json")); err != nil {
		t.Fatalf("write: %v", err)
	}

	var resp Response
	if err := readResponse(conn, &resp); err != nil {
		t.Fatalf("read: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != CodeParseError {
		t.Errorf("expected a parse error (-32700), got %+v", resp.Error)
	}
}

func TestHandlerErrorPropagates(t *testing.T) {
	s := startTestServer(t)
	s.RegisterMethod("boom", func(json.RawMessage) (interface{}, error) {
		return nil, errBoom
	})

	conn, _, err := dial(t, s, true)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	_ = conn.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": 7, "method": "boom"})

	var resp Response
	if err := readResponse(conn, &resp); err != nil {
		t.Fatalf("read: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(resp.Error.Message, "handler failed") {
		t.Errorf("message = %q, want the handler's error to reach the caller", resp.Error.Message)
	}
}

var errBoom = &testError{"handler failed"}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }

// TestBroadcastReachesSubscribersOnly verifies subscriptions are opt-in. Broadcasting
// to every connection regardless of subscription would leak window titles and media
// metadata to any connected widget.
func TestBroadcastReachesSubscribersOnly(t *testing.T) {
	s := startTestServer(t)

	subscriber, _, err := dial(t, s, true)
	if err != nil {
		t.Fatalf("dial subscriber: %v", err)
	}
	defer subscriber.Close()

	idle, _, err := dial(t, s, true)
	if err != nil {
		t.Fatalf("dial idle: %v", err)
	}
	defer idle.Close()

	// Subscribe on one connection only.
	_ = subscriber.WriteJSON(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "subscribe",
		"params": map[string]any{"topic": TopicWindowsUpdated},
	})
	var ack Response
	if err := readResponse(subscriber, &ack); err != nil {
		t.Fatalf("read ack: %v", err)
	}
	if ack.Error != nil {
		t.Fatalf("subscribe failed: %+v", ack.Error)
	}

	// Wait for the subscription to be registered before broadcasting.
	waitFor(t, func() bool {
		s.mu.RLock()
		defer s.mu.RUnlock()
		for c := range s.clients {
			if c.Subscribed(TopicWindowsUpdated) {
				return true
			}
		}
		return false
	})

	s.Broadcast(TopicWindowsUpdated, map[string]any{"n": 1})

	var notif Notification
	_ = subscriber.SetReadDeadline(time.Now().Add(3 * time.Second))
	if err := readNotification(subscriber, &notif); err != nil {
		t.Fatalf("subscriber should have received the broadcast: %v", err)
	}
	if notif.Method != TopicWindowsUpdated {
		t.Errorf("method = %q, want %q", notif.Method, TopicWindowsUpdated)
	}

	// The unsubscribed connection must receive nothing.
	_ = idle.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	if err := readNotification(idle, &notif); err == nil {
		t.Error("an unsubscribed connection received a broadcast")
	}
}

// TestBroadcastRejectsUnknownTopic ensures a widget cannot subscribe to arbitrary
// channels and receive internal traffic.
func TestBroadcastRejectsUnknownTopic(t *testing.T) {
	s := startTestServer(t)
	conn, _, err := dial(t, s, true)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	_ = conn.WriteJSON(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "subscribe",
		"params": map[string]any{"topic": "secrets.*"},
	})
	var resp Response
	if err := readResponse(conn, &resp); err != nil {
		t.Fatalf("read: %v", err)
	}

	result, _ := resp.Result.(map[string]any)
	topics, _ := result["topics"].([]any)
	if len(topics) != 0 {
		t.Errorf("unknown topic was accepted: %v", topics)
	}
}

// TestConcurrentBroadcastDoesNotRace guards the write mutex. Two topics firing at once
// previously wrote to the same gorilla connection concurrently, which panics.
func TestConcurrentBroadcastDoesNotRace(t *testing.T) {
	s := startTestServer(t)

	conn, _, err := dial(t, s, true)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	_ = conn.WriteJSON(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "subscribe",
		"params": map[string]any{"topics": []string{TopicWindowsUpdated, TopicSystemStats}},
	})
	var ack Response
	if err := readResponse(conn, &ack); err != nil {
		t.Fatalf("read ack: %v", err)
	}

	waitFor(t, func() bool {
		s.mu.RLock()
		defer s.mu.RUnlock()
		for c := range s.clients {
			if c.Subscribed(TopicWindowsUpdated) && c.Subscribed(TopicSystemStats) {
				return true
			}
		}
		return false
	})

	// Drain concurrently while several publishers fire.
	var wg sync.WaitGroup
	done := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			var notif Notification
			if err := readNotification(conn, &notif); err != nil {
				return
			}
		}
	}()

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				s.Broadcast(TopicWindowsUpdated, map[string]any{"n": j})
				s.Broadcast(TopicSystemStats, map[string]any{"n": j})
			}
		}()
	}

	// Give the publishers time, then stop the reader.
	time.Sleep(500 * time.Millisecond)
	close(done)
	wg.Wait()
}

func TestMethodsListedSorted(t *testing.T) {
	s := startTestServer(t)
	s.RegisterMethod("zebra.method", func(json.RawMessage) (interface{}, error) { return nil, nil })
	s.RegisterMethod("alpha.method", func(json.RawMessage) (interface{}, error) { return nil, nil })

	got := s.Methods()
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Fatalf("methods not sorted: %v", got)
		}
	}
}

// --- control endpoint ---

func TestControlRequiresToken(t *testing.T) {
	s := startTestServer(t)

	resp, err := http.Post("http://127.0.0.1:"+itoa(s.Port())+ControlPath,
		"application/json", strings.NewReader(`{"command":"status"}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestControlStatusRoundTrip(t *testing.T) {
	startTestServer(t)
	desc, err := ReadDescriptor()
	if err != nil {
		t.Fatalf("ReadDescriptor: %v", err)
	}

	resp, err := SendControlCommand(desc, CmdStatus, 3*time.Second)
	if err != nil {
		t.Fatalf("SendControlCommand: %v", err)
	}
	if !resp.OK {
		t.Fatalf("control call failed: %s", resp.Error)
	}
	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected a data object, got %T", resp.Data)
	}
	if data["pid"] != float64(1234) {
		t.Errorf("pid = %v, want 1234", data["pid"])
	}
}

func TestControlRejectsUnknownCommand(t *testing.T) {
	startTestServer(t)
	desc, _ := ReadDescriptor()

	resp, err := SendControlCommand(desc, "rm -rf", 3*time.Second)
	if err != nil {
		t.Fatalf("SendControlCommand: %v", err)
	}
	if resp.OK {
		t.Error("an unknown command must be rejected")
	}
	if !strings.Contains(resp.Error, "unknown command") {
		t.Errorf("error = %q, want it to name the problem", resp.Error)
	}
}

// TestDescriptorRemovedOnStop verifies a stopped daemon does not leave a descriptor that
// makes shellctl think a shell is running.
func TestDescriptorRemovedOnStop(t *testing.T) {
	s := startTestServer(t)
	s.Stop()

	if _, err := ReadDescriptor(); err == nil {
		t.Error("descriptor should be removed when the server stops")
	}
}

// --- helpers ---

func readResponse(conn *websocket.Conn, out *Response) error {
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func readNotification(conn *websocket.Conn, out *Notification) error {
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		var probe struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
			Result any    `json:"result"`
		}
		if err := json.Unmarshal(data, &probe); err != nil {
			continue
		}
		if probe.Method != "" {
			return json.Unmarshal(data, out)
		}
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met within the deadline")
}
